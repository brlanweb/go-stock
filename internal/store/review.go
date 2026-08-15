package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// ReviewIndexFact 是复盘日最后一次本地指数快照。
type ReviewIndexFact struct {
	Symbol    string  `json:"symbol"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	ChangePct float64 `json:"change_pct"`
	Amount    float64 `json:"amount"`
}

// ReviewBreadthFact 描述全市场当日涨跌分布，仅统计正常上市股票。
type ReviewBreadthFact struct {
	StockCount   int     `json:"stock_count"`
	UpCount      int     `json:"up_count"`
	FlatCount    int     `json:"flat_count"`
	DownCount    int     `json:"down_count"`
	LimitUpCount int     `json:"limit_up_count"`
	LimitDnCount int     `json:"limit_down_count"`
	UpRatio      float64 `json:"up_ratio"`
	AvgChangePct float64 `json:"avg_change_pct"`
	TotalAmount  float64 `json:"total_amount"`
}

// ReviewSectorFact 是板块强弱复盘所需的确定性统计。
type ReviewSectorFact struct {
	SectorCode  string  `json:"sector_code"`
	SectorName  string  `json:"sector_name"`
	SectorType  string  `json:"sector_type"`
	StockCount  int     `json:"stock_count"`
	AvgChange   float64 `json:"avg_change"`
	AvgChange5D float64 `json:"avg_change_5d"`
	UpRatio     float64 `json:"up_ratio"`
	AmountRatio float64 `json:"amount_ratio"`
	HeatScore   float64 `json:"heat_score"`
}

// ReviewRecommendationFact 将追踪窗口内的盘前推荐与截至复盘日的实际表现对齐。
type ReviewRecommendationFact struct {
	Date        string   `json:"date"`
	Symbol      string   `json:"symbol"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Sector      string   `json:"sector"`
	Probability float64  `json:"probability"`
	RiskScore   *float64 `json:"risk_score"`
	Reason      string   `json:"reason"`
	EntryPrice  *float64 `json:"entry_price"`
	LatestPrice *float64 `json:"latest_price"`
	ChangePct   *float64 `json:"change_pct"`
	TrackedDays int      `json:"tracked_days"`
	Frozen      bool     `json:"frozen"`
	DayChange   *float64 `json:"day_change_pct"`
	// BenchmarkPct 是沪深300在同一追踪窗口的近似涨跌：以分析日收盘指数为基准、
	// 窗口最后交易日收盘指数为终点，与“次日开盘建仓”的个股口径近似对齐。
	BenchmarkPct *float64 `json:"benchmark_change_pct"`
	// ExcessPct = ChangePct - BenchmarkPct，衡量相对大盘的选股贡献。
	ExcessPct *float64 `json:"excess_change_pct"`
}

// ReviewHotspotFact 将盘前热点漏斗选中的概念与复盘日实际板块表现对齐。
type ReviewHotspotFact struct {
	ReportDate string  `json:"report_date"`
	SectorCode string  `json:"sector_code"`
	SectorName string  `json:"sector_name"`
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence"`
	AvgChange  float64 `json:"avg_change"`
	UpRatio    float64 `json:"up_ratio"`
	AmountRat  float64 `json:"amount_ratio"`
	HeatScore  float64 `json:"heat_score"`
}

// ReviewMarketStance 是确定性推演得到的操作姿态指标：用复盘日前 lookback 个
// 交易日的全市场等权涨跌推演净值曲线，按动量/回撤/反弹/宽度直接分类，不经 AI。
// stance 取值：take_profit=落袋、hold=扛单、accumulate=扫货。
type ReviewMarketStance struct {
	Stance       string  `json:"stance"`
	LookbackDays int     `json:"lookback_days"`
	Momentum5D   float64 `json:"momentum_5d_pct"`
	DrawdownPct  float64 `json:"drawdown_pct"`
	ReboundPct   float64 `json:"rebound_pct"`
	UpRatioToday float64 `json:"up_ratio_today"`
	UpRatio5D    float64 `json:"up_ratio_5d"`
	Reason       string  `json:"reason"`
}

// DailyReviewFacts 是 17:00 复盘交给 AI 的唯一行情事实输入。
type DailyReviewFacts struct {
	TradeDate             string                     `json:"trade_date"`
	Indices               []ReviewIndexFact          `json:"indices"`
	Breadth               ReviewBreadthFact          `json:"breadth"`
	MarketStance          ReviewMarketStance         `json:"market_stance"`
	StrongSectors         []ReviewSectorFact         `json:"strong_sectors"`
	WeakSectors           []ReviewSectorFact         `json:"weak_sectors"`
	HotspotChecks         []ReviewHotspotFact        `json:"hotspot_checks"`
	LatestRecommendations []ReviewRecommendationFact `json:"latest_recommendations"`
	RecentStats           RecommendationStats        `json:"recent_recommendation_stats"`
	// PreviousReview 是上一交易日复盘的阶段与优化指令，供本次回验其有效性。
	PreviousReview LatestReviewGuidance `json:"previous_review"`
}

// ReviewRunSummary 描述一次已完成的每日复盘。
type ReviewRunSummary struct {
	ID          int64  `json:"id"`
	ReviewDate  string `json:"review_date"`
	MarketPhase string `json:"market_phase"`
	Model       string `json:"model"`
	CreatedAt   string `json:"created_at"`
}

// RecommendationDirective 是复盘注入次日推荐的受限、结构化优化指令。
type RecommendationDirective struct {
	Action    string `json:"action"`
	Rationale string `json:"rationale"`
}

// ReviewRiskControlGuidance 是复盘生成、注入次日推荐 prompt 的结构化风控参数，
// 与 AI 复盘输出中的 risk_controls 字段对应。
type ReviewRiskControlGuidance struct {
	PositionMode      string   `json:"position_mode"`
	MaxPositionPct    float64  `json:"max_position_pct"`
	MaxSingleStockPct float64  `json:"max_single_stock_pct"`
	StopLossPct       float64  `json:"stop_loss_pct"`
	AvoidConditions   []string `json:"avoid_conditions"`
}

type LatestReviewGuidance struct {
	ReviewDate  string                    `json:"review_date"`
	MarketPhase string                    `json:"market_phase"`
	Directives  []RecommendationDirective `json:"directives"`
	// RiskControls 仅在注入次日推荐时填充；上一日复盘回验（previousReviewGuidance）
	// 只回验 directives，保持为 nil。
	RiskControls *ReviewRiskControlGuidance `json:"risk_controls,omitempty"`
}

func (s *Store) DailyReviewFacts(ctx context.Context, tradeDate string) (DailyReviewFacts, error) {
	facts := DailyReviewFacts{
		TradeDate: tradeDate, Indices: []ReviewIndexFact{}, StrongSectors: []ReviewSectorFact{},
		WeakSectors: []ReviewSectorFact{}, HotspotChecks: []ReviewHotspotFact{},
		LatestRecommendations: []ReviewRecommendationFact{},
	}
	if tradeDate == "" {
		return facts, fmt.Errorf("复盘日期不能为空")
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT i.symbol,i.name,i.price,i.change_pct,i.amount
		FROM index_snapshot i
		INNER JOIN (SELECT symbol,MAX(snapshot_at) snapshot_at FROM index_snapshot WHERE DATE(snapshot_at)=? GROUP BY symbol) latest
		ON latest.symbol=i.symbol AND latest.snapshot_at=i.snapshot_at ORDER BY i.symbol`, tradeDate)
	if err != nil {
		return facts, err
	}
	for rows.Next() {
		var item ReviewIndexFact
		if err := rows.Scan(&item.Symbol, &item.Name, &item.Price, &item.ChangePct, &item.Amount); err != nil {
			rows.Close()
			return facts, err
		}
		facts.Indices = append(facts.Indices, item)
	}
	if err := rows.Close(); err != nil {
		return facts, err
	}

	err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(k.change_pct>0),0),COALESCE(SUM(k.change_pct=0),0),COALESCE(SUM(k.change_pct<0),0),
		COALESCE(SUM(k.change_pct>=9.5),0),COALESCE(SUM(k.change_pct<=-9.5),0),
		COALESCE(AVG(k.change_pct),0),COALESCE(SUM(k.amount),0)
		FROM kline_daily k INNER JOIN stock_basic b ON b.symbol=k.symbol
		WHERE k.trade_date=? AND b.status='listed' AND b.sec_type='stock'`, tradeDate).Scan(
		&facts.Breadth.StockCount, &facts.Breadth.UpCount, &facts.Breadth.FlatCount,
		&facts.Breadth.DownCount, &facts.Breadth.LimitUpCount, &facts.Breadth.LimitDnCount,
		&facts.Breadth.AvgChangePct, &facts.Breadth.TotalAmount)
	if err != nil {
		return facts, err
	}
	if facts.Breadth.StockCount > 0 {
		facts.Breadth.UpRatio = float64(facts.Breadth.UpCount) / float64(facts.Breadth.StockCount)
	}

	facts.MarketStance, err = s.reviewMarketStance(ctx, tradeDate)
	if err != nil {
		return facts, err
	}

	facts.StrongSectors, err = s.reviewSectors(ctx, tradeDate, "DESC")
	if err != nil {
		return facts, err
	}
	facts.WeakSectors, err = s.reviewSectors(ctx, tradeDate, "ASC")
	if err != nil {
		return facts, err
	}

	var recommendationDates []string
	err = func() error {
		rows, err := s.DB.QueryContext(ctx, `SELECT DATE_FORMAT(analysis_date,'%Y-%m-%d') AS recommendation_date
			FROM stock_recommendation WHERE analysis_date<=? GROUP BY analysis_date ORDER BY analysis_date DESC LIMIT 5`, tradeDate)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var date string
			if err := rows.Scan(&date); err != nil {
				return err
			}
			recommendationDates = append(recommendationDates, date)
		}
		return rows.Err()
	}()
	if err != nil {
		return facts, err
	}
	for _, recommendationDate := range recommendationDates {
		items, err := s.RecommendationsByDate(ctx, recommendationDate)
		if err != nil {
			return facts, err
		}
		for _, item := range items {
			fact := ReviewRecommendationFact{
				Date: item.Date, Symbol: item.Symbol, Code: item.Code, Name: item.Name, Sector: item.Sector,
				Probability: item.Probability, RiskScore: item.RiskScore, Reason: item.Reason,
				EntryPrice: item.EntryPrice, LatestPrice: item.LatestPrice, ChangePct: item.ChangePct,
				TrackedDays: item.TrackedDays, Frozen: item.Exited,
			}
			var dayChange sql.NullFloat64
			err := s.DB.QueryRowContext(ctx, `SELECT change_pct FROM kline_daily WHERE symbol=? AND trade_date=?`, item.Symbol, tradeDate).Scan(&dayChange)
			if err != nil && err != sql.ErrNoRows {
				return facts, err
			}
			if dayChange.Valid {
				value := dayChange.Float64
				fact.DayChange = &value
			}
			if err := s.fillReviewBenchmark(ctx, &fact); err != nil {
				return facts, err
			}
			facts.LatestRecommendations = append(facts.LatestRecommendations, fact)
		}
	}
	facts.HotspotChecks, err = s.reviewHotspotChecks(ctx, tradeDate)
	if err != nil {
		return facts, err
	}
	facts.PreviousReview, err = s.previousReviewGuidance(ctx, tradeDate)
	if err != nil {
		return facts, err
	}
	facts.RecentStats, err = s.RecommendationOverallStats(ctx, 60)
	return facts, err
}

// reviewBenchmarkSymbol 是推荐超额收益的基准指数；本地无沪深300时自动退回上证指数。
const reviewBenchmarkSymbol = "SH000300"
const reviewBenchmarkFallback = "SH000001"

// fillReviewBenchmark 计算推荐股追踪窗口内的基准指数涨跌与超额收益。
// 口径近似：个股为“分析日后首个交易日开盘 → 窗口最后交易日收盘”；指数快照没有
// 日内开盘价，因此以“分析日收盘指数 → 窗口最后交易日收盘指数”近似同一窗口。
func (s *Store) fillReviewBenchmark(ctx context.Context, fact *ReviewRecommendationFact) error {
	if fact.ChangePct == nil || fact.TrackedDays == 0 {
		return nil
	}
	var lastDate string
	// 趋势跟踪口径下追踪天数不固定，用该股实际已追踪的天数对齐基准窗口。
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(DATE_FORMAT(MAX(t.trade_date),'%Y-%m-%d'),'') FROM (
		SELECT trade_date FROM kline_daily WHERE symbol=? AND trade_date>? ORDER BY trade_date ASC LIMIT ?
	) t`, fact.Symbol, fact.Date, fact.TrackedDays).Scan(&lastDate)
	if err != nil || lastDate == "" {
		return err
	}
	for _, benchmark := range []string{reviewBenchmarkSymbol, reviewBenchmarkFallback} {
		base, err := s.indexCloseOn(ctx, benchmark, fact.Date)
		if err != nil {
			return err
		}
		last, err := s.indexCloseOn(ctx, benchmark, lastDate)
		if err != nil {
			return err
		}
		if base > 0 && last > 0 {
			benchmarkPct := (last - base) / base * 100
			excess := *fact.ChangePct - benchmarkPct
			fact.BenchmarkPct = &benchmarkPct
			fact.ExcessPct = &excess
			return nil
		}
	}
	return nil
}

// indexCloseOn 返回指数在指定日期最后一次快照价格；无数据返回 0。
func (s *Store) indexCloseOn(ctx context.Context, symbol, date string) (float64, error) {
	var price float64
	err := s.DB.QueryRowContext(ctx, `SELECT price FROM index_snapshot WHERE symbol=? AND DATE(snapshot_at)=? ORDER BY snapshot_at DESC LIMIT 1`, symbol, date).Scan(&price)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return price, err
}

// reviewHotspotChecks 读取最近一次盘前热点漏斗 final 报告的卡点概念，并对齐复盘日
// 的实际板块统计。热点报告在交易日 08:00 基于前一收盘生成，report_date 为前一交易日。
func (s *Store) reviewHotspotChecks(ctx context.Context, tradeDate string) ([]ReviewHotspotFact, error) {
	out := []ReviewHotspotFact{}
	var raw, reportDate string
	err := s.DB.QueryRowContext(ctx, `SELECT payload,DATE_FORMAT(report_date,'%Y-%m-%d')
		FROM hotspot_report WHERE stage='final' AND report_date<=? ORDER BY id DESC LIMIT 1`, tradeDate).Scan(&raw, &reportDate)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var report struct {
		Concepts []struct {
			SectorCode string  `json:"sector_code"`
			SectorName string  `json:"sector_name"`
			Status     string  `json:"status"`
			Confidence float64 `json:"confidence"`
		} `json:"concepts"`
	}
	if err := json.Unmarshal([]byte(raw), &report); err != nil || len(report.Concepts) == 0 {
		return out, nil
	}
	for _, concept := range report.Concepts {
		item := ReviewHotspotFact{
			ReportDate: reportDate, SectorCode: concept.SectorCode, SectorName: concept.SectorName,
			Status: concept.Status, Confidence: concept.Confidence,
		}
		err := s.DB.QueryRowContext(ctx, `SELECT avg_change,up_ratio,amount_ratio,heat_score
			FROM sector_daily_stats WHERE sector_code=? AND trade_date=?`, concept.SectorCode, tradeDate).
			Scan(&item.AvgChange, &item.UpRatio, &item.AmountRat, &item.HeatScore)
		if err == sql.ErrNoRows {
			continue // 当日无该概念统计（如成分不足），跳过而非编造 0 值
		}
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// previousReviewGuidance 读取复盘日之前最近一次复盘的阶段与指令，供本次回验。
func (s *Store) previousReviewGuidance(ctx context.Context, tradeDate string) (LatestReviewGuidance, error) {
	var guidance LatestReviewGuidance
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT DATE_FORMAT(review_date,'%Y-%m-%d'),market_phase,payload
		FROM daily_review WHERE stage='review' AND review_date<? ORDER BY id DESC LIMIT 1`, tradeDate).
		Scan(&guidance.ReviewDate, &guidance.MarketPhase, &raw)
	if err == sql.ErrNoRows {
		guidance.Directives = []RecommendationDirective{}
		return guidance, nil
	}
	if err != nil {
		return guidance, err
	}
	var payload struct {
		Directives []RecommendationDirective `json:"directives"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return guidance, fmt.Errorf("解析上次复盘指令: %w", err)
	}
	if len(payload.Directives) > 5 {
		payload.Directives = payload.Directives[:5]
	}
	guidance.Directives = payload.Directives
	if guidance.Directives == nil {
		guidance.Directives = []RecommendationDirective{}
	}
	return guidance, nil
}

// reviewStanceLookbackDays 是操作姿态推演的回看窗口（交易日数）。
const reviewStanceLookbackDays = 20

// reviewMarketStance 用最近 20 个交易日的全市场等权涨跌推演净值曲线，按固定
// 规则分类出操作姿态。全过程为确定性计算，AI 复盘只能引用不能改写：
//   - take_profit（落袋）：5 日等权动量 ≥ +3% 且贴近 20 日高点（回撤 ≤ 1%），
//     短期涨幅已兑现，优先锁定利润；
//   - accumulate（扫货）：距 20 日高点回撤 ≥ 4% 且 5 日动量 ≥ -1%、当日上涨
//     占比 ≥ 50%，回调充分且开始企稳，适合分批吸纳；
//   - hold（扛单）：其余情况，包括下跌未企稳（不接飞刀）与趋势中段，持仓等待。
func (s *Store) reviewMarketStance(ctx context.Context, tradeDate string) (ReviewMarketStance, error) {
	stance := ReviewMarketStance{Stance: "hold", LookbackDays: reviewStanceLookbackDays, Reason: "历史数据不足，默认持仓等待"}
	rows, err := s.DB.QueryContext(ctx, `SELECT DATE_FORMAT(t.trade_date,'%Y-%m-%d'),t.avg_change,t.up_ratio FROM (
		SELECT k.trade_date,AVG(k.change_pct) avg_change,SUM(k.change_pct>0)/COUNT(*) up_ratio
		FROM kline_daily k INNER JOIN stock_basic b ON b.symbol=k.symbol
		WHERE k.trade_date<=? AND b.status='listed' AND b.sec_type='stock'
		GROUP BY k.trade_date ORDER BY k.trade_date DESC LIMIT ?
	) t ORDER BY t.trade_date ASC`, tradeDate, reviewStanceLookbackDays)
	if err != nil {
		return stance, err
	}
	defer rows.Close()
	var avgChanges, upRatios []float64
	for rows.Next() {
		var date string
		var avgChange, upRatio float64
		if err := rows.Scan(&date, &avgChange, &upRatio); err != nil {
			return stance, err
		}
		avgChanges = append(avgChanges, avgChange)
		upRatios = append(upRatios, upRatio)
	}
	if err := rows.Err(); err != nil {
		return stance, err
	}
	return classifyReviewMarketStance(avgChanges, upRatios), nil
}

// classifyReviewMarketStance 是操作姿态的纯分类函数：输入按交易日升序的全市场
// 等权涨跌（百分数）与上涨占比（0-1），推演净值曲线后按固定阈值分类。
func classifyReviewMarketStance(avgChanges, upRatios []float64) ReviewMarketStance {
	stance := ReviewMarketStance{Stance: "hold", LookbackDays: reviewStanceLookbackDays, Reason: "历史数据不足，默认持仓等待"}
	// 至少 10 个交易日才有推演意义（动量需要 5 日、回撤需要足够的高低点样本）。
	if len(avgChanges) < 10 || len(avgChanges) != len(upRatios) {
		return stance
	}

	nav := make([]float64, len(avgChanges))
	value := 1.0
	for i, change := range avgChanges {
		value *= 1 + change/100
		nav[i] = value
	}
	last := len(nav) - 1
	peak, trough := nav[0], nav[0]
	for _, v := range nav {
		if v > peak {
			peak = v
		}
		if v < trough {
			trough = v
		}
	}
	stance.Momentum5D = (nav[last]/nav[last-5] - 1) * 100
	stance.DrawdownPct = (peak - nav[last]) / peak * 100
	stance.ReboundPct = (nav[last] - trough) / trough * 100
	stance.UpRatioToday = upRatios[last]
	var upSum float64
	for _, ratio := range upRatios[len(upRatios)-5:] {
		upSum += ratio
	}
	stance.UpRatio5D = upSum / 5

	switch {
	case stance.Momentum5D >= 3.0 && stance.DrawdownPct <= 1.0:
		stance.Stance = "take_profit"
		stance.Reason = fmt.Sprintf("等权大盘 5 日涨 %.1f%% 且贴近 20 日高点（回撤 %.1f%%），短期涨幅已兑现，宜落袋锁定利润", stance.Momentum5D, stance.DrawdownPct)
	case stance.DrawdownPct >= 4.0 && stance.Momentum5D >= -1.0 && stance.UpRatioToday >= 0.5:
		stance.Stance = "accumulate"
		stance.Reason = fmt.Sprintf("等权大盘距 20 日高点回撤 %.1f%%，5 日动量 %.1f%% 已企稳，当日上涨占比 %.0f%%，回调充分宜分批扫货", stance.DrawdownPct, stance.Momentum5D, stance.UpRatioToday*100)
	default:
		stance.Stance = "hold"
		stance.Reason = fmt.Sprintf("等权大盘 5 日动量 %.1f%%、距高点回撤 %.1f%%、当日上涨占比 %.0f%%，未触发落袋或扫货条件，持仓等待", stance.Momentum5D, stance.DrawdownPct, stance.UpRatioToday*100)
	}
	return stance
}

func (s *Store) reviewSectors(ctx context.Context, tradeDate, direction string) ([]ReviewSectorFact, error) {
	if direction != "ASC" {
		direction = "DESC"
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.sector_code,b.sector_name,d.sector_type,d.stock_count,
		d.avg_change,d.avg_change_5d,d.up_ratio,d.amount_ratio,d.heat_score
		FROM sector_daily_stats d INNER JOIN sector_basic b ON b.sector_code=d.sector_code
		WHERE d.trade_date=? AND d.stock_count>=5
		ORDER BY d.heat_score `+direction+`,d.sector_code ASC LIMIT 10`, tradeDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewSectorFact{}
	for rows.Next() {
		var item ReviewSectorFact
		if err := rows.Scan(&item.SectorCode, &item.SectorName, &item.SectorType, &item.StockCount,
			&item.AvgChange, &item.AvgChange5D, &item.UpRatio, &item.AmountRatio, &item.HeatScore); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveDailyReview(ctx context.Context, date, stage, phase, model string, payload interface{}) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO daily_review (review_date,stage,market_phase,payload,model) VALUES (?,?,?,?,?)`, date, stage, phase, string(raw), model)
	return err
}

func (s *Store) DailyReviewHistory(ctx context.Context, limit int) ([]ReviewRunSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,DATE_FORMAT(review_date,'%Y-%m-%d'),market_phase,model,
		DATE_FORMAT(created_at,'%Y-%m-%d %H:%i') FROM daily_review WHERE stage='review' ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewRunSummary{}
	for rows.Next() {
		var item ReviewRunSummary
		if err := rows.Scan(&item.ID, &item.ReviewDate, &item.MarketPhase, &item.Model, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DailyReviewByID(ctx context.Context, id int64) (json.RawMessage, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT payload FROM daily_review WHERE id=? AND stage='review'`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func (s *Store) LatestDailyReview(ctx context.Context) (json.RawMessage, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT payload FROM daily_review WHERE stage='review' ORDER BY id DESC LIMIT 1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// LatestReviewGuidanceForRecommendation 只提取结构化阶段、优化指令与风控参数，
// 避免把整份长报告注入推荐 prompt。
func (s *Store) LatestReviewGuidanceForRecommendation(ctx context.Context, beforeDate string) (LatestReviewGuidance, error) {
	var guidance LatestReviewGuidance
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT DATE_FORMAT(review_date,'%Y-%m-%d'),market_phase,payload
		FROM daily_review WHERE stage='review' AND review_date<=? ORDER BY id DESC LIMIT 1`, beforeDate).
		Scan(&guidance.ReviewDate, &guidance.MarketPhase, &raw)
	if err == sql.ErrNoRows {
		return guidance, nil
	}
	if err != nil {
		return guidance, err
	}
	var payload struct {
		Directives   []RecommendationDirective  `json:"directives"`
		RiskControls *ReviewRiskControlGuidance `json:"risk_controls"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return guidance, fmt.Errorf("解析最近复盘优化指令: %w", err)
	}
	if len(payload.Directives) > 5 {
		payload.Directives = payload.Directives[:5]
	}
	guidance.Directives = payload.Directives
	guidance.RiskControls = payload.RiskControls
	return guidance, nil
}
