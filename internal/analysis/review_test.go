package analysis

import (
	"testing"
	"time"

	"github.com/hoax/go-stock/internal/store"
)

func TestNextReviewRun(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	friday := time.Date(2026, 8, 7, 18, 0, 0, 0, loc)
	got := nextReviewRun(friday)
	want := time.Date(2026, 8, 10, 17, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("next review = %s, want %s", got, want)
	}

	before := time.Date(2026, 8, 10, 16, 59, 0, 0, loc)
	want = time.Date(2026, 8, 10, 17, 0, 0, 0, loc)
	if got = nextReviewRun(before); !got.Equal(want) {
		t.Fatalf("same-day review = %s, want %s", got, want)
	}
}

func reviewTestFacts() store.DailyReviewFacts {
	return store.DailyReviewFacts{
		StrongSectors: []store.ReviewSectorFact{{SectorCode: "BK1", SectorName: "测试板块A"}},
		WeakSectors:   []store.ReviewSectorFact{{SectorCode: "BK2", SectorName: "测试板块B"}},
		HotspotChecks: []store.ReviewHotspotFact{{SectorCode: "BK1", SectorName: "测试板块A", Status: "latent", Confidence: 70, AvgChange: 1.2}},
		LatestRecommendations: []store.ReviewRecommendationFact{
			{Date: "2026-08-10", Symbol: "SH600000", Name: "浦发银行"},
			{Date: "2026-08-07", Symbol: "SH600000", Name: "浦发银行"},
		},
		PreviousReview: store.LatestReviewGuidance{
			ReviewDate:  "2026-08-10",
			MarketPhase: "range",
			Directives:  []store.RecommendationDirective{{Action: "优先低波动候选", Rationale: "宽度中性"}},
		},
	}
}

func reviewTestReport() DailyReviewReport {
	return DailyReviewReport{
		MarketPhase: "range", Confidence: 70, MarketSummary: "震荡", IndexReview: "分化", BreadthReview: "中性",
		SectorAssessments: []ReviewSectorAssessment{
			{SectorCode: "BK1", SectorName: "测试板块A", Strength: "strong"},
			{SectorCode: "BK2", SectorName: "测试板块B", Strength: "weak"},
		},
		HotspotReviews: []ReviewHotspotAssessment{{SectorCode: "BK1", Verdict: "mixed", Assessment: "盘前热点当日兑现一般"}},
		RecommendationReviews: []ReviewPickAssessment{
			{RecommendationDate: "2026-08-10", Symbol: "SH600000", Name: "浦发银行", Verdict: "watching"},
			{RecommendationDate: "2026-08-07", Symbol: "SH600000", Name: "浦发银行", Verdict: "hit"},
		},
		PrevDirectiveReviews: []ReviewDirectiveAssessment{{Action: "优先低波动候选", Verdict: "effective", Comment: "低风险组合当日回撤更小"}},
		Directives:           []store.RecommendationDirective{{Action: "控制风险", Rationale: "市场宽度中性"}},
		RiskControls:         ReviewRiskControls{PositionMode: "balanced", MaxPositionPct: 60, MaxSingleStockPct: 20, StopLossPct: 7},
	}
}

func TestValidateDailyReviewAcceptsDuplicateSymbolAcrossDates(t *testing.T) {
	report := reviewTestReport()
	if err := validateDailyReview(&report, reviewTestFacts()); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
}

func TestValidateDailyReviewRejectsUnknownPick(t *testing.T) {
	report := reviewTestReport()
	report.RecommendationReviews[0].Symbol = "SZ000001"
	report.RecommendationReviews[0].Name = "平安银行"
	if err := validateDailyReview(&report, reviewTestFacts()); err == nil {
		t.Fatal("expected unknown recommendation to be rejected")
	}
}

func TestValidateDailyReviewRequiresHotspotReviews(t *testing.T) {
	report := reviewTestReport()
	report.HotspotReviews = nil
	if err := validateDailyReview(&report, reviewTestFacts()); err == nil {
		t.Fatal("expected missing hotspot review to be rejected")
	}

	report = reviewTestReport()
	report.HotspotReviews[0].SectorCode = "UNKNOWN"
	if err := validateDailyReview(&report, reviewTestFacts()); err == nil {
		t.Fatal("expected unknown hotspot to be rejected")
	}
}

func TestValidateDailyReviewRequiresDirectiveVerification(t *testing.T) {
	report := reviewTestReport()
	report.PrevDirectiveReviews = nil
	if err := validateDailyReview(&report, reviewTestFacts()); err == nil {
		t.Fatal("expected missing directive verification to be rejected")
	}

	report = reviewTestReport()
	report.PrevDirectiveReviews[0].Action = "被改写的指令"
	if err := validateDailyReview(&report, reviewTestFacts()); err == nil {
		t.Fatal("expected rewritten directive action to be rejected")
	}
}

func TestValidateDailyReviewRejectsDuplicateNewDirectives(t *testing.T) {
	report := reviewTestReport()
	report.Directives = append(report.Directives, report.Directives[0])
	if err := validateDailyReview(&report, reviewTestFacts()); err == nil {
		t.Fatal("expected duplicate directives to be rejected")
	}
}
