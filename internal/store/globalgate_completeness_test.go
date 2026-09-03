package store

import (
	"strings"
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

// 2026-09 复盘回归测试：因子缺失不得被静默当作「无风险」。
//
// 生产数据显示 vix 与 china_adr 长期 has_data=false，5 个因子实际只有 3 个
// 在工作，而近 10 个交易日全部判定为 green。VIX 与金龙合计可贡献 -4 分，
// 恰好等于红灯阈值，两者失效意味着红灯几乎不可能触发。
// 旧守卫只在「A50 与美股同时缺失」时降档，完全掩盖了这类部分失效。
func TestClassifyGlobalRiskGateYellowOnInsufficientFactors(t *testing.T) {
	// 只有 A50 与美股两个因子有数据：低于最小有效因子数 → 保守降档。
	quotes := []model.GlobalQuote{
		globalQuote("CN00Y", 14600, 0.1),
		globalQuote("SPX", 7500, 0.2),
	}
	gate := ClassifyGlobalRiskGate("2026-09-02", quotes)
	if gate.Level != MarketGateYellow {
		t.Fatalf("有效因子不足必须降档: got=%s reason=%s", gate.Level, gate.Reason)
	}
	if !strings.Contains(gate.Reason, "缺失") {
		t.Fatalf("降档理由必须点名缺失因子: %s", gate.Reason)
	}
}

// 生产实况回归：a50 / us_equity / fx 有数据，vix 与 china_adr 缺失。
// 恰好达到最小因子数，允许绿灯，但必须在结论中如实披露感知范围不完整，
// 避免「感知不到风险」被读成「已确认安全」。
func TestClassifyGlobalRiskGateGreenDisclosesMissingFactors(t *testing.T) {
	quotes := []model.GlobalQuote{
		globalQuote("CN00Y", 14689, -0.09),
		globalQuote("SPX", 7500, -0.22),
		globalQuote("NDX", 25000, -0.23),
		globalQuote("USDCNH", 6.7173, -0.01),
	}
	gate := ClassifyGlobalRiskGate("2026-09-02", quotes)
	if gate.Level != MarketGateGreen {
		t.Fatalf("三个有效因子且外盘平稳应为绿灯: got=%s", gate.Level)
	}
	if !strings.Contains(gate.Reason, "风险感知范围不完整") {
		t.Fatalf("绿灯必须披露缺失因子: %s", gate.Reason)
	}
	for _, name := range []string{"VIX恐慌指数", "金龙指数"} {
		if !strings.Contains(gate.Reason, name) {
			t.Fatalf("缺失因子 %s 未在理由中点名: %s", name, gate.Reason)
		}
	}
}

// 因子齐全且平稳时不应出现缺失披露，避免噪音。
func TestClassifyGlobalRiskGateGreenSilentWhenComplete(t *testing.T) {
	quotes := []model.GlobalQuote{
		globalQuote("CN00Y", 14689, 0.1),
		globalQuote("HXC", 6800, 0.3),
		globalQuote("SPX", 7500, 0.2),
		globalQuote("NDX", 25000, 0.25),
		globalQuote("VIX", 14.2, -1.5),
		globalQuote("USDCNH", 6.7173, -0.01),
	}
	gate := ClassifyGlobalRiskGate("2026-09-02", quotes)
	if gate.Level != MarketGateGreen {
		t.Fatalf("因子齐全且平稳应为绿灯: got=%s", gate.Level)
	}
	if strings.Contains(gate.Reason, "风险感知范围不完整") {
		t.Fatalf("因子齐全时不应出现缺失披露: %s", gate.Reason)
	}
}
