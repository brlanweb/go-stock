package store

import (
	"strings"
	"testing"
)

func TestBackfillLatestPositionMigrationGuards(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/020_backfill_latest_position.sql")
	if err != nil {
		t.Fatal(err)
	}
	statements := splitSQL(string(raw))
	if len(statements) != 3 {
		t.Fatalf("expected watchlist, position, and advice statements, got %d", len(statements))
	}
	migration := string(raw)
	for _, required := range []string{
		"next_trade.pick_date = (SELECT MAX(k.trade_date) FROM kline_daily k)",
		"NOT EXISTS (\n          SELECT 1 FROM position p WHERE p.analysis_date = r.analysis_date",
		"COUNT(*) FROM watchlist w WHERE w.symbol <> r.symbol) < 10",
		"'pending_entry'",
		"source = 'daily_pick'",
	} {
		if !strings.Contains(migration, required) {
			t.Fatalf("migration missing safety guard %q", required)
		}
	}
	for _, forbidden := range []string{"entry_price", "exit_price", "change_pct"} {
		if strings.Contains(migration, forbidden) {
			t.Fatalf("backfill must not fabricate performance field %q", forbidden)
		}
	}
}
