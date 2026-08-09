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
