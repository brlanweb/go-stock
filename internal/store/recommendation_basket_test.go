package store

import (
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

func TestSummarizeRecommendationBasketUsesAllAvailablePicks(t *testing.T) {
	gain := 6.0
	loss := -3.0
	flat := 0.0
	items := []model.StockRecommendation{
		{ChangePct: &gain, TrackedDays: 8, ExitReason: RecommendationExitReasonMA},
		{ChangePct: &loss, TrackedDays: 4},
		{ChangePct: &flat, TrackedDays: 6, ExitReason: RecommendationExitReasonCap},
	}

	got := summarizeRecommendationBasket("2026-08-01", items)
	if got.Date != "2026-08-01" || got.Stocks != 3 {
		t.Fatalf("expected all three recommendations, got %+v", got)
	}
	if got.FrozenStocks != 2 || got.TrackingStocks != 1 || got.Finished {
		t.Fatalf("unexpected basket state: %+v", got)
	}
	if got.TrackedDays != 8 {
		t.Fatalf("tracked days=%d, want 8", got.TrackedDays)
	}
	if got.SumChangePct == nil || *got.SumChangePct != 3 {
		t.Fatalf("sum=%v, want 3", got.SumChangePct)
	}
	if got.AvgChangePct == nil || *got.AvgChangePct != 1 {
		t.Fatalf("average=%v, want 1", got.AvgChangePct)
	}
}

func TestSummarizeRecommendationBasketIgnoresMissingPrices(t *testing.T) {
	gain := 4.0
	got := summarizeRecommendationBasket("2026-08-01", []model.StockRecommendation{
		{ChangePct: &gain, ExitReason: RecommendationExitReasonMA},
		{},
	})
	if got.Stocks != 1 || got.FrozenStocks != 1 || !got.Finished {
		t.Fatalf("unexpected basket state with missing price: %+v", got)
	}
}
