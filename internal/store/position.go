package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// 持仓状态机取值（与 019_position_lifecycle.sql 对应）。
const (
	PositionPendingEntry = "pending_entry" // 盘前入池，盘中等待建仓点
	PositionHolding      = "holding"       // 已建仓，进入退出机会分析
	PositionExited       = "exited"        // 已择机退出，收益冻结
	PositionExpired      = "expired"       // 宽限期内未建仓，放弃并腾位
)

// PositionEntryGraceDays 是建仓宽限期：入池当日未等到合适建仓点时，
// 允许顺延到之后的交易日继续寻找；超过该交易日数仍未建仓则置为 expired
// 并移出自选，把位置让给新的推荐。趋势交易允许等回踩，但不能无限期占位。
const PositionEntryGraceDays = 2

// Position 是一只 AI 推荐股的完整生命周期记录。
type Position struct {
	ID             int64    `json:"id"`
	Symbol         string   `json:"symbol"`
	Code           string   `json:"code"`
	Name           string   `json:"name"`
	PickDate       string   `json:"pick_date"`
	AnalysisDate   string   `json:"analysis_date"`
	Status         string   `json:"status"`
	EntryDate      string   `json:"entry_date,omitempty"`
	EntryPrice     *float64 `json:"entry_price"`
	ExitDate       string   `json:"exit_date,omitempty"`
	ExitPrice      *float64 `json:"exit_price"`
	ExitReason     string   `json:"exit_reason,omitempty"`
	HoldDays       int      `json:"hold_days"`
	ReferencePrice *float64 `json:"reference_price"`
	ChangePct      *float64 `json:"change_pct"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

const positionSelectColumns = `p.id,p.symbol,COALESCE(b.code,''),COALESCE(b.name,''),
	DATE_FORMAT(p.pick_date,'%Y-%m-%d'),DATE_FORMAT(p.analysis_date,'%Y-%m-%d'),p.status,
	COALESCE(DATE_FORMAT(p.entry_date,'%Y-%m-%d'),''),p.entry_price,
	COALESCE(DATE_FORMAT(p.exit_date,'%Y-%m-%d'),''),p.exit_price,p.exit_reason,p.hold_days,
	DATE_FORMAT(p.created_at,'%Y-%m-%d %H:%i'),DATE_FORMAT(p.updated_at,'%Y-%m-%d %H:%i')`

func scanPositions(rows *sql.Rows) ([]Position, error) {
	defer rows.Close()
	out := []Position{}
	for rows.Next() {
		var item Position
		var entryPrice, exitPrice sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.Symbol, &item.Code, &item.Name,
			&item.PickDate, &item.AnalysisDate, &item.Status,
			&item.EntryDate, &entryPrice, &item.ExitDate, &exitPrice,
			&item.ExitReason, &item.HoldDays, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if entryPrice.Valid {
			item.EntryPrice = &entryPrice.Float64
		}
		if exitPrice.Valid {
			item.ExitPrice = &exitPrice.Float64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// OpenPosition 在盘前推荐选出最佳建仓股后建立 pending_entry 记录。
// 同一 symbol 同一入池日重复调用时保持幂等（不覆盖已流转的状态）。
func (s *Store) OpenPosition(ctx context.Context, symbol, pickDate, analysisDate string) error {
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO position (symbol,pick_date,analysis_date,status) VALUES (?,?,?,?)
		 ON DUPLICATE KEY UPDATE analysis_date=VALUES(analysis_date)`,
		symbol, pickDate, analysisDate, PositionPendingEntry)
	return err
}

// ActivePositions 返回所有仍需盘中分析的持仓（pending_entry + holding），
// 按入池日升序，便于优先处理持有更久的标的。
func (s *Store) ActivePositions(ctx context.Context) ([]Position, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+positionSelectColumns+`
		FROM position p LEFT JOIN stock_basic b ON b.symbol=p.symbol
		WHERE p.status IN (?,?) ORDER BY p.pick_date ASC, p.id ASC`,
		PositionPendingEntry, PositionHolding)
	if err != nil {
		return nil, err
	}
	items, err := scanPositions(rows)
	if err != nil {
		return nil, err
	}
	return s.enrichPositionPerformance(ctx, items)
}

// RecentPositions 返回最近的持仓记录（含已退出与已过期），供前端复盘展示。
func (s *Store) RecentPositions(ctx context.Context, limit int) ([]Position, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+positionSelectColumns+`
		FROM position p LEFT JOIN stock_basic b ON b.symbol=p.symbol
		ORDER BY p.pick_date DESC, p.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	items, err := scanPositions(rows)
	if err != nil {
		return nil, err
	}
	return s.enrichPositionPerformance(ctx, items)
}

func (s *Store) enrichPositionPerformance(ctx context.Context, items []Position) ([]Position, error) {
	for i := range items {
		item := &items[i]
		if item.EntryPrice == nil || *item.EntryPrice <= 0 {
			continue
		}
		var err error
		switch item.Status {
		case PositionExited:
			item.ReferencePrice = item.ExitPrice
		case PositionHolding:
			item.ReferencePrice, err = s.latestPositionReferencePrice(ctx, item.Symbol)
			if err != nil {
				return nil, err
			}
		}
		if item.ReferencePrice != nil && *item.ReferencePrice > 0 {
			value := (*item.ReferencePrice / *item.EntryPrice - 1) * 100
			item.ChangePct = &value
		}
	}
	return items, nil
}

// MarkPositionEntered 把持仓从 pending_entry 推进到 holding，并记录建仓日与建仓参考价。
// 仅允许从 pending_entry 流转，重复建仓建议不会覆盖首次建仓价。
func (s *Store) MarkPositionEntered(ctx context.Context, id int64, entryDate string, price *float64) error {
	result, err := s.DB.ExecContext(ctx,
		`UPDATE position SET status=?,entry_date=?,entry_price=? WHERE id=? AND status=?`,
		PositionHolding, entryDate, price, id, PositionPendingEntry)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("持仓 %d 不处于待建仓状态，忽略建仓流转", id)
	}
	return nil
}

// MarkPositionExited 把持仓置为 exited 并冻结退出价与原因。
// 仅允许从 holding 流转；退出后该标的的收益统计随即冻结，不再跟随后续行情。
func (s *Store) MarkPositionExited(ctx context.Context, id int64, exitDate string, price *float64, reason string) error {
	result, err := s.DB.ExecContext(ctx,
		`UPDATE position SET status=?,exit_date=?,exit_price=?,exit_reason=? WHERE id=? AND status=?`,
		PositionExited, exitDate, price, reason, id, PositionHolding)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("持仓 %d 不处于持有状态，忽略退出流转", id)
	}
	return nil
}

// ExpirePosition 把超过建仓宽限期仍未建仓的标的置为 expired（不产生收益样本）。
func (s *Store) ExpirePosition(ctx context.Context, id int64, reason string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE position SET status=?,exit_reason=? WHERE id=? AND status=?`,
		PositionExpired, reason, id, PositionPendingEntry)
	return err
}

// UpdatePositionHoldDays 刷新已持有交易日数，用于 prompt 上下文与前端展示。
func (s *Store) UpdatePositionHoldDays(ctx context.Context, id int64, days int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE position SET hold_days=? WHERE id=?`, days, id)
	return err
}

// PositionSettlement 是某只推荐股的实际成交结算，用于覆盖纯技术规则的收益追踪口径。
type PositionSettlement struct {
	Status     string
	EntryDate  string
	EntryPrice *float64
	ExitPrice  *float64
	ExitDate   string
	ExitReason string
	HoldDays   int
}

// PositionSettlementsByAnalysisDate 返回某个推荐日对应的持仓结算结果（按 symbol 索引）。
// 收益统计优先采用这里的真实建仓/退出价：AI 判定退出后收益立即冻结，
// 不再按技术规则继续追踪；expired（未建仓）标的不参与收益统计。
func (s *Store) PositionSettlementsByAnalysisDate(ctx context.Context, analysisDate string) (map[string]PositionSettlement, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT symbol,status,COALESCE(DATE_FORMAT(entry_date,'%Y-%m-%d'),''),entry_price,exit_price,COALESCE(DATE_FORMAT(exit_date,'%Y-%m-%d'),''),exit_reason,hold_days
		 FROM position WHERE analysis_date=?`, analysisDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]PositionSettlement)
	for rows.Next() {
		var symbol string
		var item PositionSettlement
		var entryPrice, exitPrice sql.NullFloat64
		if err := rows.Scan(&symbol, &item.Status, &item.EntryDate, &entryPrice, &exitPrice, &item.ExitDate, &item.ExitReason, &item.HoldDays); err != nil {
			return nil, err
		}
		if entryPrice.Valid {
			item.EntryPrice = &entryPrice.Float64
		}
		if exitPrice.Valid {
			item.ExitPrice = &exitPrice.Float64
		}
		out[symbol] = item
	}
	return out, rows.Err()
}

// SectorContext 是持仓所属行业板块的最近交易日表现，用于盘中分析的板块维度。
type SectorContext struct {
	Industry    string  `json:"industry"`
	AvgChange   float64 `json:"avg_change_pct"`
	AvgChange5D float64 `json:"avg_change_5d_pct"`
	UpRatio     float64 `json:"up_ratio"`
	HeatScore   float64 `json:"heat_score"`
	TradeDate   string  `json:"trade_date"`
}

// SectorContextForSymbols 按 symbol 返回其所属行业板块的最近一个交易日统计。
// 板块日统计在收盘后同步，因此盘中拿到的是上一交易日的板块强弱，用于判断
// 题材是否仍在延续；缺失板块映射时返回仅含行业名的记录。
func (s *Store) SectorContextForSymbols(ctx context.Context, symbols []string) (map[string]SectorContext, error) {
	out := make(map[string]SectorContext, len(symbols))
	if len(symbols) == 0 {
		return out, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(symbols)), ",")
	args := make([]interface{}, len(symbols))
	for i := range symbols {
		args[i] = symbols[i]
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT sb.symbol,COALESCE(sb.industry,''),
		COALESCE(d.avg_change,0),COALESCE(d.avg_change_5d,0),COALESCE(d.up_ratio,0),COALESCE(d.heat_score,0),
		COALESCE(DATE_FORMAT(d.trade_date,'%Y-%m-%d'),'')
		FROM stock_basic sb
		LEFT JOIN sector_basic sec ON sec.sector_type='industry' AND sec.sector_name=sb.industry
		LEFT JOIN sector_daily_stats d ON d.sector_code=sec.sector_code
			AND d.trade_date=(SELECT MAX(trade_date) FROM sector_daily_stats)
		WHERE sb.symbol IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var symbol string
		var item SectorContext
		if err := rows.Scan(&symbol, &item.Industry, &item.AvgChange, &item.AvgChange5D,
			&item.UpRatio, &item.HeatScore, &item.TradeDate); err != nil {
			return nil, err
		}
		out[symbol] = item
	}
	return out, rows.Err()
}

// IsTradingDay 判断给定日期（盘中时刻）是否为 A 股交易日。
// 周末直接排除；工作日进一步对比当日最新指数快照与上一日最后一条快照：
// 法定休市（春节、国庆等）时行情上游仍返回上一交易日的收盘静态数据，
// 上证指数的价格与成交额会完全一致；真实交易日开盘后成交额持续累积，
// 必然与上一日收盘值不同。当日没有任何快照（采集停摆）时按非交易日处理，
// 宁可跳过一轮分析也不基于陈旧数据决策。
func (s *Store) IsTradingDay(ctx context.Context, date string, weekend bool) (bool, error) {
	if weekend {
		return false, nil
	}
	var todayPrice, todayAmount float64
	err := s.DB.QueryRowContext(ctx,
		`SELECT price,amount FROM index_snapshot WHERE symbol='SH000001' AND DATE(snapshot_at)=?
		 ORDER BY snapshot_at DESC LIMIT 1`, date).Scan(&todayPrice, &todayAmount)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var prevPrice, prevAmount float64
	err = s.DB.QueryRowContext(ctx,
		`SELECT price,amount FROM index_snapshot WHERE symbol='SH000001' AND DATE(snapshot_at)<?
		 ORDER BY snapshot_at DESC LIMIT 1`, date).Scan(&prevPrice, &prevAmount)
	if err == sql.ErrNoRows {
		// 没有历史快照可对比（新部署首日）：有当日快照即视为交易日。
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return todayPrice != prevPrice || todayAmount != prevAmount, nil
}

// TradingDaysSince 统计 from（含）到 to（不含）之间已经完成的交易日数量，
// 以全市场日 K 的交易日为准。之所以不含 to，是因为盘中分析时当天日 K 尚未
// 落库；这样 D0 入池、D1 盘中会正确得到 1，而不会少算一个交易日。
func (s *Store) TradingDaysSince(ctx context.Context, from, to string) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT trade_date) FROM kline_daily WHERE trade_date>=? AND trade_date<?`,
		from, to).Scan(&count)
	return count, err
}
