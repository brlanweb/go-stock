package store

import (
	"strings"
	"testing"
)

func gateIndex(name string, close, ma20, mom5 float64) MarketGateIndexFact {
	return MarketGateIndexFact{
		Symbol: name, Name: name, Close: close, MA20: ma20,
		Momentum5Pct: mom5, HasMA20: true, HasMomentum: true,
	}
}

func TestClassifyMarketGateRedOnIndexBreakdown(t *testing.T) {
	// 3/4 指数收于 MA20 下方且 5 日动量为负 → 红灯：推荐照常生成并附强警示，持仓进入防御模式。
	indices := []MarketGateIndexFact{
		gateIndex("上证指数", 3000, 3100, -2.5),
		gateIndex("深证成指", 9500, 9800, -3.0),
		gateIndex("创业板指", 1900, 2000, -4.0),
		gateIndex("沪深300", 3600, 3550, 0.5),
	}
	breadth := MarketGateBreadthFact{Valid: true, StockCount: 5000, UpRatio: 0.40, AvgChangePct: -0.8}
	gate := ClassifyMarketGate("2026-08-18", indices, breadth)
	if gate.Level != MarketGateRed {
		t.Fatalf("expected red, got %s (%s)", gate.Level, gate.Reason)
	}
	if !strings.Contains(gate.Reason, "MA20下方") {
		t.Fatalf("reason should explain index breakdown: %s", gate.Reason)
	}
	if gate.AllowAutoEntry() {
		t.Fatal("red gate must not allow auto entry")
	}
}

func TestClassifyMarketGateRedOnBroadDecline(t *testing.T) {
	// 指数尚未全破位，但全市场普跌（上涨占比 30%、平均跌 1.5%）→ 红灯。
	indices := []MarketGateIndexFact{
		gateIndex("上证指数", 3100, 3050, 0.5),
		gateIndex("深证成指", 9900, 9800, 0.3),
	}
	breadth := MarketGateBreadthFact{Valid: true, StockCount: 5000, UpRatio: 0.30, AvgChangePct: -1.5}
	gate := ClassifyMarketGate("2026-08-18", indices, breadth)
	if gate.Level != MarketGateRed {
		t.Fatalf("expected red on broad decline, got %s (%s)", gate.Level, gate.Reason)
	}
}

func TestClassifyMarketGateYellowOnPartialWeakness(t *testing.T) {
	// 2/4 指数破位但动量未共振为负 → 黄灯：仍推荐但不自动建仓。
	indices := []MarketGateIndexFact{
		gateIndex("上证指数", 3000, 3100, -1.0),
		gateIndex("深证成指", 9500, 9800, -1.5),
		gateIndex("创业板指", 2100, 2000, 3.0),
		gateIndex("沪深300", 3600, 3550, 1.5),
	}
	breadth := MarketGateBreadthFact{Valid: true, StockCount: 5000, UpRatio: 0.55, AvgChangePct: 0.2}
	gate := ClassifyMarketGate("2026-08-18", indices, breadth)
	if gate.Level != MarketGateYellow {
		t.Fatalf("expected yellow, got %s (%s)", gate.Level, gate.Reason)
	}
	if gate.AllowAutoEntry() {
		t.Fatal("yellow gate must not allow auto entry")
	}
}

func TestClassifyMarketGateYellowOnWeakBreadth(t *testing.T) {
	// 指数全部健康但宽度偏弱（上涨占比 40%）→ 黄灯：题材退潮特征。
	indices := []MarketGateIndexFact{
		gateIndex("上证指数", 3150, 3100, 1.0),
		gateIndex("深证成指", 9900, 9800, 1.2),
		gateIndex("创业板指", 2100, 2000, 2.0),
	}
	breadth := MarketGateBreadthFact{Valid: true, StockCount: 5000, UpRatio: 0.40, AvgChangePct: -0.2}
	gate := ClassifyMarketGate("2026-08-18", indices, breadth)
	if gate.Level != MarketGateYellow {
		t.Fatalf("expected yellow on weak breadth, got %s (%s)", gate.Level, gate.Reason)
	}
}

func TestClassifyMarketGateYellowOnNegativeMomentum(t *testing.T) {
	// 指数均在 MA20 上方但 5 日动量整体为负 → 黄灯。
	indices := []MarketGateIndexFact{
		gateIndex("上证指数", 3150, 3100, -1.0),
		gateIndex("深证成指", 9900, 9800, -0.5),
	}
	breadth := MarketGateBreadthFact{Valid: true, StockCount: 5000, UpRatio: 0.55, AvgChangePct: 0.3}
	gate := ClassifyMarketGate("2026-08-18", indices, breadth)
	if gate.Level != MarketGateYellow {
		t.Fatalf("expected yellow on negative momentum, got %s (%s)", gate.Level, gate.Reason)
	}
}

func TestClassifyMarketGateYellowOnMissingData(t *testing.T) {
	// 指数与宽度数据均缺失 → 保守黄灯，绝不能默认放行。
	gate := ClassifyMarketGate("2026-08-18", nil, MarketGateBreadthFact{})
	if gate.Level != MarketGateYellow {
		t.Fatalf("expected yellow on missing data, got %s (%s)", gate.Level, gate.Reason)
	}
}

func TestClassifyMarketGateGreenOnHealthyMarket(t *testing.T) {
	indices := []MarketGateIndexFact{
		gateIndex("上证指数", 3150, 3100, 1.0),
		gateIndex("深证成指", 9900, 9800, 1.5),
		gateIndex("创业板指", 2100, 2000, 2.0),
		gateIndex("沪深300", 3650, 3550, 1.2),
	}
	breadth := MarketGateBreadthFact{Valid: true, StockCount: 5000, UpRatio: 0.60, AvgChangePct: 0.5}
	gate := ClassifyMarketGate("2026-08-18", indices, breadth)
	if gate.Level != MarketGateGreen {
		t.Fatalf("expected green, got %s (%s)", gate.Level, gate.Reason)
	}
	if !gate.AllowAutoEntry() {
		t.Fatal("green gate must allow auto entry")
	}
}

func TestClassifyMarketGateIgnoresIndicesWithoutMA20(t *testing.T) {
	// 历史不足的指数不参与投票：唯一有效指数健康 + 宽度健康 → green。
	indices := []MarketGateIndexFact{
		{Symbol: "SZ399006", Name: "创业板指", Close: 1900, HasMA20: false},
		gateIndex("上证指数", 3150, 3100, 1.0),
	}
	breadth := MarketGateBreadthFact{Valid: true, StockCount: 5000, UpRatio: 0.60, AvgChangePct: 0.5}
	gate := ClassifyMarketGate("2026-08-18", indices, breadth)
	if gate.Level != MarketGateGreen {
		t.Fatalf("expected green when broken index lacks history, got %s (%s)", gate.Level, gate.Reason)
	}
}

func TestCalculateMarketSentimentExtremeGreed(t *testing.T) {
	market := &MarketGate{
		Indices: []MarketGateIndexFact{
			gateIndex("上证指数", 3300, 3100, 4),
			gateIndex("深证成指", 10500, 9800, 4),
		},
		Breadth: MarketGateBreadthFact{Valid: true, UpRatio: 0.80, AvgChangePct: 1.5},
	}
	global := &GlobalRiskGate{Signals: []GlobalRiskSignal{
		{Factor: "a50", HasData: true, Score: 0},
		{Factor: "vix", HasData: true, Price: 15},
	}}

	got := CalculateMarketSentiment(market, global)
	if got.Score != 88 || got.Label != "极度贪婪" {
		t.Fatalf("强动量、强宽度与低波动应为极度贪婪 88，got=%d %s", got.Score, got.Label)
	}
}

func TestCalculateMarketSentimentExtremeFear(t *testing.T) {
	market := &MarketGate{
		Indices: []MarketGateIndexFact{
			gateIndex("上证指数", 2900, 3100, -4),
			gateIndex("深证成指", 9000, 9800, -4),
		},
		Breadth: MarketGateBreadthFact{Valid: true, UpRatio: 0.20, AvgChangePct: -1.5},
	}
	global := &GlobalRiskGate{Signals: []GlobalRiskSignal{
		{Factor: "a50", HasData: true, Score: -3},
		{Factor: "china_adr", HasData: true, Score: -1},
		{Factor: "us_equity", HasData: true, Score: -2},
		{Factor: "vix", HasData: true, Price: 35, Score: -2},
	}}

	got := CalculateMarketSentiment(market, global)
	if got.Score != 11 || got.Label != "极度恐惧" {
		t.Fatalf("弱动量、弱宽度与高波动应为极度恐惧 11，got=%d %s", got.Score, got.Label)
	}
}

func TestCalculateMarketSentimentNeutralWhenDataMissing(t *testing.T) {
	got := CalculateMarketSentiment(nil, nil)
	if got.Score != 50 || got.Label != "中性" {
		t.Fatalf("数据全缺失时应返回中性 50，got=%d %s", got.Score, got.Label)
	}
}

func TestCalculateMarketSentimentDoesNotCountVIXTwice(t *testing.T) {
	global := &GlobalRiskGate{Score: -1, Signals: []GlobalRiskSignal{{Factor: "vix", HasData: true, Price: 26, Score: -1}}}
	got := CalculateMarketSentiment(nil, global)
	if got.GlobalAppetite != 50 {
		t.Fatalf("只有VIX时外盘风险偏好应保持中性，避免VIX重复计权，got=%.2f", got.GlobalAppetite)
	}
}
