package store

import (
	"math"
	"testing"
)

func TestScorecardSampleConfidence(t *testing.T) {
	cases := []struct {
		samples int
		want    float64
	}{{0, 0}, {30, 1}, {120, 1}}
	for _, tc := range cases {
		if got := sampleConfidence(tc.samples); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("samples=%d confidence=%f want=%f", tc.samples, got, tc.want)
		}
	}
	if got := sampleConfidence(3); math.Abs(got-math.Sqrt(0.1)) > 1e-9 {
		t.Fatalf("小样本应按 sqrt(n/30) 衰减，got=%f", got)
	}
}

func TestCalculateOverallScoreUsesRiskAdjustedMetrics(t *testing.T) {
	trades := []scoredTrade{
		{position: ScorecardPosition{HoldDays: 2}, netPct: 10},
		{position: ScorecardPosition{HoldDays: 4}, netPct: -5},
		{position: ScorecardPosition{HoldDays: 3}, netPct: 2},
	}
	out := calculateOverallScore(trades, nil)
	if out.Samples != 3 || out.Wins != 2 || out.Losses != 1 {
		t.Fatalf("样本/胜负统计错误: %+v", out)
	}
	if out.WinRate == nil || math.Abs(*out.WinRate-66.67) > 0.01 {
		t.Fatalf("胜率错误: %+v", out.WinRate)
	}
	if out.ProfitFactor == nil || math.Abs(*out.ProfitFactor-2.4) > 0.01 {
		t.Fatalf("ProfitFactor 应为 (10+2)/5=2.4: %+v", out.ProfitFactor)
	}
	if out.TotalReturnPct == nil || out.MaxDrawdownPct == nil || *out.MaxDrawdownPct <= 0 {
		t.Fatalf("必须生成复合收益和最大回撤: %+v", out)
	}
}

func TestSelectionScoreShrinksToNeutralForTinySample(t *testing.T) {
	one := calculateSelectionStage([]MechanicalOutcome{{NetPct: 10}})
	manyItems := make([]MechanicalOutcome, 30)
	for i := range manyItems {
		manyItems[i].NetPct = 10
	}
	many := calculateSelectionStage(manyItems)
	if one.Score <= 50 || one.Score >= many.Score {
		t.Fatalf("小样本高收益应向中性分收缩: one=%.2f many=%.2f", one.Score, many.Score)
	}
	if many.Score != 100 {
		t.Fatalf("30个一致正样本达到满置信度后应封顶100，got=%.2f", many.Score)
	}
}

func TestOpportunityScoreRewardsUsefulFiltering(t *testing.T) {
	good := calculateOpportunityStage(30, 30, []float64{5, 4, 6}, []float64{-2, -1, 0}, ScorecardEntryAdviceStats{})
	bad := calculateOpportunityStage(30, 30, []float64{-2, -1, 0}, []float64{5, 4, 6}, ScorecardEntryAdviceStats{})
	if good.Score <= 50 || bad.Score >= 50 || good.Score <= bad.Score {
		t.Fatalf("已建仓组优于放弃组时应加分，反之扣分: good=%.2f bad=%.2f", good.Score, bad.Score)
	}
}

func TestSettlementEquityGroupsSameDayTrades(t *testing.T) {
	trades := []scoredTrade{
		{position: ScorecardPosition{ExitDate: "2026-01-02"}, netPct: 10},
		{position: ScorecardPosition{ExitDate: "2026-01-02"}, netPct: -10},
		{position: ScorecardPosition{ExitDate: "2026-01-03"}, netPct: 5},
	}
	curve := buildSettlementEquity(trades)
	if len(curve) != 2 || curve[0].Trades != 2 || curve[0].Equity != 1 || curve[1].Equity != 1.05 {
		t.Fatalf("同日交易应先等权聚合再复合: %+v", curve)
	}
}

func TestPerformanceScorePenalizesDrawdown(t *testing.T) {
	avg, pf, lowDD, highDD := 2.0, 1.8, 3.0, 20.0
	good := scorePerformance(ScorecardOverall{Samples: 30, AvgNetPct: &avg, ProfitFactor: &pf, MaxDrawdownPct: &lowDD})
	bad := scorePerformance(ScorecardOverall{Samples: 30, AvgNetPct: &avg, ProfitFactor: &pf, MaxDrawdownPct: &highDD})
	if good <= bad {
		t.Fatalf("同收益下高回撤必须低分: lowDD=%.2f highDD=%.2f", good, bad)
	}
}

func TestStrategyParamEvaluationGuards(t *testing.T) {
	if strategyEvaluationReady(StrategyEvaluationMinSamples - 1) {
		t.Fatal("新增结算样本不足时不得验收参数")
	}
	if !strategyEvaluationReady(StrategyEvaluationMinSamples) {
		t.Fatal("达到新增样本门槛后应允许验收参数")
	}
	if strategyShouldRollback(98, 100) {
		t.Fatal("总分恰好下降阈值时应保留参数")
	}
	if !strategyShouldRollback(97.99, 100) {
		t.Fatal("总分下降超过阈值时必须回滚参数")
	}
}
