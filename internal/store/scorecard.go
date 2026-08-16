package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ============================================================================
// 考核底稿查询：为四环节评分（选股/机会判断/建仓/离场）提供确定性数据。
//
// 统一口径约定：
//  1. 只有 data_quality='' 的样本参与考核；t0_violation 等失真样本单独计数。
//  2. 已退出交易的净收益 = 按仓位加权的毛收益 - 往返交易成本（与推荐统计一致）。
//  3. 机械基线：分析日后首个交易日开盘买入、持有 mechanicalHoldDays 个交易日
//     收盘卖出，同样扣除往返成本。它是「剥离建仓/离场技巧后的纯选股收益」，
//     也是建仓+离场两环节合计贡献的及格线。
// ============================================================================

// MechanicalHoldDays 是机械基线的固定持有交易日数，与入场 edge 的 1-5 日尺度对齐。
const MechanicalHoldDays = 5

// ScorecardPosition 是一笔参与考核的持仓底稿（holding 或 exited）。
type ScorecardPosition struct {
	ID           int64    `json:"id"`
	Symbol       string   `json:"symbol"`
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	PickDate     string   `json:"pick_date"`
	AnalysisDate string   `json:"analysis_date"`
	EntryDate    string   `json:"entry_date"`
	ExitDate     string   `json:"exit_date,omitempty"`
	EntryPrice   float64  `json:"entry_price"`
	ExitPrice    *float64 `json:"exit_price,omitempty"`
	HighestPrice *float64 `json:"highest_price,omitempty"`
	LowestPrice  *float64 `json:"lowest_price,omitempty"`
	HoldDays     int      `json:"hold_days"`
	PositionPct  float64  `json:"position_pct"`
	RealizedPct  float64  `json:"realized_pct"`
	ExitKind     string   `json:"exit_kind,omitempty"`
	ExitReason   string   `json:"exit_reason,omitempty"`
}

// NetChangePct 返回该笔交易扣除往返成本后的净收益率；未退出时返回 nil。
func (p ScorecardPosition) NetChangePct() *float64 {
	if p.ExitPrice == nil || p.EntryPrice <= 0 {
		return nil
	}
	gross := positionBlendedChangePct(p.RealizedPct, p.PositionPct, (*p.ExitPrice/p.EntryPrice-1)*100)
	net := PositionNetChangePct(gross)
	return &net
}

// ScorecardPositions 返回窗口内已建仓的正常质量样本（holding + exited），
// 按建仓日升序。sinceDate 为空时不限起点。
func (s *Store) ScorecardPositions(ctx context.Context, sinceDate string) ([]ScorecardPosition, error) {
	query := `SELECT p.id,p.symbol,COALESCE(b.code,''),COALESCE(b.name,''),p.status,
		DATE_FORMAT(p.pick_date,'%Y-%m-%d'),DATE_FORMAT(p.analysis_date,'%Y-%m-%d'),
		COALESCE(DATE_FORMAT(p.entry_date,'%Y-%m-%d'),''),COALESCE(DATE_FORMAT(p.exit_date,'%Y-%m-%d'),''),
		p.entry_price,p.exit_price,p.highest_price,p.lowest_price,
		p.hold_days,p.position_pct,p.realized_pct,p.exit_kind,p.exit_reason
		FROM position p LEFT JOIN stock_basic b ON b.symbol=p.symbol
		WHERE p.status IN (?,?) AND p.data_quality='' AND p.entry_date IS NOT NULL AND p.entry_price>0`
	args := []any{PositionHolding, PositionExited}
	if sinceDate != "" {
		query += " AND p.pick_date>=?"
		args = append(args, sinceDate)
	}
	query += " ORDER BY p.entry_date ASC, p.id ASC"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScorecardPosition{}
	for rows.Next() {
		var item ScorecardPosition
		var entry sql.NullFloat64
		var exit, highest, lowest sql.NullFloat64
		if err := rows.Scan(&item.ID, &item.Symbol, &item.Code, &item.Name, &item.Status,
			&item.PickDate, &item.AnalysisDate, &item.EntryDate, &item.ExitDate,
			&entry, &exit, &highest, &lowest,
			&item.HoldDays, &item.PositionPct, &item.RealizedPct, &item.ExitKind, &item.ExitReason); err != nil {
			return nil, err
		}
		if entry.Valid {
			item.EntryPrice = entry.Float64
		}
		if exit.Valid {
			item.ExitPrice = &exit.Float64
		}
		if highest.Valid {
			item.HighestPrice = &highest.Float64
		}
		if lowest.Valid {
			item.LowestPrice = &lowest.Float64
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ScorecardExcludedCount 返回窗口内因数据质量被排除的样本数。
func (s *Store) ScorecardExcludedCount(ctx context.Context, sinceDate string) (int, error) {
	query := `SELECT COUNT(*) FROM position WHERE data_quality<>''`
	args := []any{}
	if sinceDate != "" {
		query += " AND pick_date>=?"
		args = append(args, sinceDate)
	}
	var count int
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

// ScorecardExpiredPicks 返回窗口内宽限期未建仓被放弃的标的（机会判断环节底稿）。
func (s *Store) ScorecardExpiredPicks(ctx context.Context, sinceDate string) ([]ScorecardPosition, error) {
	query := `SELECT p.id,p.symbol,COALESCE(b.code,''),COALESCE(b.name,''),p.status,
		DATE_FORMAT(p.pick_date,'%Y-%m-%d'),DATE_FORMAT(p.analysis_date,'%Y-%m-%d')
		FROM position p LEFT JOIN stock_basic b ON b.symbol=p.symbol
		WHERE p.status=?`
	args := []any{PositionExpired}
	if sinceDate != "" {
		query += " AND p.pick_date>=?"
		args = append(args, sinceDate)
	}
	query += " ORDER BY p.pick_date ASC, p.id ASC"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScorecardPosition{}
	for rows.Next() {
		var item ScorecardPosition
		if err := rows.Scan(&item.ID, &item.Symbol, &item.Code, &item.Name, &item.Status,
			&item.PickDate, &item.AnalysisDate); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// DailyBar 是考核计算所需的最小日 K 视图。
type DailyBar struct {
	Date      string  `json:"date"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	ChangePct float64 `json:"change_pct"`
}

// DailyBarsBetween 返回 [from, to] 区间内的日 K（时间正序）；to 为空表示到最新。
// change_pct 来自行情源、已处理除权，供净值曲线做日度收益衔接。
func (s *Store) DailyBarsBetween(ctx context.Context, symbol, from, to string) ([]DailyBar, error) {
	query := `SELECT DATE_FORMAT(trade_date,'%Y-%m-%d'),open,high,low,close,change_pct
		FROM kline_daily WHERE symbol=? AND trade_date>=?`
	args := []any{symbol, from}
	if to != "" {
		query += " AND trade_date<=?"
		args = append(args, to)
	}
	query += " ORDER BY trade_date ASC LIMIT 500"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyBar{}
	for rows.Next() {
		var bar DailyBar
		if err := rows.Scan(&bar.Date, &bar.Open, &bar.High, &bar.Low, &bar.Close, &bar.ChangePct); err != nil {
			return nil, err
		}
		out = append(out, bar)
	}
	return out, rows.Err()
}

// DailyBarsAfter 返回严格晚于 afterDate 的前 limit 根日 K（时间正序），
// 供机械基线与离场后续动计算使用。
func (s *Store) DailyBarsAfter(ctx context.Context, symbol, afterDate string, limit int) ([]DailyBar, error) {
	if limit <= 0 || limit > 60 {
		limit = MechanicalHoldDays
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT DATE_FORMAT(trade_date,'%Y-%m-%d'),open,high,low,close,change_pct
		 FROM kline_daily WHERE symbol=? AND trade_date>? ORDER BY trade_date ASC LIMIT ?`,
		symbol, afterDate, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyBar{}
	for rows.Next() {
		var bar DailyBar
		if err := rows.Scan(&bar.Date, &bar.Open, &bar.High, &bar.Low, &bar.Close, &bar.ChangePct); err != nil {
			return nil, err
		}
		out = append(out, bar)
	}
	return out, rows.Err()
}

// MechanicalOutcome 是一条机械基线交易的结果。
type MechanicalOutcome struct {
	Symbol       string   `json:"symbol"`
	AfterDate    string   `json:"after_date"`
	EntryDate    string   `json:"entry_date"`
	ExitDate     string   `json:"exit_date"`
	NetPct       float64  `json:"net_pct"`
	BenchmarkPct *float64 `json:"benchmark_pct,omitempty"`
	ExcessPct    *float64 `json:"excess_pct,omitempty"`
}

// MechanicalWindow 计算「afterDate 后首个交易日开盘买入、持有 holdDays 个交易日
// 收盘卖出」的净收益；窗口未走完（仍在追踪）时返回 nil。
// 该口径不含任何建仓/离场判断，是纯选股能力的确定性度量。
func (s *Store) MechanicalWindow(ctx context.Context, symbol, afterDate string, holdDays int) (*MechanicalOutcome, error) {
	if holdDays <= 0 {
		holdDays = MechanicalHoldDays
	}
	bars, err := s.DailyBarsAfter(ctx, symbol, afterDate, holdDays)
	if err != nil {
		return nil, err
	}
	if len(bars) < holdDays {
		return nil, nil // 窗口未走完，不产生样本
	}
	entryOpen := bars[0].Open
	exitClose := bars[holdDays-1].Close
	if entryOpen <= 0 || exitClose <= 0 {
		return nil, nil
	}
	outcome := &MechanicalOutcome{
		Symbol:    symbol,
		AfterDate: afterDate,
		EntryDate: bars[0].Date,
		ExitDate:  bars[holdDays-1].Date,
		NetPct:    PositionNetChangePct((exitClose/entryOpen - 1) * 100),
	}
	// 同窗口基准：沪深300 优先，退回上证指数；两者都缺时超额为 nil。
	for _, benchmark := range []string{reviewBenchmarkSymbol, reviewBenchmarkFallback} {
		base, err := s.indexCloseOn(ctx, benchmark, outcome.EntryDate)
		if err != nil {
			return nil, err
		}
		last, err := s.indexCloseOn(ctx, benchmark, outcome.ExitDate)
		if err != nil {
			return nil, err
		}
		if base > 0 && last > 0 {
			benchmarkPct := (last - base) / base * 100
			excess := outcome.NetPct - benchmarkPct
			outcome.BenchmarkPct = &benchmarkPct
			outcome.ExcessPct = &excess
			break
		}
	}
	return outcome, nil
}

// RecommendationPicksSince 返回 sinceDate（含）以来的全部盘前推荐（date+symbol），
// 供机械选股基线遍历，不做任何收益富化。
func (s *Store) RecommendationPicksSince(ctx context.Context, sinceDate string) ([][2]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT DATE_FORMAT(analysis_date,'%Y-%m-%d'),symbol FROM stock_recommendation
		 WHERE analysis_date>=? ORDER BY analysis_date ASC, rank_no ASC`, sinceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][2]string{}
	for rows.Next() {
		var date, symbol string
		if err := rows.Scan(&date, &symbol); err != nil {
			return nil, err
		}
		out = append(out, [2]string{date, symbol})
	}
	return out, rows.Err()
}

// ReviewPhaseByDate 返回窗口内每个复盘日的 market_phase（up/range/down），
// 供考核统计按市场周期分层。key 为 review_date。
func (s *Store) ReviewPhaseByDate(ctx context.Context, sinceDate string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT DATE_FORMAT(review_date,'%Y-%m-%d'),market_phase FROM daily_review
		 WHERE stage='review' AND market_phase<>'' AND review_date>=?
		 ORDER BY review_date ASC, id ASC`, sinceDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var date, phase string
		if err := rows.Scan(&date, &phase); err != nil {
			return nil, err
		}
		out[date] = phase // 同日多次复盘取最后一次
	}
	return out, rows.Err()
}

// TradingDateBefore 返回严格早于 date 的最近一个交易日；没有时返回空串。
func (s *Store) TradingDateBefore(ctx context.Context, date string) (string, error) {
	var out sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT DATE_FORMAT(MAX(trade_date),'%Y-%m-%d') FROM kline_daily WHERE trade_date<?`, date).Scan(&out)
	if err != nil {
		return "", err
	}
	return out.String, nil
}

// RecommendationSinceDate 返回最近 days 个推荐日中最早的一天；无推荐历史时返回空串。
// 考核窗口与推荐统计使用同一起点，保证口径可对照。
func (s *Store) RecommendationSinceDate(ctx context.Context, days int) (string, error) {
	if days <= 0 || days > 365 {
		days = 60
	}
	dates, err := s.RecommendationHistory(ctx, days)
	if err != nil {
		return "", err
	}
	if len(dates) == 0 {
		return "", nil
	}
	return dates[len(dates)-1], nil
}

// ScorecardEntryAdviceStats 统计窗口内建仓阶段的建议分布（机会判断环节底稿）。
type ScorecardEntryAdviceStats struct {
	EntryCount int `json:"entry_count"`
	WaitCount  int `json:"wait_count"`
}

// EntryAdviceStatsSince 汇总窗口内 stage=entry 的建议动作分布。
func (s *Store) EntryAdviceStatsSince(ctx context.Context, sinceDate string) (ScorecardEntryAdviceStats, error) {
	var stats ScorecardEntryAdviceStats
	rows, err := s.DB.QueryContext(ctx,
		`SELECT action,COUNT(*) FROM entry_advice WHERE stage=? AND trade_date>=? GROUP BY action`,
		EntryStageEntry, sinceDate)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		var count int
		if err := rows.Scan(&action, &count); err != nil {
			return stats, err
		}
		switch action {
		case EntryActionEntry:
			stats.EntryCount = count
		case EntryActionWait:
			stats.WaitCount = count
		}
	}
	return stats, rows.Err()
}

// fixedRiskWindow 用同一批 AI 选股模拟固定止损参数，作为参数敏感性影子组。
// 建仓采用次日开盘；严格执行 T+1（从第二根日K开始检查退出）；止盈统一12%、
// 最长15个交易日。触发日若开盘已越过阈值，按更保守的开盘价成交，避免乐观回测。
func (s *Store) fixedRiskWindow(ctx context.Context, symbol, analysisDate string, stopPct float64) (*float64, bool, error) {
	bars, err := s.DailyBarsAfter(ctx, symbol, analysisDate, PositionMaxHoldDays)
	if err != nil {
		return nil, false, err
	}
	if len(bars) == 0 || bars[0].Open <= 0 {
		return nil, false, nil
	}
	entry := bars[0].Open
	stopPrice := entry * (1 - stopPct/100)
	takePrice := entry * (1 + PositionTakeProfitPct/100)
	for i := 1; i < len(bars); i++ { // i=0 是建仓日，T+1 不可卖
		bar := bars[i]
		var exit float64
		switch {
		case bar.Open <= stopPrice:
			exit = bar.Open // 隔夜跳空不能假设成交在更优的止损线
		case bar.Low <= stopPrice:
			exit = stopPrice
		case bar.Open >= takePrice:
			exit = bar.Open
		case bar.High >= takePrice:
			exit = takePrice
		}
		if exit > 0 {
			value := PositionNetChangePct((exit/entry - 1) * 100)
			return &value, true, nil
		}
	}
	if len(bars) >= PositionMaxHoldDays {
		value := PositionNetChangePct((bars[len(bars)-1].Close/entry - 1) * 100)
		return &value, true, nil
	}
	return nil, false, nil
}

// MarketPhaseBefore 返回该交易日盘前实际已知的最近市场阶段。严格使用 review_date<date，
// 避免把当天17:00才生成的复盘阶段倒灌给当天08:10的推荐，造成前视偏差。
func (s *Store) MarketPhaseBefore(ctx context.Context, date string) (string, error) {
	var phase string
	err := s.DB.QueryRowContext(ctx, `SELECT market_phase FROM daily_review
		WHERE stage='review' AND market_phase<>'' AND review_date<? ORDER BY review_date DESC,id DESC LIMIT 1`, date).Scan(&phase)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return phase, err
}

// ErrScorecardNoData 表示考核窗口内没有任何有效样本。
var ErrScorecardNoData = fmt.Errorf("考核窗口内没有有效样本")
