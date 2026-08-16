package store

import (
	"context"
	"math"
	"sort"
	"time"
)

// StrategyScorecard 是全系统统一考核结果。所有分数均为 0-100，收益指标保留原始百分比。
// score 不是收益承诺：它把最终收益、风险、样本置信度与四个环节的独立贡献合并，
// 防止系统通过放大仓位、放宽止损或依赖单只极端盈利来“刷高分”。
type StrategyScorecard struct {
	GeneratedAt time.Time              `json:"generated_at"`
	WindowDays  int                    `json:"window_days"`
	SinceDate   string                 `json:"since_date"`
	Phase       string                 `json:"phase"`
	Overall     ScorecardOverall       `json:"overall"`
	Stages      ScorecardStages        `json:"stages"`
	Equity      []ScorecardEquityPoint `json:"equity"`
	ExitKinds   []ScorecardExitKind    `json:"exit_kinds"`
	Quality     ScorecardDataQuality   `json:"data_quality"`
	Methodology ScorecardMethodology   `json:"methodology"`
}

type ScorecardOverall struct {
	Score              float64  `json:"score"`
	Confidence         float64  `json:"confidence"`
	Samples            int      `json:"samples"`
	Wins               int      `json:"wins"`
	Losses             int      `json:"losses"`
	WinRate            *float64 `json:"win_rate,omitempty"`
	AvgNetPct          *float64 `json:"avg_net_pct,omitempty"`
	MedianNetPct       *float64 `json:"median_net_pct,omitempty"`
	ProfitFactor       *float64 `json:"profit_factor,omitempty"`
	TotalReturnPct     *float64 `json:"total_return_pct,omitempty"`
	MaxDrawdownPct     *float64 `json:"max_drawdown_pct,omitempty"`
	TradeSharpe        *float64 `json:"trade_sharpe,omitempty"`
	Calmar             *float64 `json:"calmar,omitempty"`
	AvgHoldDays        *float64 `json:"avg_hold_days,omitempty"`
	MechanicalAvgPct   *float64 `json:"mechanical_avg_pct,omitempty"`
	ActualVsMechanical *float64 `json:"actual_vs_mechanical_pct,omitempty"`
}

type ScorecardStages struct {
	Selection   ScorecardStage `json:"selection"`
	Opportunity ScorecardStage `json:"opportunity"`
	Entry       ScorecardStage `json:"entry"`
	Exit        ScorecardStage `json:"exit"`
}

type ScorecardStage struct {
	Score      float64            `json:"score"`
	Samples    int                `json:"samples"`
	Confidence float64            `json:"confidence"`
	Metrics    map[string]float64 `json:"metrics"`
	Summary    string             `json:"summary"`
}

type ScorecardEquityPoint struct {
	Date        string  `json:"date"`
	Equity      float64 `json:"equity"`
	DrawdownPct float64 `json:"drawdown_pct"`
	Trades      int     `json:"trades"`
}

type ScorecardExitKind struct {
	Kind            string   `json:"kind"`
	Samples         int      `json:"samples"`
	WinRate         *float64 `json:"win_rate,omitempty"`
	AvgNetPct       *float64 `json:"avg_net_pct,omitempty"`
	AvgCaptureRate  *float64 `json:"avg_capture_rate,omitempty"`
	AvgPostExit5Pct *float64 `json:"avg_post_exit_5d_pct,omitempty"`
}

type ScorecardDataQuality struct {
	ExcludedSamples int `json:"excluded_samples"`
	T0Violations    int `json:"t0_violations"`
}

type ScorecardMethodology struct {
	MechanicalBaseline string `json:"mechanical_baseline"`
	EquityCurve        string `json:"equity_curve"`
	RiskNote           string `json:"risk_note"`
	MinimumSamples     int    `json:"minimum_samples"`
}

type scoredTrade struct {
	position   ScorecardPosition
	netPct     float64
	mfePct     *float64
	maePct     *float64
	capturePct *float64
	entryEff   *float64
	postExit5  *float64
}

// StrategyScorecardForWindow 生成统一考核。phase 当前支持 all；保留参数是为了后续
// 对 up/range/down 分层使用同一接口，非法值回退 all。
func (s *Store) StrategyScorecardForWindow(ctx context.Context, days int, phase string) (StrategyScorecard, error) {
	if days <= 0 || days > 365 {
		days = 60
	}
	if phase != "up" && phase != "range" && phase != "down" {
		phase = "all"
	}
	report := StrategyScorecard{
		GeneratedAt: time.Now(), WindowDays: days, Phase: phase,
		Equity: []ScorecardEquityPoint{}, ExitKinds: []ScorecardExitKind{},
		Methodology: ScorecardMethodology{
			MechanicalBaseline: "推荐后首个交易日开盘等权买入，固定持有5个交易日收盘卖出，扣除0.25%往返成本",
			EquityCurve:        "按已结算交易的退出日等权复合形成结算净值；用于统一比较，不代表账户逐日盯市净值",
			RiskNote:           "模拟参考价未完整建模涨跌停、停牌、盘口冲击与隔夜跳空；数据质量异常样本已排除",
			MinimumSamples:     30,
		},
	}
	since, err := s.RecommendationSinceDate(ctx, days)
	if err != nil {
		return report, err
	}
	if since == "" {
		return report, nil
	}
	report.SinceDate = since

	positions, err := s.ScorecardPositions(ctx, since)
	if err != nil {
		return report, err
	}
	if phase != "all" {
		positions, err = s.filterScorecardPositionsByPhase(ctx, positions, phase)
		if err != nil {
			return report, err
		}
	}
	excluded, err := s.ScorecardExcludedCount(ctx, since)
	if err != nil {
		return report, err
	}
	report.Quality.ExcludedSamples, report.Quality.T0Violations = excluded, excluded

	trades := make([]scoredTrade, 0, len(positions))
	enteredMechanical := []float64{}
	for _, p := range positions {
		if mechanical, err := s.MechanicalWindow(ctx, p.Symbol, p.AnalysisDate, MechanicalHoldDays); err != nil {
			return report, err
		} else if mechanical != nil {
			enteredMechanical = append(enteredMechanical, mechanical.NetPct)
		}
		net := p.NetChangePct()
		if net == nil || p.Status != PositionExited {
			continue
		}
		trade := scoredTrade{position: p, netPct: *net}
		if p.EntryPrice > 0 && p.HighestPrice != nil {
			v := (*p.HighestPrice/p.EntryPrice - 1) * 100
			trade.mfePct = &v
			if v > 0 {
				capture := *net / v * 100
				// 超过100%通常来自分批减仓口径或采样极值不足；保留方向但封顶，防止污染评分。
				capture = clamp(capture, -200, 150)
				trade.capturePct = &capture
			}
		}
		if p.EntryPrice > 0 && p.LowestPrice != nil {
			v := (*p.LowestPrice/p.EntryPrice - 1) * 100
			trade.maePct = &v
		}
		bars, err := s.DailyBarsBetween(ctx, p.Symbol, p.EntryDate, p.EntryDate)
		if err != nil {
			return report, err
		}
		if len(bars) == 1 && bars[0].High > bars[0].Low {
			v := (bars[0].High - p.EntryPrice) / (bars[0].High - bars[0].Low) * 100
			v = clamp(v, 0, 100) // 100=接近日内低点建仓，0=接近日内高点追入
			trade.entryEff = &v
		}
		if p.ExitDate != "" && p.ExitPrice != nil && *p.ExitPrice > 0 {
			future, err := s.DailyBarsAfter(ctx, p.Symbol, p.ExitDate, MechanicalHoldDays)
			if err != nil {
				return report, err
			}
			if len(future) == MechanicalHoldDays && future[len(future)-1].Close > 0 {
				v := (future[len(future)-1].Close/(*p.ExitPrice) - 1) * 100
				trade.postExit5 = &v
			}
		}
		trades = append(trades, trade)
	}

	mechanical, err := s.selectionMechanicalOutcomes(ctx, since, phase)
	if err != nil {
		return report, err
	}
	expired, err := s.ScorecardExpiredPicks(ctx, since)
	if err != nil {
		return report, err
	}
	if phase != "all" {
		expired, err = s.filterScorecardPositionsByPhase(ctx, expired, phase)
		if err != nil {
			return report, err
		}
	}
	expiredMechanical := []float64{}
	for _, p := range expired {
		outcome, err := s.MechanicalWindow(ctx, p.Symbol, p.AnalysisDate, MechanicalHoldDays)
		if err != nil {
			return report, err
		}
		if outcome != nil {
			expiredMechanical = append(expiredMechanical, outcome.NetPct)
		}
	}
	advice, err := s.EntryAdviceStatsSince(ctx, since)
	if err != nil {
		return report, err
	}

	report.Overall = calculateOverallScore(trades, mechanical)
	report.Equity = buildSettlementEquity(trades)
	report.ExitKinds = buildExitKindStats(trades)
	report.Stages.Selection = calculateSelectionStage(mechanical)
	report.Stages.Opportunity = calculateOpportunityStage(len(positions), len(expired), enteredMechanical, expiredMechanical, advice)
	report.Stages.Entry = calculateEntryStage(trades)
	report.Stages.Exit = calculateExitStage(trades)

	stageMean := (report.Stages.Selection.Score*0.30 + report.Stages.Opportunity.Score*0.20 + report.Stages.Entry.Score*0.20 + report.Stages.Exit.Score*0.30)
	performanceScore := scorePerformance(report.Overall)
	confidence := sampleConfidence(report.Overall.Samples)
	// 最终目标仍是盈利，但不允许靠高风险或极小样本刷分。60%看最终风险调整收益，
	// 40%看四个核心环节；样本不足时向中性分50收缩，而不是显示虚假的高确定性。
	raw := performanceScore*0.60 + stageMean*0.40
	report.Overall.Score = round2(50 + (raw-50)*confidence)
	report.Overall.Confidence = round2(confidence * 100)
	return report, nil
}

func (s *Store) selectionMechanicalOutcomes(ctx context.Context, since, phase string) ([]MechanicalOutcome, error) {
	picks, err := s.RecommendationPicksSince(ctx, since)
	if err != nil {
		return nil, err
	}
	out := []MechanicalOutcome{}
	for _, pick := range picks {
		if phase != "all" {
			pickPhase, err := s.MarketPhaseBefore(ctx, pick[0])
			if err != nil {
				return nil, err
			}
			if pickPhase != phase {
				continue
			}
		}
		result, err := s.MechanicalWindow(ctx, pick[1], pick[0], MechanicalHoldDays)
		if err != nil {
			return nil, err
		}
		if result != nil {
			out = append(out, *result)
		}
	}
	return out, nil
}

func (s *Store) filterScorecardPositionsByPhase(ctx context.Context, positions []ScorecardPosition, phase string) ([]ScorecardPosition, error) {
	out := make([]ScorecardPosition, 0, len(positions))
	phaseCache := map[string]string{}
	for _, position := range positions {
		pickPhase, ok := phaseCache[position.AnalysisDate]
		if !ok {
			var err error
			pickPhase, err = s.MarketPhaseBefore(ctx, position.AnalysisDate)
			if err != nil {
				return nil, err
			}
			phaseCache[position.AnalysisDate] = pickPhase
		}
		if pickPhase == phase {
			out = append(out, position)
		}
	}
	return out, nil
}

func calculateOverallScore(trades []scoredTrade, mechanical []MechanicalOutcome) ScorecardOverall {
	var out ScorecardOverall
	if len(mechanical) > 0 {
		values := make([]float64, 0, len(mechanical))
		for _, item := range mechanical {
			values = append(values, item.NetPct)
		}
		v := mean(values)
		out.MechanicalAvgPct = ptr(round2(v))
	}
	if len(trades) == 0 {
		return out
	}
	returns := make([]float64, 0, len(trades))
	var wins, losses, holdDays int
	var grossWin, grossLoss float64
	for _, trade := range trades {
		returns = append(returns, trade.netPct)
		holdDays += trade.position.HoldDays
		if trade.netPct > 0 {
			wins++
			grossWin += trade.netPct
		} else if trade.netPct < 0 {
			losses++
			grossLoss += -trade.netPct
		}
	}
	out.Samples, out.Wins, out.Losses = len(trades), wins, losses
	out.WinRate = ptr(round2(float64(wins) / float64(len(trades)) * 100))
	out.AvgNetPct = ptr(round2(mean(returns)))
	out.MedianNetPct = ptr(round2(median(returns)))
	out.AvgHoldDays = ptr(round2(float64(holdDays) / float64(len(trades))))
	if grossLoss > 0 {
		out.ProfitFactor = ptr(round2(grossWin / grossLoss))
	} else if grossWin > 0 {
		out.ProfitFactor = ptr(10) // 有盈利、无亏损时有限封顶，避免 JSON Inf
	}
	if out.MechanicalAvgPct != nil {
		v := *out.AvgNetPct - *out.MechanicalAvgPct
		out.ActualVsMechanical = ptr(round2(v))
	}
	equity, peak, maxDD := 1.0, 1.0, 0.0
	for _, v := range returns {
		equity *= 1 + v/100
		if equity > peak {
			peak = equity
		}
		dd := (peak - equity) / peak * 100
		if dd > maxDD {
			maxDD = dd
		}
	}
	total := (equity - 1) * 100
	out.TotalReturnPct, out.MaxDrawdownPct = ptr(round2(total)), ptr(round2(maxDD))
	if sd := stddev(returns); sd > 0 {
		out.TradeSharpe = ptr(round2(mean(returns) / sd * math.Sqrt(float64(len(returns)))))
	}
	if maxDD > 0 {
		out.Calmar = ptr(round2(total / maxDD))
	}
	return out
}

func calculateSelectionStage(items []MechanicalOutcome) ScorecardStage {
	stage := ScorecardStage{Metrics: map[string]float64{}, Summary: "机械5日口径衡量纯选股能力，剥离建仓与离场判断"}
	if len(items) == 0 {
		stage.Score = 50
		return stage
	}
	values, excess := []float64{}, []float64{}
	wins := 0
	for _, item := range items {
		values = append(values, item.NetPct)
		if item.NetPct > 0 {
			wins++
		}
		if item.ExcessPct != nil {
			excess = append(excess, *item.ExcessPct)
		}
	}
	stage.Samples, stage.Confidence = len(values), round2(sampleConfidence(len(values))*100)
	stage.Metrics["avg_5d_net_pct"] = round2(mean(values))
	stage.Metrics["positive_rate"] = round2(float64(wins) / float64(len(values)) * 100)
	if len(excess) > 0 {
		stage.Metrics["avg_excess_pct"] = round2(mean(excess))
	}
	base := 50 + mean(values)*8
	if len(excess) > 0 {
		base += mean(excess) * 5
	}
	stage.Score = confidenceAdjusted(base, len(values))
	return stage
}

func calculateOpportunityStage(entered, expired int, enteredMechanical, expiredMechanical []float64, advice ScorecardEntryAdviceStats) ScorecardStage {
	stage := ScorecardStage{Metrics: map[string]float64{}, Summary: "比较已建仓与宽限期放弃标的的机械收益，衡量机会过滤是否真正增值"}
	total := entered + expired
	stage.Samples = total
	stage.Confidence = round2(sampleConfidence(total) * 100)
	if total == 0 {
		stage.Score = 50
		return stage
	}
	conversion := float64(entered) / float64(total) * 100
	stage.Metrics["entry_conversion_rate"] = round2(conversion)
	stage.Metrics["wait_decisions"] = float64(advice.WaitCount)
	var advantage float64
	if len(enteredMechanical) > 0 {
		stage.Metrics["entered_mechanical_avg_pct"] = round2(mean(enteredMechanical))
	}
	if len(expiredMechanical) > 0 {
		stage.Metrics["expired_mechanical_avg_pct"] = round2(mean(expiredMechanical))
		advantage = mean(enteredMechanical) - mean(expiredMechanical)
		stage.Metrics["filter_advantage_pct"] = round2(advantage)
	}
	// 转化率不是越高越好，核心看已建仓组是否显著优于放弃组；没有成熟对照时保持中性。
	base := 50.0
	if len(enteredMechanical) > 0 && len(expiredMechanical) > 0 {
		base += advantage * 10
	}
	stage.Score = confidenceAdjusted(base, total)
	return stage
}

func calculateEntryStage(trades []scoredTrade) ScorecardStage {
	stage := ScorecardStage{Metrics: map[string]float64{}, Summary: "用日内建仓位置与持仓最大不利偏移衡量建仓精准度"}
	eff, mae := []float64{}, []float64{}
	stopLosses, stopRebounds := 0, 0
	for _, trade := range trades {
		if trade.entryEff != nil {
			eff = append(eff, *trade.entryEff)
		}
		if trade.maePct != nil {
			mae = append(mae, *trade.maePct)
		}
		if trade.position.ExitKind == ExitKindStopLoss {
			stopLosses++
			if trade.postExit5 != nil && *trade.postExit5 >= 5 {
				stopRebounds++
			}
		}
	}
	stage.Samples = len(trades)
	stage.Confidence = round2(sampleConfidence(len(trades)) * 100)
	if len(trades) == 0 {
		stage.Score = 50
		return stage
	}
	base := 50.0
	if len(eff) > 0 {
		v := median(eff)
		stage.Metrics["median_entry_efficiency"] = round2(v)
		base += (v - 50) * 0.5
	}
	if len(mae) > 0 {
		v := median(mae)
		stage.Metrics["median_mae_pct"] = round2(v)
		base += clamp(v, -10, 0) * 2 // MAE 越负，扣分越多
	}
	if stopLosses > 0 {
		rate := float64(stopRebounds) / float64(stopLosses) * 100
		stage.Metrics["stop_false_signal_rate"] = round2(rate)
		base -= rate * 0.15
	}
	stage.Score = confidenceAdjusted(base, len(trades))
	return stage
}

func calculateExitStage(trades []scoredTrade) ScorecardStage {
	stage := ScorecardStage{Metrics: map[string]float64{}, Summary: "用MFE捕获率与离场后5日续涨衡量离场是否过早或过晚"}
	captures, post := []float64{}, []float64{}
	for _, trade := range trades {
		if trade.capturePct != nil && trade.netPct > 0 {
			captures = append(captures, *trade.capturePct)
		}
		if trade.postExit5 != nil {
			post = append(post, *trade.postExit5)
		}
	}
	stage.Samples = len(trades)
	stage.Confidence = round2(sampleConfidence(len(trades)) * 100)
	if len(trades) == 0 {
		stage.Score = 50
		return stage
	}
	base := 50.0
	if len(captures) > 0 {
		v := median(captures)
		stage.Metrics["median_mfe_capture_rate"] = round2(v)
		base += (clamp(v, 0, 100) - 50) * 0.5
	}
	if len(post) > 0 {
		v := mean(post)
		stage.Metrics["avg_post_exit_5d_pct"] = round2(v)
		base -= clamp(v, -5, 10) * 2 // 离场后继续大涨说明退出偏早；继续跌则是有效退出
	}
	stage.Score = confidenceAdjusted(base, len(trades))
	return stage
}

func buildSettlementEquity(trades []scoredTrade) []ScorecardEquityPoint {
	byDate := map[string][]float64{}
	for _, trade := range trades {
		byDate[trade.position.ExitDate] = append(byDate[trade.position.ExitDate], trade.netPct)
	}
	dates := make([]string, 0, len(byDate))
	for date := range byDate {
		if date != "" {
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	equity, peak := 1.0, 1.0
	out := make([]ScorecardEquityPoint, 0, len(dates))
	for _, date := range dates {
		returns := byDate[date]
		equity *= 1 + mean(returns)/100
		if equity > peak {
			peak = equity
		}
		out = append(out, ScorecardEquityPoint{Date: date, Equity: round4(equity), DrawdownPct: round2((peak - equity) / peak * 100), Trades: len(returns)})
	}
	return out
}

func buildExitKindStats(trades []scoredTrade) []ScorecardExitKind {
	type bucket struct {
		net, capture, post []float64
		wins               int
	}
	buckets := map[string]*bucket{}
	for _, trade := range trades {
		kind := trade.position.ExitKind
		if kind == "" {
			kind = ExitKindAI
		}
		b := buckets[kind]
		if b == nil {
			b = &bucket{}
			buckets[kind] = b
		}
		b.net = append(b.net, trade.netPct)
		if trade.netPct > 0 {
			b.wins++
		}
		if trade.capturePct != nil {
			b.capture = append(b.capture, *trade.capturePct)
		}
		if trade.postExit5 != nil {
			b.post = append(b.post, *trade.postExit5)
		}
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]ScorecardExitKind, 0, len(keys))
	for _, key := range keys {
		b := buckets[key]
		item := ScorecardExitKind{Kind: key, Samples: len(b.net)}
		item.WinRate = ptr(round2(float64(b.wins) / float64(len(b.net)) * 100))
		item.AvgNetPct = ptr(round2(mean(b.net)))
		if len(b.capture) > 0 {
			item.AvgCaptureRate = ptr(round2(mean(b.capture)))
		}
		if len(b.post) > 0 {
			item.AvgPostExit5Pct = ptr(round2(mean(b.post)))
		}
		out = append(out, item)
	}
	return out
}

func scorePerformance(overall ScorecardOverall) float64 {
	if overall.Samples == 0 || overall.AvgNetPct == nil {
		return 50
	}
	score := 50 + *overall.AvgNetPct*6
	if overall.ProfitFactor != nil {
		score += clamp(*overall.ProfitFactor-1, -1, 2) * 10
	}
	if overall.MaxDrawdownPct != nil {
		score -= clamp(*overall.MaxDrawdownPct, 0, 30) * 1.2
	}
	if overall.Calmar != nil {
		score += clamp(*overall.Calmar, -3, 5) * 4
	}
	return clamp(score, 0, 100)
}

func confidenceAdjusted(score float64, samples int) float64 {
	return round2(50 + (clamp(score, 0, 100)-50)*sampleConfidence(samples))
}

func sampleConfidence(samples int) float64 {
	if samples <= 0 {
		return 0
	}
	return math.Min(1, math.Sqrt(float64(samples)/30.0))
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64(nil), values...)
	sort.Float64s(copyValues)
	middle := len(copyValues) / 2
	if len(copyValues)%2 == 0 {
		return (copyValues[middle-1] + copyValues[middle]) / 2
	}
	return copyValues[middle]
}

func stddev(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	avg := mean(values)
	var sum float64
	for _, value := range values {
		d := value - avg
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)-1))
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func ptr(value float64) *float64   { return &value }
func round2(value float64) float64 { return math.Round(value*100) / 100 }
func round4(value float64) float64 { return math.Round(value*10000) / 10000 }
