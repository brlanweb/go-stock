package store

import (
	"database/sql"
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

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

	// 剧烈波动 + 深回撤 + 近 5 日暴涨 → 高风险，任何阶段上限下都被剔除
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
