package store

import "testing"

// 构造 20 日等权行情：前 windowA 日每日 changeA，其后每日 changeB。
func stanceSeries(changeA float64, daysA int, changeB float64, daysB int, upRatio float64) (avgChanges, upRatios []float64) {
	for i := 0; i < daysA; i++ {
		avgChanges = append(avgChanges, changeA)
		upRatios = append(upRatios, upRatio)
	}
	for i := 0; i < daysB; i++ {
		avgChanges = append(avgChanges, changeB)
		upRatios = append(upRatios, upRatio)
	}
	return avgChanges, upRatios
}

func TestClassifyReviewMarketStanceTakeProfit(t *testing.T) {
	// 连续上涨 20 日、贴近新高、5 日动量远超 3% → 落袋
	avgChanges, upRatios := stanceSeries(0.8, 20, 0, 0, 0.7)
	stance := classifyReviewMarketStance(avgChanges, upRatios)
	if stance.Stance != "take_profit" {
		t.Fatalf("stance=%s want take_profit (mom5=%.2f dd=%.2f)", stance.Stance, stance.Momentum5D, stance.DrawdownPct)
	}
	if stance.Momentum5D < 3.0 || stance.DrawdownPct > 1.0 {
		t.Fatalf("metrics inconsistent: mom5=%.2f dd=%.2f", stance.Momentum5D, stance.DrawdownPct)
	}
}

func TestClassifyReviewMarketStanceAccumulate(t *testing.T) {
	// 先涨 10 日建立高点，再跌 6 日回撤超 4%，最后 4 日企稳且宽度回暖 → 扫货
	avgChanges, upRatios := stanceSeries(1.0, 10, -1.0, 6, 0.4)
	avgChanges = append(avgChanges, 0.1, 0.1, 0.1, 0.1)
	upRatios = append(upRatios, 0.55, 0.55, 0.6, 0.6)
	stance := classifyReviewMarketStance(avgChanges, upRatios)
	if stance.Stance != "accumulate" {
		t.Fatalf("stance=%s want accumulate (mom5=%.2f dd=%.2f up=%.2f)", stance.Stance, stance.Momentum5D, stance.DrawdownPct, stance.UpRatioToday)
	}
}

func TestClassifyReviewMarketStanceHoldOnFallingKnife(t *testing.T) {
	// 回撤够深但 5 日动量仍在急跌（不接飞刀）→ 扛单
	avgChanges, upRatios := stanceSeries(1.0, 10, -1.2, 10, 0.3)
	stance := classifyReviewMarketStance(avgChanges, upRatios)
	if stance.Stance != "hold" {
		t.Fatalf("stance=%s want hold (mom5=%.2f dd=%.2f)", stance.Stance, stance.Momentum5D, stance.DrawdownPct)
	}
}

func TestClassifyReviewMarketStanceInsufficientData(t *testing.T) {
	avgChanges, upRatios := stanceSeries(0.5, 5, 0, 0, 0.6)
	stance := classifyReviewMarketStance(avgChanges, upRatios)
	if stance.Stance != "hold" || stance.Momentum5D != 0 {
		t.Fatalf("insufficient data must default to hold, got %s mom5=%.2f", stance.Stance, stance.Momentum5D)
	}
}
