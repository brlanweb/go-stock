package store

import (
	"strings"
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

func globalQuote(symbol string, price, chgPct float64) model.GlobalQuote {
	return model.GlobalQuote{Symbol: symbol, Price: &price, ChangePct: &chgPct}
}

// 千股跌停前夜的典型外盘组合：A50 夜盘暴跌 + 金龙重挫 + 美股大跌 + VIX 飙升。
// 必须判红灯：触发持仓防御模式并在推荐中注入强警示（不再跳过当日推荐）。
func TestClassifyGlobalRiskGateRedOnOvernightCrash(t *testing.T) {
	quotes := []model.GlobalQuote{
		globalQuote("CN00Y", 14200, -2.2),
		globalQuote("HXC", 6100, -4.1),
		globalQuote("SPX", 7500, -2.3),
		globalQuote("NDX", 25400, -2.8),
		globalQuote("VIX", 32.5, 24.0),
		globalQuote("USDCNH", 6.82, 0.7),
	}
	gate := ClassifyGlobalRiskGate("2026-08-19", quotes)
	if gate.Level != MarketGateRed {
		t.Fatalf("expected red, got %s score=%d (%s)", gate.Level, gate.Score, gate.Reason)
	}
	if !gate.Defensive() {
		t.Fatal("red gate must enable defensive mode")
	}
	if !strings.Contains(gate.Reason, "A50") {
		t.Fatalf("reason should mention A50: %s", gate.Reason)
	}
}

// A50 单因子暴跌（≤-2%）即接近红灯（-3 分），叠加任一 -1 因子应触发红灯。
// 这是 A50 权重最高的设计验证：它直接定价 A 股开盘预期。
func TestClassifyGlobalRiskGateA50Dominance(t *testing.T) {
	quotes := []model.GlobalQuote{
		globalQuote("CN00Y", 14100, -2.5),
		globalQuote("HXC", 6400, -1.8), // -1
		globalQuote("SPX", 7700, 0.2),
		globalQuote("NDX", 26300, 0.1),
		globalQuote("VIX", 15.0, -2.0),
		globalQuote("USDCNH", 6.73, 0.0),
	}
	gate := ClassifyGlobalRiskGate("2026-08-19", quotes)
	if gate.Level != MarketGateRed {
		t.Fatalf("A50 -2.5%% + ADR -1.8%% should be red, got %s score=%d", gate.Level, gate.Score)
	}
	// A50 单独 -2.5% 且其余全部平稳时为 -3 分 → 黄灯（观察，不建仓）。
	calm := []model.GlobalQuote{
		globalQuote("CN00Y", 14100, -2.5),
		globalQuote("SPX", 7700, 0.2),
		globalQuote("NDX", 26300, 0.1),
		globalQuote("VIX", 15.0, -2.0),
		globalQuote("USDCNH", 6.73, 0.0),
	}
	gate = ClassifyGlobalRiskGate("2026-08-19", calm)
	if gate.Level != MarketGateYellow {
		t.Fatalf("A50 crash alone should be yellow, got %s score=%d", gate.Level, gate.Score)
	}
}

// 美股温和回调（单因子 -1）不应干扰推荐：绿灯放行。
func TestClassifyGlobalRiskGateGreenOnMildPullback(t *testing.T) {
	quotes := []model.GlobalQuote{
		globalQuote("CN00Y", 14760, -0.2),
		globalQuote("HXC", 6350, -0.5),
		globalQuote("SPX", 7690, -0.9),
		globalQuote("NDX", 26200, -1.0),
		globalQuote("VIX", 16.2, 4.0),
		globalQuote("USDCNH", 6.73, 0.1),
	}
	gate := ClassifyGlobalRiskGate("2026-08-19", quotes)
	if gate.Level != MarketGateGreen {
		t.Fatalf("expected green, got %s score=%d (%s)", gate.Level, gate.Score, gate.Reason)
	}
	if gate.Score != -1 {
		t.Fatalf("expected score -1 (us only), got %d", gate.Score)
	}
}

// 多因子共振偏弱但未达红灯 → 黄灯：推荐仅观察、风险上限收紧。
func TestClassifyGlobalRiskGateYellowOnBroadWeakness(t *testing.T) {
	quotes := []model.GlobalQuote{
		globalQuote("CN00Y", 14650, -0.9), // -1
		globalQuote("HXC", 6300, -1.7),    // -1
		globalQuote("SPX", 7710, -0.3),
		globalQuote("NDX", 26250, -0.2),
		globalQuote("VIX", 26.0, 6.0), // -1
		globalQuote("USDCNH", 6.74, 0.2),
	}
	gate := ClassifyGlobalRiskGate("2026-08-19", quotes)
	if gate.Level != MarketGateYellow {
		t.Fatalf("expected yellow, got %s score=%d (%s)", gate.Level, gate.Score, gate.Reason)
	}
}

// A50 与美股同时缺失：隔夜风险不可感知，必须保守降黄灯而不是放行。
func TestClassifyGlobalRiskGateYellowOnMissingCoreData(t *testing.T) {
	quotes := []model.GlobalQuote{
		globalQuote("VIX", 15.0, 1.0),
		globalQuote("USDCNH", 6.73, 0.0),
	}
	gate := ClassifyGlobalRiskGate("2026-08-19", quotes)
	if gate.Level != MarketGateYellow {
		t.Fatalf("missing core data should degrade to yellow, got %s", gate.Level)
	}
	if !strings.Contains(gate.Reason, "缺失") {
		t.Fatalf("reason should explain missing data: %s", gate.Reason)
	}
	// 空输入同样降档。
	gate = ClassifyGlobalRiskGate("2026-08-19", nil)
	if gate.Level != MarketGateYellow {
		t.Fatalf("empty input should degrade to yellow, got %s", gate.Level)
	}
}

// VIX 绝对水平与单日跳升双口径：任一达标即计分。
func TestClassifyGlobalRiskGateVIXDualCriteria(t *testing.T) {
	base := func(vixPrice, vixChg float64) []model.GlobalQuote {
		return []model.GlobalQuote{
			globalQuote("CN00Y", 14760, 0.1),
			globalQuote("SPX", 7700, 0.1),
			globalQuote("NDX", 26300, 0.1),
			globalQuote("VIX", vixPrice, vixChg),
		}
	}
	// 水平不高但单日 +22% 跳升 → -2。
	gate := ClassifyGlobalRiskGate("2026-08-19", base(21.0, 22.0))
	if gate.Score != -2 {
		t.Fatalf("VIX spike should score -2, got %d", gate.Score)
	}
	// 高水平但当日回落 → 仍按水平计 -1（≥25）。
	gate = ClassifyGlobalRiskGate("2026-08-19", base(26.5, -3.0))
	if gate.Score != -1 {
		t.Fatalf("VIX elevated level should score -1, got %d", gate.Score)
	}
}

// 融合规则：取最严档位，只能收紧不能放宽。
func TestStricterGateLevel(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{MarketGateGreen, MarketGateGreen, MarketGateGreen},
		{MarketGateGreen, MarketGateYellow, MarketGateYellow},
		{MarketGateYellow, MarketGateGreen, MarketGateYellow},
		{MarketGateGreen, MarketGateRed, MarketGateRed},
		{MarketGateRed, MarketGateYellow, MarketGateRed},
		{MarketGateYellow, MarketGateRed, MarketGateRed},
	}
	for _, c := range cases {
		if got := StricterGateLevel(c.a, c.b); got != c.want {
			t.Fatalf("StricterGateLevel(%s,%s)=%s want %s", c.a, c.b, got, c.want)
		}
	}
}
