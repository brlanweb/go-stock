package store

import (
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

func TestAddRecommendationReferenceSampleSeparatesReferenceStats(t *testing.T) {
	gain := 8.0
	loss := -4.0
	tracking := 2.0
	stats := RecommendationStats{}
	addRecommendationReferenceSample(&stats, model.StockRecommendation{ReferenceOnly: true, ChangePct: &gain, ExitReason: RecommendationExitReasonMA})
	addRecommendationReferenceSample(&stats, model.StockRecommendation{ReferenceOnly: true, ChangePct: &loss, ExitReason: RecommendationExitReasonCap})
	addRecommendationReferenceSample(&stats, model.StockRecommendation{ReferenceOnly: true, ChangePct: &tracking})
	addRecommendationReferenceSample(&stats, model.StockRecommendation{ChangePct: &gain, ExitReason: RecommendationExitReasonMA})
	finalizeRecommendationReferenceStats(&stats)

	if stats.ReferencePicks != 3 || stats.ReferenceFrozenPicks != 2 || stats.ReferenceTrackingPicks != 1 {
		t.Fatalf("unexpected reference counts: %+v", stats)
	}
	if stats.ReferenceWins != 1 || stats.ReferenceLosses != 1 {
		t.Fatalf("unexpected reference win/loss counts: %+v", stats)
	}
	if stats.ReferenceWinRate == nil || *stats.ReferenceWinRate != 50 {
		t.Fatalf("unexpected reference win rate: %+v", stats.ReferenceWinRate)
	}
	if stats.ReferenceSumChangePct == nil || *stats.ReferenceSumChangePct != 4 {
		t.Fatalf("only frozen reference samples enter realized sum: %+v", stats.ReferenceSumChangePct)
	}
	if stats.ReferenceAvgChangePct == nil || *stats.ReferenceAvgChangePct != 2 {
		t.Fatalf("unexpected reference average: %+v", stats.ReferenceAvgChangePct)
	}
	if stats.FrozenPicks != 0 || stats.Wins != 0 || stats.Losses != 0 || stats.SumChangePct != nil {
		t.Fatalf("reference samples must not alter lifecycle stats: %+v", stats)
	}
}
