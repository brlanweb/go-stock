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

// ClaimPending 领取一批待处理任务并标记 running（含 failed 重试，retry<5）。
func (s *Store) ClaimPending(ctx context.Context, task string, n int) ([]model.SyncCheckpoint, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT symbol, IFNULL(DATE_FORMAT(last_synced_date,'%Y-%m-%d'),''), retry_count
		 FROM sync_checkpoint
		 WHERE task=? AND (status='pending' OR (status='failed' AND retry_count<5) OR status='running')
		 ORDER BY status='running' DESC, symbol LIMIT ?`, task, n)
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

// MarkDone 标记完成。
func (s *Store) MarkDone(ctx context.Context, task, symbol, lastDate string) error {
	var d interface{}
	if lastDate != "" {
		d = lastDate
	}
	_, err := s.DB.ExecContext(ctx,
		"UPDATE sync_checkpoint SET status='done', last_synced_date=?, last_error='' WHERE symbol=? AND task=?",
		d, symbol, task)
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

// ResetFailed 将失败断点重置为 pending 并清零重试计数（新一轮回填给予重试机会）。
func (s *Store) ResetFailed(ctx context.Context, task string) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		"UPDATE sync_checkpoint SET status='pending', retry_count=0 WHERE task=? AND status='failed'", task)
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

// ReconcileCheckpoints 按库内每只证券的最新日线重建断点状态。达到目标交易日
// 的证券不访问上游；空历史和落后目标的证券才保留为 pending。
func (s *Store) ReconcileCheckpoints(ctx context.Context, task, targetDate string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE sync_checkpoint cp
		LEFT JOIN (
			SELECT symbol, MAX(trade_date) AS last_date
			FROM kline_daily
			GROUP BY symbol
		) k ON k.symbol=cp.symbol
		SET cp.last_synced_date=k.last_date,
			cp.status=CASE WHEN k.last_date IS NOT NULL AND k.last_date>=? THEN 'done' ELSE 'pending' END,
			cp.retry_count=0,
			cp.last_error=''
		WHERE cp.task=?`, targetDate, task)
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
