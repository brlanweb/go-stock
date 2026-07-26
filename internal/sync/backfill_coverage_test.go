package sync

import (
	"testing"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/store"
)

func TestFetchedCoverageCompleteRejectsRecentETFFragment(t *testing.T) {
	coverage := &store.KlineCoverageInfo{
		ListDate: "2024-11-07",
		Status:   "listed",
	}
	fragment := []model.Kline{
		{Date: "2026-01-05"},
		{Date: "2026-07-24"},
	}
	if fetchedCoverageComplete(fragment, coverage, "0", "2026-07-24") {
		t.Fatal("recent-only ETF fragment must not be considered complete")
	}
}

func TestFetchedCoverageCompleteAcceptsListingHistory(t *testing.T) {
	coverage := &store.KlineCoverageInfo{
		ListDate: "2024-11-07",
		Status:   "listed",
	}
	complete := []model.Kline{
		{Date: "2024-11-07"},
		{Date: "2026-07-24"},
	}
	if !fetchedCoverageComplete(complete, coverage, "0", "2026-07-24") {
		t.Fatal("listing-to-target history should be complete")
	}
}

func TestFetchedCoverageCompleteAcceptsIncrementalTail(t *testing.T) {
	coverage := &store.KlineCoverageInfo{
		ListDate:             "2010-01-01",
		Status:               "listed",
		HistoryStartComplete: true,
	}
	increment := []model.Kline{{Date: "2026-07-24"}}
	if !fetchedCoverageComplete(increment, coverage, "20260724", "2026-07-24") {
		t.Fatal("incremental tail should not need to contain listing date")
	}
}

func TestFetchedCoverageCompleteUsesDelistedTarget(t *testing.T) {
	coverage := &store.KlineCoverageInfo{
		ListDate:      "1998-06-01",
		LastTradeDate: "2009-12-29",
		Status:        "delisted",
	}
	history := []model.Kline{
		{Date: "1998-06-01"},
		{Date: "2009-12-29"},
	}
	if !fetchedCoverageComplete(history, coverage, "0", "2026-07-24") {
		t.Fatal("delisted security should use its last trade date as target")
	}
}

func TestFetchedCoverageCompleteSkipsFutureListing(t *testing.T) {
	coverage := &store.KlineCoverageInfo{
		ListDate: "2026-07-27",
		Status:   "listed",
	}
	if !fetchedCoverageComplete([]model.Kline{{Date: "2026-07-24"}}, coverage, "0", "2026-07-24") {
		t.Fatal("future listing should not require provider history")
	}
}

func TestMergeKlinesKeepsFirstSourceAndAddsMissingDates(t *testing.T) {
	base := []model.Kline{
		{Date: "2026-07-23", Close: 10},
		{Date: "2026-07-24", Close: 11},
	}
	extra := []model.Kline{
		{Date: "2024-11-07", Close: 5},
		{Date: "2026-07-24", Close: 99},
	}
	merged := mergeKlines(base, extra)
	if len(merged) != 3 {
		t.Fatalf("expected 3 merged rows, got %d", len(merged))
	}
	if merged[0].Date != "2024-11-07" || merged[2].Date != "2026-07-24" {
		t.Fatalf("merged rows are not sorted: %#v", merged)
	}
	if merged[2].Close != 11 {
		t.Fatalf("first provider value must win, got close=%v", merged[2].Close)
	}
}
