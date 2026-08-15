package analysis

import (
	"fmt"
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

func monteCarloTestKlines(n int) []model.Kline {
	klines := make([]model.Kline, n)
	price := 10.0
	for i := range klines {
		// 交替 +1.2% / -0.6% 的确定性序列，整体温和上行
		if i%2 == 0 {
			price *= 1.012
		} else {
			price *= 0.994
		}
		klines[i] = model.Kline{Date: fmt.Sprintf("2026-01-%02d", i%28+1), Close: price}
	}
	return klines
}

func TestRunMonteCarloDeterministicAndBounded(t *testing.T) {
	klines := monteCarloTestKlines(200)
	first, err := RunMonteCarlo("SH600000", klines, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := RunMonteCarlo("SH600000", klines, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Fatal("同一输入必须产生完全一致的模拟结果（确定性种子）")
	}
	if first.Paths != monteCarloPaths || first.Days != 10 {
		t.Fatalf("unexpected meta: %+v", first)
	}
	if first.P5Pct > first.MedianPct || first.MedianPct > first.P95Pct {
		t.Fatalf("percentiles must be ordered: %+v", first)
	}
	if first.WinRate < 0 || first.WinRate > 100 {
		t.Fatalf("win rate out of range: %f", first.WinRate)
	}
	// 温和上行序列的模拟胜率应显著高于 50%
	if first.WinRate < 55 {
		t.Fatalf("expected upward-biased win rate, got %f", first.WinRate)
	}
}

func TestRunMonteCarloRejectsShortHistory(t *testing.T) {
	if _, err := RunMonteCarlo("SH600000", monteCarloTestKlines(30), 10); err == nil {
		t.Fatal("insufficient history must be rejected")
	}
}

func TestRunMonteCarloClampsDays(t *testing.T) {
	result, err := RunMonteCarlo("SH600000", monteCarloTestKlines(200), 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Days != monteCarloMaxDays {
		t.Fatalf("days must clamp to %d, got %d", monteCarloMaxDays, result.Days)
	}
}
