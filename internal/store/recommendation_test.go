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
