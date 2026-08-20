package store

import (
	"context"
	"fmt"
)

// WatchlistSymbols 自选股列表（按排序）。
func (s *Store) WatchlistSymbols(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT symbol FROM watchlist ORDER BY created_at DESC, sort_order DESC LIMIT 10")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sym string
		if err := rows.Scan(&sym); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

// AddLifecycleWatchlist 为 AI 生命周期标的预留自选位。它不会淘汰仍在
// pending_entry/holding 的旧持仓；自选已满 10 只时拒绝新增，等待盘中退出或
// 建仓过期腾位，避免“数据库仍持有但实时自选已被挤掉”的失联状态。
func (s *Store) AddLifecycleWatchlist(ctx context.Context, symbol string) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM watchlist WHERE symbol<>?", symbol).Scan(&count); err != nil {
		return err
	}
	if count >= 10 {
		return fmt.Errorf("自选生命周期已满10只，等待退出或过期后再加入")
	}
	return s.AddWatchlist(ctx, symbol)
}

// AddWatchlist 添加自选股，并只保留最近加入的 10 只。
func (s *Store) AddWatchlist(ctx context.Context, symbol string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM watchlist WHERE symbol=?", symbol); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO watchlist (symbol, sort_order, created_at) SELECT ?, IFNULL(MAX(sort_order),0)+1, NOW() FROM watchlist", symbol); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist WHERE symbol IN (
		SELECT symbol FROM (SELECT symbol FROM watchlist ORDER BY created_at DESC,sort_order DESC LIMIT 18446744073709551615 OFFSET 10) old_items
	)`); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveWatchlist 移除自选股。
func (s *Store) RemoveWatchlist(ctx context.Context, symbol string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM watchlist WHERE symbol=?", symbol)
	return err
}

// RemoveWatchlistAndAbandonLifecycle 处理用户手动移除自选：自选是 AI 生命周期
// 的实时跟踪入口，移除即表示用户放弃该标的，必须同步终结活跃生命周期，
// 否则盘中调度会继续分析一只用户已不关注（甚至已场外卖出）的股票。
//
// 联动口径（与统计口径一一对应）：
//   - pending_entry → expired：撤销建仓候选，与宽限期过期同口径，不产生收益样本；
//   - holding → removed：停止跟踪。没有用户确认的平仓价，收益无法可信结算，
//     不进入胜率/收益/考核统计（区别于 exited 的冻结结算）；
//   - 每条状态流转写入 entry_advice 审计记录，全部动作与自选删除同事务提交。
func (s *Store) RemoveWatchlistAndAbandonLifecycle(ctx context.Context, symbol, tradeDate string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id,status FROM position WHERE symbol=? AND status IN (?,?) FOR UPDATE`,
		symbol, PositionPendingEntry, PositionHolding)
	if err != nil {
		return err
	}
	type activePosition struct {
		id     int64
		status string
	}
	var actives []activePosition
	for rows.Next() {
		var item activePosition
		if err := rows.Scan(&item.id, &item.status); err != nil {
			rows.Close()
			return err
		}
		actives = append(actives, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, active := range actives {
		if active.status == PositionPendingEntry {
			reason := "用户手动移除自选，撤销建仓候选"
			if _, err := tx.ExecContext(ctx,
				`UPDATE position SET status=?,exit_reason=? WHERE id=? AND status=?`,
				PositionExpired, reason, active.id, PositionPendingEntry); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO entry_advice (trade_date,symbol,source,stage,action,reason,urgency,model_name)
				 VALUES (?,?,?,?,?,?,?,?)`,
				tradeDate, symbol, EntrySourceManual, EntryStageEntry, PositionExpired, reason,
				EntryUrgencyNormal, "manual"); err != nil {
				return err
			}
			continue
		}
		reason := "用户手动移除自选，停止跟踪；无确认平仓价，不计入收益统计"
		if _, err := tx.ExecContext(ctx,
			`UPDATE position SET status=?,exit_date=?,exit_reason=?,exit_kind=? WHERE id=? AND status=?`,
			PositionRemoved, tradeDate, reason, ExitKindManual, active.id, PositionHolding); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entry_advice (trade_date,symbol,source,stage,action,reason,urgency,model_name)
			 VALUES (?,?,?,?,?,?,?,?)`,
			tradeDate, symbol, EntrySourceManual, EntryStageExit, PositionRemoved, reason,
			EntryUrgencyNormal, "manual"); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM watchlist WHERE symbol=?", symbol); err != nil {
		return err
	}
	return tx.Commit()
}
