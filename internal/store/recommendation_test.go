package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

func TestSelectRecommendationSectorsFiltersGenericConcepts(t *testing.T) {
	sectors := []recommendationSector{
		{Code: "BK0596", Type: "concept", Name: "融资融券", Popularity: 99},
		{Code: "BK0804", Type: "concept", Name: "深股通", Popularity: 98},
		{Code: "BK0500", Type: "concept", Name: "MSCI中国", Popularity: 97},
		{Code: "BK0727", Type: "industry", Name: "医疗器械", Popularity: 90},
		{Code: "BK1036", Type: "concept", Name: "减肥药", Popularity: 88},
	}
	selected := selectRecommendationSectors(sectors)
	if len(selected) != 2 {
		t.Fatalf("expected 2 sectors after filtering generic concepts, got %d: %+v", len(selected), selected)
	}
	if selected[0].Name != "医疗器械" || selected[1].Name != "减肥药" {
		t.Fatalf("generic concepts must be filtered, got %+v", selected)
	}

	// 过滤后仍按热度序截断到题材上限
	many := make([]recommendationSector, 0, recommendationSectorLimit+5)
	for i := 0; i < recommendationSectorLimit+5; i++ {
		many = append(many, recommendationSector{Code: fmt.Sprintf("BK%04d", i), Type: "concept", Name: fmt.Sprintf("题材%d", i), Popularity: float64(100 - i)})
	}
	if got := selectRecommendationSectors(many); len(got) != recommendationSectorLimit {
		t.Fatalf("expected cap at %d sectors, got %d", recommendationSectorLimit, len(got))
	}
}

func TestRecommendationTrendScoreRequiresCompleteRisingSixtyDays(t *testing.T) {
	klines := make([]model.Kline, recommendationKlineDays)
	for i := range klines {
		close := 10.0 + float64(i)*0.1
		klines[i] = model.Kline{Close: close}
	}
	score, ok := recommendationTrendScore(klines)
	if !ok || score <= 0 {
		t.Fatalf("expected rising 60-day trend to qualify, score=%f ok=%v", score, ok)
	}

	if _, ok := recommendationTrendScore(klines[:59]); ok {
		t.Fatal("incomplete 60-day history must not qualify")
	}
}

func TestRecommendationTrendScoreRejectsFallingTrend(t *testing.T) {
	klines := make([]model.Kline, recommendationKlineDays)
	for i := range klines {
		klines[i] = model.Kline{Close: 20.0 - float64(i)*0.1}
	}
	if _, ok := recommendationTrendScore(klines); ok {
		t.Fatal("falling trend must not qualify")
	}
}

func TestRecommendationRiskScore(t *testing.T) {
	// 平稳缓涨：低波动、低回撤、无短期过热 → 低风险
	calm := make([]model.Kline, recommendationKlineDays)
	for i := range calm {
		calm[i] = model.Kline{Close: 10.0 + float64(i)*0.01}
	}
	calmScore, ok := recommendationRiskScore(calm)
	if !ok {
		t.Fatal("calm series must produce a risk score")
	}
	if calmScore > recommendationBaseMaxRisk {
		t.Fatalf("calm series risk=%f exceeds base threshold", calmScore)
	}

	// 剧烈波动 + 深回撤 + 近 5 日暴涨 → 高风险分，但该分值只用于展示。
	risky := make([]model.Kline, recommendationKlineDays)
	price := 10.0
	for i := range risky {
		if i%2 == 0 {
			price *= 1.10
		} else {
			price *= 0.82
		}
		risky[i] = model.Kline{Close: price}
	}
	// 尾部 5 日连续暴涨制造短期过热
	for i := recommendationKlineDays - 5; i < recommendationKlineDays; i++ {
		price *= 1.10
		risky[i] = model.Kline{Close: price}
	}
	riskyScore, ok := recommendationRiskScore(risky)
	if !ok {
		t.Fatal("risky series must produce a risk score")
	}
	if riskyScore <= recommendationMaxRiskUp {
		t.Fatalf("risky series risk=%f should exceed max threshold %f", riskyScore, recommendationMaxRiskUp)
	}
	if riskyScore <= calmScore {
		t.Fatalf("risky=%f must be greater than calm=%f", riskyScore, calmScore)
	}

	// 数据不完整不给分
	if _, ok := recommendationRiskScore(calm[:59]); ok {
		t.Fatal("incomplete history must not produce a risk score")
	}
}

func TestRecommendationMaxRiskScoreByPhase(t *testing.T) {
	cases := map[string]float64{
		"up":    recommendationMaxRiskUp,
		"range": recommendationMaxRiskRange,
		"down":  recommendationMaxRiskDown,
		"":      recommendationBaseMaxRisk,
		"other": recommendationBaseMaxRisk,
	}
	for phase, want := range cases {
		if got := RecommendationMaxRiskScore(phase); got != want {
			t.Fatalf("phase=%q max risk=%f, want %f", phase, got, want)
		}
	}
	// 上升阶段放宽、下降阶段收紧的方向不能反转
	if !(recommendationMaxRiskUp > recommendationBaseMaxRisk && recommendationMaxRiskDown < recommendationBaseMaxRisk) {
		t.Fatal("phase risk thresholds direction invalid")
	}
}

func TestRecommendationLimitPctByBoard(t *testing.T) {
	cases := map[string]float64{
		"600519": 10, "000001": 10, "601988": 10,
		"300750": 20, "688981": 20,
		"920001": 30, "830799": 30, "430047": 30,
	}
	for code, want := range cases {
		if got := recommendationLimitPct(code); got != want {
			t.Fatalf("code=%s limit=%f, want %f", code, got, want)
		}
	}
}

func TestRecommendationGapRiskHigh(t *testing.T) {
	build := func(lastChangePct float64) []model.Kline {
		klines := make([]model.Kline, recommendationKlineDays)
		for i := range klines {
			klines[i] = model.Kline{Close: 10}
		}
		klines[len(klines)-1].ChangePct = lastChangePct
		return klines
	}
	// 主板昨日涨停（≥ 10%×0.93）→ 剔除
	if !recommendationGapRiskHigh(build(9.98), "600519") {
		t.Fatal("main board limit-up must be flagged as gap risk")
	}
	// 主板昨日涨 5% → 保留
	if recommendationGapRiskHigh(build(5), "600519") {
		t.Fatal("moderate gain must not be flagged")
	}
	// 创业板涨 9.98% 未及 20% 限幅 → 保留
	if recommendationGapRiskHigh(build(9.98), "300750") {
		t.Fatal("GEM 9.98%% is not limit-up, must not be flagged")
	}
	// 创业板涨 19.9% → 剔除
	if !recommendationGapRiskHigh(build(19.9), "300750") {
		t.Fatal("GEM limit-up must be flagged")
	}
	// change_pct 缺失时用最后两根收盘价近似
	missing := make([]model.Kline, recommendationKlineDays)
	for i := range missing {
		missing[i] = model.Kline{Close: 10}
	}
	missing[len(missing)-1] = model.Kline{Close: 11} // 收盘 +10%
	if !recommendationGapRiskHigh(missing, "000001") {
		t.Fatal("fallback close-to-close limit-up must be flagged")
	}
}

func TestRecommendationSortScoreOverheatPenalty(t *testing.T) {
	build := func(gain5 float64) []model.Kline {
		klines := make([]model.Kline, recommendationKlineDays)
		for i := range klines {
			klines[i] = model.Kline{Close: 10}
		}
		klines[len(klines)-6].Close = 10
		klines[len(klines)-1].Close = 10 * (1 + gain5)
		return klines
	}
	const trendScore = 100.0
	// 近 5 日涨 10% 在惩罚起点内 → 不降权
	if got := recommendationSortScore(trendScore, build(0.10)); got != trendScore {
		t.Fatalf("gain5=10%% score=%f, want %f", got, trendScore)
	}
	// 近 5 日涨 25% → 部分降权，介于 50 与 100 之间
	mid := recommendationSortScore(trendScore, build(0.25))
	if mid >= trendScore || mid <= trendScore*(1-recommendationOverheatMaxPenalty) {
		t.Fatalf("gain5=25%% score=%f, want between %f and %f", mid, trendScore*(1-recommendationOverheatMaxPenalty), trendScore)
	}
	// 近 5 日涨 40% 超过封顶 → 打五折
	if got := recommendationSortScore(trendScore, build(0.40)); got != trendScore*(1-recommendationOverheatMaxPenalty) {
		t.Fatalf("gain5=40%% score=%f, want %f", got, trendScore*(1-recommendationOverheatMaxPenalty))
	}
}

func TestRecommendationPerformance(t *testing.T) {
	entry, latest, changePct := recommendationPerformance(
		sql.NullFloat64{Float64: 10, Valid: true},
		sql.NullFloat64{Float64: 12.5, Valid: true},
	)
	if entry == nil || latest == nil || changePct == nil {
		t.Fatal("expected complete performance data")
	}
	if *entry != 10 || *latest != 12.5 || *changePct != 25 {
		t.Fatalf("unexpected performance: entry=%v latest=%v change=%v", *entry, *latest, *changePct)
	}

	entry, latest, changePct = recommendationPerformance(sql.NullFloat64{}, sql.NullFloat64{Float64: 12.5, Valid: true})
	if entry != nil || latest == nil || changePct != nil {
		t.Fatalf("expected incomplete performance data: entry=%v latest=%v change=%v", entry, latest, changePct)
	}
}

func TestApplyRecommendationPerformanceIgnoresRecommendationWithoutLifecycle(t *testing.T) {
	store := &Store{}
	item := model.StockRecommendation{Date: "2026-07-29", Symbol: "SZ301536"}

	if err := store.applyRecommendationPerformance(context.Background(), &item, map[string]PositionSettlement{}); err != nil {
		t.Fatalf("legacy recommendation without lifecycle must be ignored: %v", err)
	}
	if item.Settled || item.Exited || item.ChangePct != nil || item.EntryPrice != nil || item.LatestPrice != nil {
		t.Fatalf("recommendation without lifecycle must not become a trading result: %+v", item)
	}
}

func TestApplyRecommendationPerformanceFreezesExitedLifecycle(t *testing.T) {
	entry, exit := 100.0, 112.5
	item := model.StockRecommendation{Date: "2026-08-01", Symbol: "SH600000"}
	settlements := map[string]PositionSettlement{
		item.Symbol: {
			Status: PositionExited, EntryDate: "2026-08-03", EntryPrice: &entry,
			ExitDate: "2026-08-06", ExitReason: "AI exit", ExitPrice: &exit, HoldDays: 4,
			PositionPct: 100,
		},
	}

	if err := (&Store{}).applyRecommendationPerformance(context.Background(), &item, settlements); err != nil {
		t.Fatal(err)
	}
	// 毛收益 12.5%，扣除往返交易成本后才是可比的真实收益。
	want := PositionNetChangePct(12.5)
	if !item.Settled || !item.Exited || item.ChangePct == nil || math.Abs(*item.ChangePct-want) > 1e-9 {
		t.Fatalf("exited lifecycle must freeze net-of-cost performance (want %.4f): %+v", want, item)
	}
	if item.TrackedDays != 4 || item.ExitReason != "AI exit" || item.PositionStatus != PositionExited {
		t.Fatalf("unexpected exited lifecycle metadata: %+v", item)
	}
}

func TestApplyRecommendationPerformanceExcludesRemovedLifecycle(t *testing.T) {
	// 用户手动移除自选后：状态与放弃原因保留展示，但没有可信结算价，
	// 不得产生收益样本（Settled=false），也不得回落到参考走势口径。
	entry := 100.0
	item := model.StockRecommendation{Date: "2026-08-10", Symbol: "SH600000"}
	settlements := map[string]PositionSettlement{
		item.Symbol: {
			Status: PositionRemoved, EntryDate: "2026-08-11", EntryPrice: &entry,
			ExitDate: "2026-08-13", ExitReason: "用户手动移除自选，停止跟踪；无确认平仓价，不计入收益统计",
			HoldDays: 3, PositionPct: 100,
		},
	}

	if err := (&Store{}).applyRecommendationPerformance(context.Background(), &item, settlements); err != nil {
		t.Fatal(err)
	}
	if item.PositionStatus != PositionRemoved || item.ExitReason == "" {
		t.Fatalf("removed lifecycle must keep status and reason for display: %+v", item)
	}
	if item.Settled || item.Exited || item.ChangePct != nil || item.EntryPrice != nil || item.LatestPrice != nil {
		t.Fatalf("removed lifecycle must not produce a performance sample: %+v", item)
	}
}

// 分批减仓后，已退出交易的收益必须按仓位加权：
// +12% 时减半仓锁定 6 个点，剩余半仓在 +4% 退出贡献 2 个点，合计毛收益 8 个点。
func TestApplyRecommendationPerformanceBlendsReducedPosition(t *testing.T) {
	entry, exit := 100.0, 104.0
	item := model.StockRecommendation{Date: "2026-08-01", Symbol: "SH600000"}
	settlements := map[string]PositionSettlement{
		item.Symbol: {
			Status: PositionExited, EntryDate: "2026-08-03", EntryPrice: &entry,
			ExitDate: "2026-08-06", ExitPrice: &exit, ExitReason: "trailing stop", HoldDays: 3,
			PositionPct: 50, RealizedPct: 6,
		},
	}
	if err := (&Store{}).applyRecommendationPerformance(context.Background(), &item, settlements); err != nil {
		t.Fatal(err)
	}
	want := PositionNetChangePct(8)
	if item.ChangePct == nil || math.Abs(*item.ChangePct-want) > 1e-9 {
		t.Fatalf("blended performance want %.4f, got %+v", want, item.ChangePct)
	}
}

// 风险分只展示：无论分值高低，生产候选排序分必须完全一致。
func TestRecommendationCandidateSortScoreIgnoresRisk(t *testing.T) {
	klines := make([]model.Kline, recommendationKlineDays)
	for i := range klines {
		klines[i] = model.Kline{Close: 10 + float64(i)*0.01}
	}
	lowRisk := recommendationCandidateSortScore(100, klines, recommendationLeaderBoostTop1, 5)
	highRisk := recommendationCandidateSortScore(100, klines, recommendationLeaderBoostTop1, 99)
	if lowRisk != highRisk {
		t.Fatalf("risk score must not affect production ranking: low=%.4f high=%.4f", lowRisk, highRisk)
	}
}

func TestRecommendationLeaderRanksBySectorPopularity(t *testing.T) {
	pool := []RecommendationCandidate{
		{Symbol: "A", Industry: "算力", Popularity: 80},
		{Symbol: "B", Industry: "算力", Popularity: 100},
		{Symbol: "C", Industry: "机器人", Popularity: 70},
	}
	ranks := recommendationLeaderRanks(pool)
	if ranks["B"] != 1 || ranks["A"] != 2 || ranks["C"] != 1 {
		t.Fatalf("unexpected leader ranks: %+v", ranks)
	}
}

func TestSelectStrongSectorLeadersPrioritizesSectorThenLeader(t *testing.T) {
	candidates := []RecommendationCandidate{
		{Symbol: "A1", Industry: "强板块", SectorHeat: 90},
		{Symbol: "A2", Industry: "强板块", SectorHeat: 90},
		{Symbol: "A3", Industry: "强板块", SectorHeat: 90},
		{Symbol: "A4", Industry: "强板块", SectorHeat: 90},
		{Symbol: "B1", Industry: "次强板块", SectorHeat: 80},
	}
	scores := map[string]float64{"A1": 100, "A2": 90, "A3": 80, "A4": 70, "B1": 200}
	selected := selectStrongSectorLeaders(candidates, scores, 4)
	if len(selected) != 4 {
		t.Fatalf("expected 4 candidates, got %+v", selected)
	}
	// 板块强度先于个股分：即使 B1 个股分更高，仍排在强板块三只龙头之后；
	// 同板块最多三只，A4 不得挤占次强板块龙头位置。
	want := []string{"A1", "A2", "A3", "B1"}
	for i, symbol := range want {
		if selected[i].Symbol != symbol {
			t.Fatalf("selected[%d]=%s want %s; all=%+v", i, selected[i].Symbol, symbol, selected)
		}
	}
}

// 泛概念熔断：回退候选池只要命中黑名单就必须被识别出来，供调用方放弃当日推荐。
func TestGenericConceptNamesDetectsFallbackPollution(t *testing.T) {
	polluted := []RecommendationCandidate{
		{Symbol: "SH600663", Industry: "融资融券"},
		{Symbol: "SZ300119", Industry: "深股通"},
		{Symbol: "SH603444", Industry: "百元股"},
		{Symbol: "SZ002556", Industry: "磷化工"},
	}
	got := GenericConceptNames(polluted)
	if len(got) != 3 {
		t.Fatalf("expected 3 generic concepts flagged, got %d: %v", len(got), got)
	}
	clean := []RecommendationCandidate{
		{Symbol: "SZ002556", Industry: "磷化工"},
		{Symbol: "SH603228", Industry: "CPO概念"},
		{Symbol: "SH600354", Industry: "转基因"},
	}
	if got := GenericConceptNames(clean); len(got) != 0 {
		t.Fatalf("real sector candidates must not be flagged, got %v", got)
	}
}
