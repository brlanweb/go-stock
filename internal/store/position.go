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

// 考核样本质量标记（与 022_position_data_quality.sql 对应）。
// 空字符串表示正常样本；t0_violation 表示当日建仓当日退出，违反 A 股 T+1，
// 保留审计价值但不得进入任何考核统计与参数寻优。
const (
	PositionDataQualityOK = ""
	PositionDataQualityT0 = "t0_violation"
)

// 退出归因：区分是确定性风控触发还是 AI 判断，便于复盘各条规则的实际贡献。
const (
	ExitKindAI           = "ai"            // AI 综合三层上下文判断退出
	ExitKindStopLoss     = "stop_loss"     // 相对建仓成本的硬止损
	ExitKindTrailingStop = "trailing_stop" // 移动止盈：自持仓最高价回撤
	ExitKindTakeProfit   = "take_profit"   // 达到目标收益全部落袋
	ExitKindTimeStop     = "time_stop"     // 动量半衰期内未兑现，时间止损
	ExitKindTrendBreak   = "trend_break"   // 趋势结构破位
	ExitKindSystemic     = "systemic"      // 大盘系统性风险
)

// 风控参数。入场 edge（强板块+强个股+人气+建仓机会）的有效期是 1-5 个交易日，
// 因此退出尺度必须对齐到同一量级，而不是用 MA20 这类 20-60 日尺度信号。
// 全部参数集中在此，便于回测扫描与后续按复盘指令动态调整。
const (
	// PositionStopLossPct 硬止损：相对建仓价的最大可接受回撤。
	// 截断亏损是趋势/动量策略盈利公式中权重最大的一项。
	PositionStopLossPct = 6.0

	// PositionStopLossATRMult ATR 自适应止损倍数：高波动股用更宽的止损，
	// 避免被正常波动扫出；最终止损距离取 max(固定值, ATR×倍数) 并受上限约束。
	PositionStopLossATRMult = 1.8
	PositionStopLossMaxPct  = 10.0

	// PositionTrailingArmPct 移动止盈激活阈值：浮盈达到该值后开始保护利润。
	// PositionTrailingGivebackPct 自最高点允许的回撤幅度。
	PositionTrailingArmPct      = 5.0
	PositionTrailingGivebackPct = 4.0

	// PositionTakeProfitPct 硬止盈：动量已充分兑现，落袋为安。
	PositionTakeProfitPct = 12.0

	// PositionTimeStopDays / PositionTimeStopMinPct 时间止损：
	// 建仓后 N 个交易日内浮盈未达到阈值，说明动量优势已衰减，退出腾位。
	PositionTimeStopDays   = 3
	PositionTimeStopMinPct = 3.0

	// PositionMaxHoldDays 最长持有交易日数兜底，防止个别标的长期占用仓位。
	PositionMaxHoldDays = 15

	// PositionMinExitHoldDays 是可以卖出的最小 hold_days，用于强制 A 股 T+1。
	//
	// hold_days 口径见 analysis/entry.go：hold_days = TradingDaysSince(entry_date, today) + 1，
	// 因此建仓当日 hold_days=1，次一交易日 hold_days=2。取 2 表示「建仓当日不可卖出」。
	// 缺少该约束时，10:00 建仓、13:30 触发止损会被记为当日 exited——这在 A 股不可能成交，
	// 会系统性高估止损类样本的收益，并让所有基于这些样本的考核与调参建立在失真数据上。
	PositionMinExitHoldDays = 2

	// PositionReducePct 单次减仓比例；减到低于 PositionMinPositionPct 时直接清仓。
	PositionReducePct      = 50.0
	PositionMinPositionPct = 30.0

	// PositionRoundTripCostPct 往返交易成本（佣金+印花税+过户费+滑点）估算，
	// 用于收益结算，避免统计口径系统性高估。
	PositionRoundTripCostPct = 0.25
)

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
	HighestPrice   *float64 `json:"highest_price"`
	LowestPrice    *float64 `json:"lowest_price"`
	ExitDate       string   `json:"exit_date,omitempty"`
	ExitPrice      *float64 `json:"exit_price"`
	ExitReason     string   `json:"exit_reason,omitempty"`
	ExitKind       string   `json:"exit_kind,omitempty"`
	DataQuality    string   `json:"data_quality,omitempty"`
	HoldDays       int      `json:"hold_days"`
	PositionPct    float64  `json:"position_pct"`
	RealizedPct    float64  `json:"realized_pct"`
	ReferencePrice *float64 `json:"reference_price"`
	ChangePct      *float64 `json:"change_pct"`
	GrossChangePct *float64 `json:"gross_change_pct"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

const positionSelectColumns = `p.id,p.symbol,COALESCE(b.code,''),COALESCE(b.name,''),
	DATE_FORMAT(p.pick_date,'%Y-%m-%d'),DATE_FORMAT(p.analysis_date,'%Y-%m-%d'),p.status,
	COALESCE(DATE_FORMAT(p.entry_date,'%Y-%m-%d'),''),p.entry_price,p.highest_price,p.lowest_price,
	COALESCE(DATE_FORMAT(p.exit_date,'%Y-%m-%d'),''),p.exit_price,p.exit_reason,p.exit_kind,
	p.hold_days,p.position_pct,p.realized_pct,
	DATE_FORMAT(p.created_at,'%Y-%m-%d %H:%i'),DATE_FORMAT(p.updated_at,'%Y-%m-%d %H:%i')`

func scanPositions(rows *sql.Rows) ([]Position, error) {
	defer rows.Close()
	out := []Position{}
	for rows.Next() {
		var item Position
		var entryPrice, exitPrice, highestPrice, lowestPrice sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.Symbol, &item.Code, &item.Name,
			&item.PickDate, &item.AnalysisDate, &item.Status,
			&item.EntryDate, &entryPrice, &highestPrice, &lowestPrice,
			&item.ExitDate, &exitPrice, &item.ExitReason, &item.ExitKind, &item.DataQuality,
			&item.HoldDays, &item.PositionPct, &item.RealizedPct,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if entryPrice.Valid {
			item.EntryPrice = &entryPrice.Float64
		}
		if exitPrice.Valid {
			item.ExitPrice = &exitPrice.Float64
		}
		if highestPrice.Valid {
			item.HighestPrice = &highestPrice.Float64
		}
		if lowestPrice.Valid {
			item.LowestPrice = &lowestPrice.Float64
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

// PositionNetChangePct 把毛收益率换算为扣除往返交易成本后的净收益率。
// 统一在结算口径中扣减，避免「微盈实亏」被统计成盈利单。
func PositionNetChangePct(grossPct float64) float64 {
	return grossPct - PositionRoundTripCostPct
}

// positionBlendedChangePct 合并「已减仓落袋部分」与「剩余仓位」的加权收益：
// realized 是历次减仓时按减仓比例锁定的收益贡献，remaining 是当前仓位的浮动/最终收益。
// 分批减仓后单笔交易的真实收益必须按仓位加权，否则会高估或低估实际盈亏。
//
// positionPct 与 realizedPct 同时为 0 视为「未记录仓位信息」（历史数据或零值结构），
// 按满仓处理；仅当确实减仓到 0（realizedPct 非 0）时才只返回落袋收益。
func positionBlendedChangePct(realizedPct, positionPct, currentGrossPct float64) float64 {
	if positionPct <= 0 {
		if realizedPct == 0 {
			return currentGrossPct
		}
		return realizedPct
	}
	return realizedPct + currentGrossPct*positionPct/100
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
			currentGross := (*item.ReferencePrice / *item.EntryPrice - 1) * 100
			gross := positionBlendedChangePct(item.RealizedPct, item.PositionPct, currentGross)
			net := PositionNetChangePct(gross)
			item.GrossChangePct = &gross
			item.ChangePct = &net
		}
	}
	return items, nil
}

// MarkPositionEntered 把持仓从 pending_entry 推进到 holding，并记录建仓日与建仓参考价。
// 仅允许从 pending_entry 流转，重复建仓建议不会覆盖首次建仓价。
// 建仓时同步初始化 highest/lowest，作为移动止盈与 MAE 复盘的基准。
func (s *Store) MarkPositionEntered(ctx context.Context, id int64, entryDate string, price *float64) error {
	result, err := s.DB.ExecContext(ctx,
		`UPDATE position SET status=?,entry_date=?,entry_price=?,highest_price=?,lowest_price=?,
		 position_pct=100,realized_pct=0 WHERE id=? AND status=?`,
		PositionHolding, entryDate, price, price, price, id, PositionPendingEntry)
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

// UpdatePositionExtremes 刷新持仓期最高/最低价。移动止盈必须基于持仓期间的
// 真实峰值，而不是当日 K 线极值，因此每轮盘中分析都要用实时价推进。
func (s *Store) UpdatePositionExtremes(ctx context.Context, id int64, price float64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE position
		 SET highest_price=GREATEST(COALESCE(highest_price,?),?),
		     lowest_price=LEAST(COALESCE(lowest_price,?),?)
		 WHERE id=? AND status=?`,
		price, price, price, price, id, PositionHolding)
	return err
}

// MarkPositionExited 把持仓置为 exited 并冻结退出价、原因与归因类型。
// 仅允许从 holding 流转；退出后该标的的收益统计随即冻结，不再跟随后续行情。
// 退出与移出自选必须原子完成，否则会出现「库内已退出但自选仍占位」的失联状态。
func (s *Store) MarkPositionExited(ctx context.Context, id int64, exitDate string, price *float64, reason, kind, symbol string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 纵深防御：即使上游 T+1 守卫被绕过，落库时也会把当日进出打成 t0_violation，
	// 保证考核统计永远不会吃进现实中无法成交的样本。
	result, err := tx.ExecContext(ctx,
		`UPDATE position SET status=?,exit_date=?,exit_price=?,exit_reason=?,exit_kind=?,
		 data_quality=IF(entry_date IS NOT NULL AND entry_date=?, ?, data_quality)
		 WHERE id=? AND status=?`,
		PositionExited, exitDate, price, reason, kind, exitDate, PositionDataQualityT0, id, PositionHolding)
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
	// 每笔退出立即生成结构化复盘底稿，与状态流转在同一事务中提交。
	// 这样即使服务在退出后立刻中断，也不会留下「有结算、无复盘」的断链记录。
	if err := savePositionReviewTx(ctx, tx, id); err != nil {
		return err
	}
	if symbol != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist WHERE symbol=?`, symbol); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReducePosition 执行一次分批减仓：按 reducePct 降低仓位比例，并把该部分收益
// 按仓位加权锁定到 realized_pct。减仓后仓位低于最小阈值时由调用方转为清仓。
// 减仓明细与仓位变更在同一事务内完成，保证审计记录与仓位状态一致。
func (s *Store) ReducePosition(ctx context.Context, id int64, tradeDate string, price float64, reducePct, changePct float64, reason string) (float64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var currentPct float64
	if err := tx.QueryRowContext(ctx,
		`SELECT position_pct FROM position WHERE id=? AND status=? FOR UPDATE`,
		id, PositionHolding).Scan(&currentPct); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("持仓 %d 不处于持有状态，忽略减仓", id)
		}
		return 0, err
	}
	if reducePct > currentPct {
		reducePct = currentPct
	}
	if reducePct <= 0 {
		return currentPct, nil
	}
	remaining := currentPct - reducePct
	// 落袋收益按「减仓比例 / 满仓」加权计入，与最终剩余仓位收益可直接相加。
	realizedDelta := changePct * reducePct / 100

	if _, err := tx.ExecContext(ctx,
		`UPDATE position SET position_pct=?,realized_pct=realized_pct+? WHERE id=? AND status=?`,
		remaining, realizedDelta, id, PositionHolding); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO position_reduction (position_id,trade_date,price,reduce_pct,change_pct,reason)
		 VALUES (?,?,?,?,?,?)`,
		id, tradeDate, price, reducePct, changePct, reason); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return remaining, nil
}

// ExpirePosition 把超过建仓宽限期仍未建仓的标的置为 expired（不产生收益样本），
// 并同步移出自选腾位。两步在同一事务内完成，避免自选被无效标的长期占用。
func (s *Store) ExpirePosition(ctx context.Context, id int64, reason, symbol string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE position SET status=?,exit_reason=? WHERE id=? AND status=?`,
		PositionExpired, reason, id, PositionPendingEntry); err != nil {
		return err
	}
	if symbol != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM watchlist WHERE symbol=?`, symbol); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdatePositionHoldDays 刷新已持有交易日数，用于 prompt 上下文与前端展示。
func (s *Store) UpdatePositionHoldDays(ctx context.Context, id int64, days int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE position SET hold_days=? WHERE id=?`, days, id)
	return err
}

// PositionSettlement 是某只推荐股的实际成交结算，用于覆盖纯技术规则的收益追踪口径。
type PositionSettlement struct {
	Status      string
	EntryDate   string
	EntryPrice  *float64
	ExitPrice   *float64
	ExitDate    string
	ExitReason  string
	ExitKind    string
	DataQuality string
	HoldDays    int
	PositionPct float64
	RealizedPct float64
}

// PositionSettlementsByAnalysisDate 返回某个推荐日对应的持仓结算结果（按 symbol 索引）。
// 收益统计优先采用这里的真实建仓/退出价：AI 判定退出后收益立即冻结，
// 不再按技术规则继续追踪；expired（未建仓）标的不参与收益统计。
func (s *Store) PositionSettlementsByAnalysisDate(ctx context.Context, analysisDate string) (map[string]PositionSettlement, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT symbol,status,COALESCE(DATE_FORMAT(entry_date,'%Y-%m-%d'),''),entry_price,exit_price,COALESCE(DATE_FORMAT(exit_date,'%Y-%m-%d'),''),exit_reason,exit_kind,data_quality,hold_days,position_pct,realized_pct
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
		if err := rows.Scan(&symbol, &item.Status, &item.EntryDate, &entryPrice, &exitPrice, &item.ExitDate, &item.ExitReason, &item.ExitKind, &item.DataQuality, &item.HoldDays, &item.PositionPct, &item.RealizedPct); err != nil {
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
