package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hoax/go-stock/internal/model"
)

// InitCheckpoints 为一批 symbol 初始化断点（已存在则跳过）。
func (s *Store) InitCheckpoints(ctx context.Context, task string, symbols []string) error {
	const batch = 500
	for i := 0; i < len(symbols); i += batch {
		end := i + batch
		if end > len(symbols) {
			end = len(symbols)
		}
		part := symbols[i:end]
		var sb strings.Builder
		sb.WriteString("INSERT IGNORE INTO sync_checkpoint (symbol,task,status) VALUES ")
		args := make([]interface{}, 0, len(part)*3)
		for j, sym := range part {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,'pending')")
			args = append(args, sym, task)
		}
		if _, err := s.DB.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("init checkpoints: %w", err)
		}
	}
	return nil
}

// ClaimPending 领取一批待处理任务并标记 running。
// failed 不在同一轮立即重领，避免不受支持的证券连续撞击上游；后续重试由显式重排策略控制。
func (s *Store) ClaimPending(ctx context.Context, task string, n int) ([]model.SyncCheckpoint, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT cp.symbol, IFNULL(DATE_FORMAT(cp.last_synced_date,'%Y-%m-%d'),''), cp.retry_count
		 FROM sync_checkpoint cp
		 INNER JOIN stock_basic b ON b.symbol=cp.symbol
		 WHERE cp.task=? AND b.status='listed' AND cp.status='pending'
		 ORDER BY cp.symbol LIMIT ?`, task, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cps []model.SyncCheckpoint
	for rows.Next() {
		var cp model.SyncCheckpoint
		cp.Task = task
		if err := rows.Scan(&cp.Symbol, &cp.LastSyncedDate, &cp.RetryCount); err != nil {
			return nil, err
		}
		cps = append(cps, cp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, cp := range cps {
		if _, err := s.DB.ExecContext(ctx,
			"UPDATE sync_checkpoint SET status='running' WHERE symbol=? AND task=?", cp.Symbol, task); err != nil {
			return nil, err
		}
	}
	return cps, nil
}

// MarkDone 根据实际入库覆盖范围标记完成。
func (s *Store) MarkDone(ctx context.Context, task, symbol, targetDate string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE sync_checkpoint cp
		INNER JOIN stock_basic b ON b.symbol=cp.symbol
		LEFT JOIN (
			SELECT symbol,MIN(trade_date) first_date,MAX(trade_date) last_date,COUNT(*) kline_count
			FROM kline_daily WHERE symbol=? GROUP BY symbol
		) k ON k.symbol=cp.symbol
		SET cp.first_synced_date=k.first_date,
			cp.last_synced_date=k.last_date,
			cp.kline_count=IFNULL(k.kline_count,0),
			cp.status=CASE
				WHEN b.status<>'listed' THEN 'done'
				WHEN k.last_date IS NOT NULL AND k.last_date>=?
				 AND (b.list_date IS NULL OR b.list_date>? OR k.first_date<=DATE_ADD(b.list_date, INTERVAL 14 DAY))
				THEN 'done' ELSE 'pending' END,
			cp.last_error=''
		WHERE cp.symbol=? AND cp.task=?`, symbol, targetDate, targetDate, symbol, task)
	return err
}

// MarkFailed 标记失败并累加重试计数。
func (s *Store) MarkFailed(ctx context.Context, task, symbol, errMsg string) error {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	_, err := s.DB.ExecContext(ctx,
		"UPDATE sync_checkpoint SET status='failed', retry_count=retry_count+1, last_error=? WHERE symbol=? AND task=?",
		errMsg, symbol, task)
	return err
}

// ResetFailed 保留失败断点及 retry_count，避免每次重启把永久失败证券再次重试。
func (s *Store) ResetFailed(ctx context.Context, task string) (int64, error) {
	return 0, nil
}

// RequeueExhaustedMappedSymbols 只为已具备官方旧代码映射的 920 证券提供一次新策略重试机会。
// 其他永久失败断点保持 failed，避免服务重启后重新撞击上游限流。
func (s *Store) RequeueExhaustedMappedSymbols(ctx context.Context, task string, symbols []string) (int64, error) {
	if len(symbols) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(symbols)), ",")
	args := make([]interface{}, 0, len(symbols)+1)
	args = append(args, task)
	for _, symbol := range symbols {
		args = append(args, symbol)
	}
	res, err := s.DB.ExecContext(ctx,
		"UPDATE sync_checkpoint SET status='pending',retry_count=0,last_error='' WHERE task=? AND status='failed' AND symbol IN ("+placeholders+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ResetRunning 启动时将残留 running 重置为 pending（断点续传）。
func (s *Store) ResetRunning(ctx context.Context, task string) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		"UPDATE sync_checkpoint SET status='pending' WHERE task=? AND status='running'", task)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ReconcileCheckpoints 按库内日期覆盖范围重建断点状态。仅“已覆盖上市日期且最新日线
// 达到目标交易日”的证券才算完成，避免只有最新一天的数据被误判为 done。
func (s *Store) ReconcileCheckpoints(ctx context.Context, task, targetDate string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE sync_checkpoint cp
		INNER JOIN stock_basic b ON b.symbol=cp.symbol
		LEFT JOIN (
			SELECT symbol, MIN(trade_date) AS first_date, MAX(trade_date) AS last_date, COUNT(*) AS kline_count
			FROM kline_daily
			GROUP BY symbol
		) k ON k.symbol=cp.symbol
		SET cp.first_synced_date=k.first_date,
			cp.last_synced_date=k.last_date,
			cp.kline_count=IFNULL(k.kline_count,0),
			cp.status=CASE
				WHEN k.last_date IS NOT NULL AND k.last_date>=?
				 AND (
					b.list_date IS NULL
					OR b.list_date>?
					OR k.first_date<=DATE_ADD(b.list_date, INTERVAL 14 DAY)
				 ) THEN 'done'
				WHEN cp.status='failed' THEN 'failed'
				ELSE 'pending'
			END,
			cp.retry_count=CASE
				WHEN b.status<>'listed' THEN 0
				WHEN cp.status='done' THEN 0
				WHEN cp.status='failed' THEN cp.retry_count
				ELSE cp.retry_count END,
			cp.last_error=CASE WHEN b.status<>'listed' OR cp.status='done' THEN '' ELSE cp.last_error END
		WHERE cp.task=?`, targetDate, targetDate, task)
	if err != nil {
		return 0, fmt.Errorf("reconcile checkpoints: %w", err)
	}
	return res.RowsAffected()
}

// SyncStatus 任务进度汇总。
func (s *Store) SyncStatus(ctx context.Context, task string) (*model.SyncStatus, error) {
	st := &model.SyncStatus{Task: task}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT status, COUNT(*) FROM sync_checkpoint WHERE task=? GROUP BY status", task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var cnt int
		if err := rows.Scan(&status, &cnt); err != nil {
			return nil, err
		}
		st.Total += cnt
		switch status {
		case "done":
			st.Done = cnt
		case "pending":
			st.Pending = cnt
		case "running":
			st.Running = cnt
		case "failed":
			st.Failed = cnt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var complete, partial, empty sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT
		SUM(status='done'),
		SUM(status<>'done' AND kline_count>0),
		SUM(kline_count=0)
		FROM sync_checkpoint WHERE task=?`, task).Scan(&complete, &partial, &empty); err == nil {
		st.Complete, st.Partial, st.Empty = int(complete.Int64), int(partial.Int64), int(empty.Int64)
	}
	latest, err := s.LatestKlineDate(ctx)
	if err == nil {
		st.LatestDate = latest
	}
	return st, nil
}

// CheckpointFor 查询单只断点。
func (s *Store) CheckpointFor(ctx context.Context, task, symbol string) (*model.SyncCheckpoint, error) {
	var cp model.SyncCheckpoint
	cp.Task, cp.Symbol = task, symbol
	err := s.DB.QueryRowContext(ctx,
		`SELECT status, IFNULL(DATE_FORMAT(last_synced_date,'%Y-%m-%d'),''), retry_count, last_error
		 FROM sync_checkpoint WHERE symbol=? AND task=?`, symbol, task).
		Scan(&cp.Status, &cp.LastSyncedDate, &cp.RetryCount, &cp.LastError)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cp, nil
}
