package api

import "testing"

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
