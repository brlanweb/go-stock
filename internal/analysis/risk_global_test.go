package analysis

import (
	"strings"
	"testing"

	"github.com/hoax/go-stock/internal/store"
)

// 防御模式（全球风险门红灯）下确定性风控的收紧行为验证。
// 场景原型：隔夜外盘预警的普跌日（千股跌停），持仓必须比常规纪律更早离场。

// 硬止损收紧：常规阈值 6% 时亏 4.5% 不触发；防御模式阈值 4.2%（6%×0.7）应触发。
func TestEvaluateRiskDefensiveStopLossTightened(t *testing.T) {
	base := riskInput{
		Price: 9.55, EntryPrice: 10.0, HighestPrice: 10.0, // 亏损 4.5%
		HoldDays: 2, PositionPct: 100,
	}
	if d := evaluateRisk(base); d.Action != riskActionNone {
		t.Fatalf("normal mode should not stop loss at -4.5%%: %+v", d)
	}
	base.GlobalRed = true
	d := evaluateRisk(base)
	if d.Action != riskActionExit || d.Kind != store.ExitKindStopLoss {
		t.Fatalf("defensive mode should stop loss at -4.5%%: %+v", d)
	}
	if !strings.Contains(d.Reason, "红灯") {
		t.Fatalf("reason should mention defensive mode: %s", d.Reason)
	}
}

// 系统性风险提前触发：大盘平均 -1.0% 常规不触发（阈值 -1.5%），
// 防御模式阈值 -0.8% 应立即退出——等跌满 1.5% 往往已封跌停卖不出去。
func TestEvaluateRiskDefensiveSystemicEarlyTrigger(t *testing.T) {
	base := riskInput{
		Price: 10.1, EntryPrice: 10.0, HighestPrice: 10.2,
		HoldDays: 2, PositionPct: 100,
		IndexTotal: 6, IndexFalling: 5, MarketAvgPct: -1.0,
	}
	if d := evaluateRisk(base); d.Action != riskActionNone {
		t.Fatalf("normal mode should not trigger systemic at -1.0%%: %+v", d)
	}
	base.GlobalRed = true
	d := evaluateRisk(base)
	if d.Action != riskActionExit || d.Kind != store.ExitKindSystemic {
		t.Fatalf("defensive mode should trigger systemic at -1.0%%: %+v", d)
	}
	if !strings.Contains(d.Reason, "全球风险门红灯") {
		t.Fatalf("reason should attribute global gate: %s", d.Reason)
	}
}

// 移动止盈收紧：峰值浮盈 8%、现浮盈 5%（回撤 3%）常规不触发（阈值 4%），
// 防御模式阈值 2.8%（4%×0.7）应锁定利润离场。
func TestEvaluateRiskDefensiveTrailingTightened(t *testing.T) {
	base := riskInput{
		Price: 10.5, EntryPrice: 10.0, HighestPrice: 10.8, // 峰值 8%，现 5%
		HoldDays: 2, PositionPct: 100,
	}
	if d := evaluateRisk(base); d.Action != riskActionNone {
		t.Fatalf("normal mode should hold at 3%% giveback: %+v", d)
	}
	base.GlobalRed = true
	d := evaluateRisk(base)
	if d.Action != riskActionExit || d.Kind != store.ExitKindTrailingStop {
		t.Fatalf("defensive mode should trail out at 3%% giveback: %+v", d)
	}
}

// 趋势破位放开盘中确认：非尾盘档常规不判破位，防御模式盘中即可确认离场。
func TestEvaluateRiskDefensiveIntradayTrendBreak(t *testing.T) {
	base := riskInput{
		Price: 9.75, EntryPrice: 10.0, HighestPrice: 10.0, // 亏 2.5%，未到止损
		HoldDays: 2, PositionPct: 100,
		MA10: 10.0, SectorWeak: true, IsTailSlot: false,
	}
	if d := evaluateRisk(base); d.Action != riskActionNone {
		t.Fatalf("normal mode should wait for tail slot: %+v", d)
	}
	base.GlobalRed = true
	d := evaluateRisk(base)
	if d.Action != riskActionExit || d.Kind != store.ExitKindTrendBreak {
		t.Fatalf("defensive mode should confirm trend break intraday: %+v", d)
	}
	if !strings.Contains(d.Reason, "盘中") {
		t.Fatalf("reason should mention intraday confirmation: %s", d.Reason)
	}
}

// T+1 硬约束优先级最高：建仓当日即使防御模式也不能卖出。
func TestEvaluateRiskDefensiveRespectsT1(t *testing.T) {
	d := evaluateRisk(riskInput{
		Price: 9.0, EntryPrice: 10.0, HighestPrice: 10.0, // 亏 10%
		HoldDays: 0, PositionPct: 100, GlobalRed: true,
	})
	if d.Action != riskActionNone {
		t.Fatalf("T+1 must block same-day exit even in defensive mode: %+v", d)
	}
}
