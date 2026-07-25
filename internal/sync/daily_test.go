package sync

import (
	"testing"

	"github.com/hoax/go-stock/internal/provider"
)

func TestSnapshotTradeDateUsesMajority(t *testing.T) {
	snaps := []provider.SecuritySnapshot{
		{TradeDate: "2026-07-24", Price: 1},
		{TradeDate: "2026-07-24", Price: 2},
		{TradeDate: "2026-07-25", Price: 3},
		{TradeDate: "2026-07-25", Price: 0},
	}
	if got := snapshotTradeDate(snaps); got != "2026-07-24" {
		t.Fatalf("snapshotTradeDate()=%q, want 2026-07-24", got)
	}
}

func TestSnapshotTradeDateEmpty(t *testing.T) {
	if got := snapshotTradeDate(nil); got != "" {
		t.Fatalf("snapshotTradeDate()=%q, want empty", got)
	}
}
