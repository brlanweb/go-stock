package store

import (
	"math"
	"testing"
)

func TestPositionNetChangePctDeductsRoundTripCost(t *testing.T) {
	// 毛收益 3% 扣除往返成本后应低于 3%，避免「微盈实亏」被统计成盈利单。
	got := PositionNetChangePct(3)
	want := 3 - PositionRoundTripCostPct
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("净收益应扣除往返成本：got=%.4f want=%.4f", got, want)
	}
	// 毛收益 0.2% 的交易扣成本后实际为负。
	if PositionNetChangePct(0.2) >= 0 {
		t.Fatal("低于成本的微盈应结算为亏损")
	}
}

func TestPositionBlendedChangePctWeightsByPosition(t *testing.T) {
	cases := []struct {
		name        string
		realized    float64
		positionPct float64
		current     float64
		want        float64
	}{
		// 满仓未减仓：收益即当前浮盈。
		{"满仓", 0, 100, 10, 10},
		// +12% 时减仓一半锁定 6 个点，剩余半仓回落到 +4% 贡献 2 个点。
		{"减仓后回落", 6, 50, 4, 8},
		// 减仓后剩余仓位继续上涨：半仓 +20% 贡献 10 个点。
		{"减仓后续涨", 6, 50, 20, 16},
		// 已全部减仓：只剩落袋收益，不再随行情变化。
		{"清仓", 9, 0, 999, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := positionBlendedChangePct(tc.realized, tc.positionPct, tc.current)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("加权收益 got=%.4f want=%.4f", got, tc.want)
			}
		})
	}
}

// 分批止盈的核心价值：先锁定一半利润后，即使剩余仓位回吐到成本价，
// 整笔交易仍为盈利单——这是把「方向正确」转化为「盈利」的关键机制。
func TestPartialProfitTakingConvertsGiveBackIntoWin(t *testing.T) {
	// 未减仓时：+12% 涨到顶后全部回吐到 0，整笔白做。
	if got := positionBlendedChangePct(0, 100, 0); got != 0 {
		t.Fatalf("未减仓全回吐应为 0，got=%.4f", got)
	}
	// 在 +12% 减半仓锁定 6 个点后，剩余半仓即使回到成本价，整笔仍盈利 6 个点。
	blended := positionBlendedChangePct(6, 50, 0)
	if net := PositionNetChangePct(blended); net <= 0 {
		t.Fatalf("分批止盈后即使回吐也应保住盈利，got=%.4f", net)
	}
}
