package analysis

import (
	"fmt"
	"testing"
	"time"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/store"
)

func TestRankRecommendationsIsDeterministic(t *testing.T) {
	candidates := make([]store.RecommendationCandidate, 10)
	for i := range candidates {
		klines := make([]model.Kline, 60)
		for day := range klines {
			close := 10 + float64(day)*float64(i+1)/100
			klines[day] = model.Kline{
				Date:  fmt.Sprintf("2026-04-%02d", day+1),
				Open:  close - 0.01,
				Close: close,
			}
		}
		candidates[i] = store.RecommendationCandidate{
			Symbol:   fmt.Sprintf("SH600%03d", i),
			Code:     fmt.Sprintf("600%03d", i),
			Name:     fmt.Sprintf("stock-%d", i),
			Industry: "sector",
			Klines:   klines,
		}
	}

	first := rankRecommendations(candidates)
	second := rankRecommendations(candidates)
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("expected 3 recommendations, got %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("run %d differs: %#v != %#v", i, first[i], second[i])
		}
	}
	if first[0].Symbol != "SH600009" {
		t.Fatalf("expected strongest trend first, got %s", first[0].Symbol)
	}
}

func TestNextRecommendationRunSkipsWeekend(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)

	// 交易日 07:30 → 当天 08:10
	beforeRun := time.Date(2026, time.July, 23, 7, 30, 0, 0, loc)
	if next, want := nextRecommendationRun(beforeRun), time.Date(2026, time.July, 23, 8, 10, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}

	// 周五 09:00 已过运行点 → 跳过周末到周一 08:10
	fridayMorning := time.Date(2026, time.July, 24, 9, 0, 0, 0, loc)
	next := nextRecommendationRun(fridayMorning)
	want := time.Date(2026, time.July, 27, 8, 10, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
	if isRecommendationTradingDay(time.Date(2026, time.July, 25, 5, 0, 0, 0, loc)) {
		t.Fatal("Saturday must not be treated as a trading day")
	}
}

func TestPreviousTradingDaySkipsWeekend(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)

	// 周一早晨的上一交易日是上周五
	monday := time.Date(2026, time.July, 27, 8, 10, 0, 0, loc)
	if prev := previousTradingDay(monday); prev.Format("2006-01-02") != "2026-07-24" {
		t.Fatalf("previous trading day = %s, want 2026-07-24", prev.Format("2006-01-02"))
	}
	// 周三的上一交易日是周二
	wednesday := time.Date(2026, time.July, 22, 8, 10, 0, 0, loc)
	if prev := previousTradingDay(wednesday); prev.Format("2006-01-02") != "2026-07-21" {
		t.Fatalf("previous trading day = %s, want 2026-07-21", prev.Format("2006-01-02"))
	}
}

func TestNextHotspotRunMorningSchedule(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)

	// 交易日 07:30 → 当天 08:00
	beforeOpen := time.Date(2026, time.July, 23, 7, 30, 0, 0, loc)
	if next, want := nextHotspotRun(beforeOpen), time.Date(2026, time.July, 23, 8, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}

	// 交易日 08:00 整点已过 → 次一交易日 08:00
	atOpen := time.Date(2026, time.July, 23, 8, 0, 0, 0, loc)
	if next, want := nextHotspotRun(atOpen), time.Date(2026, time.July, 24, 8, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}

	// 周五 09:00 → 跳过周末到周一 08:00
	fridayMorning := time.Date(2026, time.July, 24, 9, 0, 0, 0, loc)
	if next, want := nextHotspotRun(fridayMorning), time.Date(2026, time.July, 27, 8, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}

	// 周六任意时刻 → 周一 08:00
	saturday := time.Date(2026, time.July, 25, 12, 0, 0, 0, loc)
	if next, want := nextHotspotRun(saturday), time.Date(2026, time.July, 27, 8, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
}

// 自动建仓开关必须默认关闭：策略正期望尚未被回测证明前，
// 推荐只能作为观察项输出，绝不能因为默认值漂移而擅自开仓。
func TestAutoEntryDisabledByDefault(t *testing.T) {
	service := New(nil, Config{})
	if service.AutoEntryEnabled() {
		t.Fatal("自动建仓必须默认关闭")
	}
	if enabled := New(nil, Config{AutoEntryEnabled: true}); !enabled.AutoEntryEnabled() {
		t.Fatal("显式开启后 AutoEntryEnabled 必须为 true")
	}
}
