package api

import (
	"testing"
	"time"
)

func TestValidAgentHistoryDays(t *testing.T) {
	for _, days := range []int{0, 10, 30, 60} {
		if !validAgentHistoryDays(days) {
			t.Fatalf("expected %d to be valid", days)
		}
	}
	for _, days := range []int{-1, 1, 20, 90, 600} {
		if validAgentHistoryDays(days) {
			t.Fatalf("expected %d to be rejected", days)
		}
	}
}

func TestNextMarketOpen(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before open", time.Date(2026, 7, 29, 8, 30, 0, 0, loc), time.Date(2026, 7, 29, 9, 0, 0, 0, loc)},
		{"after open", time.Date(2026, 7, 29, 15, 0, 0, 0, loc), time.Date(2026, 7, 30, 9, 0, 0, 0, loc)},
		{"friday close", time.Date(2026, 7, 31, 15, 0, 0, 0, loc), time.Date(2026, 8, 3, 9, 0, 0, 0, loc)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextMarketOpen(tt.now); !got.Equal(tt.want) {
				t.Fatalf("nextMarketOpen(%s) = %s, want %s", tt.now, got, tt.want)
			}
		})
	}
}
