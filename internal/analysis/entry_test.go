package analysis

import (
	"testing"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

func TestSelectBestEntryPickPrefersProbabilityThenLowerRisk(t *testing.T) {
	risk := func(v float64) *float64 { return &v }
	items := []model.StockRecommendation{
		{Rank: 1, Symbol: "SH600001", Probability: 70, RiskScore: risk(50)},
		{Rank: 2, Symbol: "SH600002", Probability: 80, RiskScore: risk(60)},
		{Rank: 3, Symbol: "SH600003", Probability: 80, RiskScore: risk(40)},
	}
	// 概率最高优先；SH600002 与 SH600003 概率持平（80），取风险分更低的 SH600003。
	best := selectBestEntryPick(items)
	if best == nil || best.Symbol != "SH600003" {
		t.Fatalf("expected SH600003 (prob 80, risk 40), got %+v", best)
	}
}

func TestSelectBestEntryPickEmpty(t *testing.T) {
	if selectBestEntryPick(nil) != nil {
		t.Fatal("empty input must return nil")
	}
}

func TestIndexQuotesToMarketContextUsesProviderTradeDate(t *testing.T) {
	price, change, amount := 3200.0, -1.2, 123_000_000.0
	volume := int64(456)
	quotes := []*model.Quote{{
		Symbol: "SH000001", Price: &price, ChangePct: &change, Amount: &amount, Volume: &volume,
		ProviderTimestamp: "2026-08-14T14:52:00+08:00",
	}}
	indices, marketDate := indexQuotesToMarketContext(quotes)
	if marketDate != "2026-08-14" {
		t.Fatalf("expected provider market date, got %q", marketDate)
	}
	if len(indices) != 1 || indices[0].Name != "上证指数" || indices[0].Price == nil || *indices[0].Price != price {
		t.Fatalf("unexpected index conversion: %+v", indices)
	}
}

func TestNextEntryRunTradingSlots(t *testing.T) {
	loc := shanghai()
	// 周五 09:45 → 当天 10:00
	now := time.Date(2026, 8, 14, 9, 45, 0, 0, loc)
	next := nextEntryRun(now)
	if next.Hour() != 10 || next.Minute() != 0 || next.Day() != 14 {
		t.Fatalf("expected 2026-08-14 10:00, got %v", next)
	}
	// 周五 15:00（已过全部时段）→ 下周一 10:00
	now = time.Date(2026, 8, 14, 15, 0, 0, 0, loc)
	next = nextEntryRun(now)
	if next.Weekday() != time.Monday || next.Hour() != 10 || next.Minute() != 0 {
		t.Fatalf("expected Monday 10:00, got %v", next)
	}
	// 周六 → 下周一 10:30
	now = time.Date(2026, 8, 15, 9, 0, 0, 0, loc)
	next = nextEntryRun(now)
	if next.Weekday() != time.Monday || next.Day() != 17 {
		t.Fatalf("expected Monday 08-17, got %v", next)
	}
	// 午休 12:00 → 13:30
	now = time.Date(2026, 8, 14, 12, 0, 0, 0, loc)
	next = nextEntryRun(now)
	if next.Hour() != 13 || next.Minute() != 30 {
		t.Fatalf("expected 13:30 after lunch, got %v", next)
	}
	// 14:31 → 14:52 尾盘风险检查
	now = time.Date(2026, 8, 14, 14, 31, 0, 0, loc)
	next = nextEntryRun(now)
	if next.Hour() != 14 || next.Minute() != 52 {
		t.Fatalf("expected 14:52 tail check, got %v", next)
	}
}
