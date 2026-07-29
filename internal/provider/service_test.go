package provider

import (
	"testing"
	"time"
)

func TestIsTradingHours(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before morning open", time.Date(2026, 7, 29, 9, 29, 59, 0, location), false},
		{"morning open", time.Date(2026, 7, 29, 9, 30, 0, 0, location), true},
		{"morning close", time.Date(2026, 7, 29, 11, 30, 0, 0, location), true},
		{"lunch break", time.Date(2026, 7, 29, 11, 31, 0, 0, location), false},
		{"afternoon open", time.Date(2026, 7, 29, 13, 0, 0, 0, location), true},
		{"afternoon close", time.Date(2026, 7, 29, 15, 0, 0, 0, location), true},
		{"after close", time.Date(2026, 7, 29, 15, 1, 0, 0, location), false},
		{"weekend", time.Date(2026, 8, 1, 10, 0, 0, 0, location), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTradingHours(tt.at); got != tt.want {
				t.Fatalf("IsTradingHours(%s) = %v, want %v", tt.at, got, tt.want)
			}
		})
	}
}

func TestIsTradingHoursConvertsToShanghaiTimezone(t *testing.T) {
	utc := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	if !IsTradingHours(utc) {
		t.Fatalf("10:00 Asia/Shanghai should be trading time")
	}
}
