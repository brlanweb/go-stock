package analysis

import (
	"math"
	"testing"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/store"
)

// baseHolding 是一个「健康持仓」基线：小幅浮盈、已过 T+1、大盘平稳，
// 各用例只改动待验证的那一个维度，避免相互干扰。
//
// HoldDays 取 PositionMinExitHoldDays 而非 1：hold_days=1 表示建仓当日，
// 受 T+1 限制不可卖出，用它做基线会让所有退出用例恒为 none。
func baseHolding() riskInput {
	return riskInput{
		Price:        104,
		EntryPrice:   100,
		HighestPrice: 104,
		HoldDays:     store.PositionMinExitHoldDays,
		PositionPct:  100,
		ATRPct:       2,
		MA10:         100,
		MA20:         98,
		MarketAvgPct: 0.3,
		IndexTotal:   6,
		IndexFalling: 1,
	}
}

func TestEvaluateRiskHealthyHoldingYieldsNoAction(t *testing.T) {
	if got := evaluateRisk(baseHolding()); got.Action != riskActionNone {
		t.Fatalf("健康持仓不应触发风控，got=%+v", got)
	}
}

func TestEvaluateRiskHardStopLoss(t *testing.T) {
	in := baseHolding()
	in.Price, in.HighestPrice = 93, 100 // -7%，超过 6% 固定止损
	got := evaluateRisk(in)
	if got.Action != riskActionExit || got.Kind != store.ExitKindStopLoss {
		t.Fatalf("应触发硬止损，got=%+v", got)
	}
}

// 止损优先级必须最高：同时满足止损与其他条件时，归因应为 stop_loss。
func TestEvaluateRiskStopLossTakesPrecedence(t *testing.T) {
	in := baseHolding()
	in.Price, in.HighestPrice = 90, 120
	in.HoldDays = 9
	in.IsTailSlot, in.MA10, in.SectorWeak = true, 110, true
	if got := evaluateRisk(in); got.Kind != store.ExitKindStopLoss {
		t.Fatalf("止损应优先于其他规则，got=%+v", got)
	}
}

// 高波动股应获得更宽的止损距离，避免被正常波动扫出。
func TestEvaluateRiskATRWidensStopForVolatileStock(t *testing.T) {
	in := baseHolding()
	in.ATRPct = 5 // 5×1.8=9% > 6% 固定值
	in.Price, in.HighestPrice = 93, 100
	if got := evaluateRisk(in); got.Action != riskActionNone {
		t.Fatalf("高波动股 -7%% 不应触发 9%% 止损，got=%+v", got)
	}
	in.Price = 90.5 // -9.5%，超过自适应止损
	if got := evaluateRisk(in); got.Kind != store.ExitKindStopLoss {
		t.Fatalf("超过自适应止损应退出，got=%+v", got)
	}
}

func TestStopLossDistanceRespectsCap(t *testing.T) {
	if got := stopLossDistancePct(50); got != store.PositionStopLossMaxPct {
		t.Fatalf("极端波动止损距离应封顶到 %.1f，got=%.2f", store.PositionStopLossMaxPct, got)
	}
	if got := stopLossDistancePct(0); got != store.PositionStopLossPct {
		t.Fatalf("无 ATR 时应回退固定止损，got=%.2f", got)
	}
}

func TestEvaluateRiskTrailingStopProtectsProfit(t *testing.T) {
	in := baseHolding()
	in.HighestPrice, in.Price = 118, 113 // 峰值 +18%，回落到 +13%，回撤 5%
	got := evaluateRisk(in)
	if got.Action != riskActionExit || got.Kind != store.ExitKindTrailingStop {
		t.Fatalf("应触发移动止盈，got=%+v", got)
	}
}

// 移动止盈未激活前（浮盈低于 arm 线），小幅回撤不应误伤。
func TestEvaluateRiskTrailingNotArmedBelowThreshold(t *testing.T) {
	in := baseHolding()
	in.HighestPrice, in.Price = 104, 100.5
	if got := evaluateRisk(in); got.Action != riskActionNone {
		t.Fatalf("未达激活线不应触发移动止盈，got=%+v", got)
	}
}

func TestEvaluateRiskTakeProfitReducesThenExits(t *testing.T) {
	in := baseHolding()
	in.Price, in.HighestPrice = 113, 113 // +13% 达到止盈线
	got := evaluateRisk(in)
	if got.Action != riskActionReduce || got.Kind != store.ExitKindTakeProfit {
		t.Fatalf("满仓达标应先减仓，got=%+v", got)
	}
	in.PositionPct = store.PositionMinPositionPct // 已减到最小阈值
	got = evaluateRisk(in)
	if got.Action != riskActionExit || got.Kind != store.ExitKindTakeProfit {
		t.Fatalf("仓位已减至阈值应清仓，got=%+v", got)
	}
}

func TestEvaluateRiskTimeStopOnDecayedMomentum(t *testing.T) {
	in := baseHolding()
	in.HoldDays, in.Price, in.HighestPrice = 3, 101, 102 // 3 天仅 +1%
	got := evaluateRisk(in)
	if got.Action != riskActionExit || got.Kind != store.ExitKindTimeStop {
		t.Fatalf("动量衰减应触发时间止损，got=%+v", got)
	}
}

// 时间止损只针对「未兑现」的持仓，盈利达标的不应被扫出。
func TestEvaluateRiskTimeStopSkipsProfitablePosition(t *testing.T) {
	in := baseHolding()
	in.HoldDays, in.Price, in.HighestPrice = 5, 106, 106
	if got := evaluateRisk(in); got.Action != riskActionNone {
		t.Fatalf("盈利达标不应触发时间止损，got=%+v", got)
	}
}

func TestEvaluateRiskSystemicUsesRatioNotUnanimity(t *testing.T) {
	in := baseHolding()
	// 6 个指数中 4 个下跌、均值 -2%：旧实现要求「全部下跌」几乎永不触发。
	in.IndexFalling, in.MarketAvgPct = 4, -2.0
	got := evaluateRisk(in)
	if got.Action != riskActionExit || got.Kind != store.ExitKindSystemic {
		t.Fatalf("2/3 指数下跌且均跌 2%% 应触发系统性退出，got=%+v", got)
	}
}

func TestEvaluateRiskTrendBreakOnlyAtTailSlot(t *testing.T) {
	in := baseHolding()
	in.Price, in.HighestPrice, in.MA10, in.SectorWeak = 98, 104, 100, true
	// 盘中插针：非尾盘档不应因跌破均线就不可逆清仓。
	in.IsTailSlot = false
	if got := evaluateRisk(in); got.Action != riskActionNone {
		t.Fatalf("盘中跌破均线不应立即清仓，got=%+v", got)
	}
	in.IsTailSlot = true
	if got := evaluateRisk(in); got.Kind != store.ExitKindTrendBreak {
		t.Fatalf("尾盘确认破位应退出，got=%+v", got)
	}
}

// 缓冲带：尾盘仅微幅跌破 MA10（<1%）视为噪音，不触发退出。
func TestEvaluateRiskTrendBreakRequiresBuffer(t *testing.T) {
	in := baseHolding()
	in.IsTailSlot, in.MA10, in.SectorWeak = true, 100, true
	in.Price, in.HighestPrice = 99.5, 104
	if got := evaluateRisk(in); got.Action != riskActionNone {
		t.Fatalf("未跌破缓冲带不应退出，got=%+v", got)
	}
}

func TestEvaluateRiskMaxHoldDaysFallback(t *testing.T) {
	in := baseHolding()
	in.HoldDays, in.Price, in.HighestPrice = store.PositionMaxHoldDays, 104, 104
	if got := evaluateRisk(in); got.Action != riskActionExit {
		t.Fatalf("达到最长持有应退出，got=%+v", got)
	}
}

// T+1 硬约束：建仓当日（hold_days=1）即使触发任何一条退出规则也不得卖出。
// 这是数据可信度的地基——当日进出的样本在 A 股现实中不可能成交。
func TestEvaluateRiskForbidsSameDayExit(t *testing.T) {
	cases := map[string]func(in *riskInput){
		"硬止损":   func(in *riskInput) { in.Price, in.HighestPrice = 90, 100 },
		"移动止盈":  func(in *riskInput) { in.HighestPrice, in.Price = 118, 113 },
		"硬止盈":   func(in *riskInput) { in.Price, in.HighestPrice = 113, 113 },
		"系统性风险": func(in *riskInput) { in.IndexFalling, in.MarketAvgPct = 5, -3.0 },
		"尾盘破位": func(in *riskInput) {
			in.IsTailSlot, in.MA10, in.SectorWeak = true, 110, true
			in.Price = 98
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := baseHolding()
			in.HoldDays = 1 // 建仓当日
			mutate(&in)
			if got := evaluateRisk(in); got.Action != riskActionNone {
				t.Fatalf("建仓当日受 T+1 限制不得产生退出动作，got=%+v", got)
			}
			// 次一交易日同样的行情必须正常触发，证明守卫只推迟、不吞掉纪律。
			in.HoldDays = store.PositionMinExitHoldDays
			if got := evaluateRisk(in); got.Action == riskActionNone {
				t.Fatalf("次一交易日应恢复风控判定，got=%+v", got)
			}
		})
	}
}

func TestEvaluateRiskUsesDynamicPolicySnapshot(t *testing.T) {
	in := baseHolding()
	in.Price, in.HighestPrice = 95, 100 // -5%，默认6%止损不会触发
	if got := evaluateRisk(in); got.Action != riskActionNone {
		t.Fatalf("默认6%%止损下 -5%% 不应退出，got=%+v", got)
	}
	policy := store.DefaultStrategyRiskPolicy()
	policy.StopLossPct = 4
	policy.StopLossATRMult = 1 // ATR=2%，不放宽4%固定止损
	in.Policy = policy
	if got := evaluateRisk(in); got.Kind != store.ExitKindStopLoss {
		t.Fatalf("动态4%%止损应在 -5%% 时退出，got=%+v", got)
	}
}

func TestEvaluateRiskIgnoresIncompleteInput(t *testing.T) {
	if got := evaluateRisk(riskInput{Price: 0, EntryPrice: 100}); got.Action != riskActionNone {
		t.Fatalf("缺少价格时不得决策，got=%+v", got)
	}
	if got := evaluateRisk(riskInput{Price: 100, EntryPrice: 0}); got.Action != riskActionNone {
		t.Fatalf("缺少建仓价时不得决策，got=%+v", got)
	}
}

func TestAtrPercent(t *testing.T) {
	klines := make([]model.Kline, 20)
	for i := range klines {
		base := 100.0
		klines[i] = model.Kline{High: base + 2, Low: base - 2, Close: base}
	}
	got := atrPercent(klines)
	if math.Abs(got-4.0) > 0.01 {
		t.Fatalf("等幅波动 ATR 应为 4%%，got=%.3f", got)
	}
	if atrPercent(klines[:5]) != 0 {
		t.Fatal("数据不足应返回 0 以回退固定止损")
	}
}

// 回归：恰好 20 根 K 线时曾因访问 closes[last-20] 越界 panic，
// 而盘中分析是整批处理，一只新股就能中断当轮全部持仓的风控。
func TestFillPositionIndicatorsDoesNotPanicOnShortHistory(t *testing.T) {
	for _, n := range []int{0, 1, 19, 20, 21, 60} {
		klines := make([]model.Kline, n)
		for i := range klines {
			price := float64(10 + i)
			klines[i] = model.Kline{Open: price, High: price + 1, Low: price - 1, Close: price, Volume: 1000}
		}
		var item positionAnalysisItem
		fillPositionIndicators(&item, klines) // 不得 panic
		if n >= 21 && item.MA20 == 0 {
			t.Fatalf("%d 根 K 线应能算出 MA20", n)
		}
	}
}

// K 线不足导致 ATRPct=0 时，止损必须回退到固定距离而不是失效。
// 新股、次新股正是波动最大、最需要止损保护的标的。
func TestEvaluateRiskStopLossStillWorksWithoutATR(t *testing.T) {
	in := baseHolding()
	in.ATRPct = 0
	in.Price, in.HighestPrice = 93, 100
	if got := evaluateRisk(in); got.Kind != store.ExitKindStopLoss {
		t.Fatalf("无 ATR 时仍须触发固定止损，got=%+v", got)
	}
}

// 均线数据缺失（MA10=0）时不得误判趋势破位。
func TestEvaluateRiskSkipsTrendBreakWithoutMA(t *testing.T) {
	in := baseHolding()
	in.IsTailSlot, in.MA10, in.MA20, in.SectorWeak = true, 0, 0, true
	in.Price, in.HighestPrice = 102, 104
	if got := evaluateRisk(in); got.Action != riskActionNone {
		t.Fatalf("缺少均线时不得判定破位，got=%+v", got)
	}
}

// 减仓到最小阈值后若再次达到止盈条件，必须清仓而不是无限减仓，
// 否则会留下既不满足减仓、又永远等不到清仓的「僵尸持仓」。
func TestEvaluateRiskNeverReducesBelowMinimum(t *testing.T) {
	in := baseHolding()
	in.Price, in.HighestPrice = 113, 113
	for _, pct := range []float64{store.PositionMinPositionPct, store.PositionMinPositionPct - 10, 1} {
		in.PositionPct = pct
		got := evaluateRisk(in)
		if got.Action != riskActionExit {
			t.Fatalf("仓位 %.0f%% 时应清仓而非继续减仓，got=%+v", pct, got)
		}
	}
}

// 指数数据不足 3 个时不得做系统性风险判定，避免样本太小的误杀。
func TestEvaluateRiskSystemicNeedsEnoughIndices(t *testing.T) {
	in := baseHolding()
	in.IndexTotal, in.IndexFalling, in.MarketAvgPct = 2, 2, -5
	if got := evaluateRisk(in); got.Kind == store.ExitKindSystemic {
		t.Fatalf("指数样本不足不得判系统性风险，got=%+v", got)
	}
}
