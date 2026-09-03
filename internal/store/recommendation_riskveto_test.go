package store

import (
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

// 2026-09 复盘回归测试：风险分必须重新拥有否决权。
//
// 背景：旧设计把 RiskScore 降级为「仅展示」，明令禁止其参与筛选、排序或否决，
// 导致系统能算出风险却无权规避——2026-09-02 三只推荐 risk 分别为 86/71/57，
// 理由中均写明「指数红灯，系统性风险高」，仍被强制推出。
// 75 天全量复盘显示 risk>=70 分桶是唯一负超额分桶（5 日超额 -0.19%）。
func TestRecommendationProductionRiskVetoThreshold(t *testing.T) {
	if RecommendationProductionMaxRisk != 75.0 {
		t.Fatalf("生产风险否决线被改动: got=%v want=75", RecommendationProductionMaxRisk)
	}
	// 复盘中被强制推出的真实风险分必须落在否决区。
	for _, risk := range []float64{86, 75} {
		if risk < RecommendationProductionMaxRisk {
			t.Fatalf("risk=%v 应当被否决，但低于阈值 %v", risk, RecommendationProductionMaxRisk)
		}
	}
	// 中低风险候选仍应放行，避免闸门过严导致长期无候选。
	for _, risk := range []float64{57, 71, 74.9} {
		if risk >= RecommendationProductionMaxRisk {
			t.Fatalf("risk=%v 不应被否决", risk)
		}
	}
}

// 风险分只做二值闸门，不参与排序打分：否则高风险候选可以靠高趋势分
// 把自己「补回来」，闸门就退化成了软性降权。
func TestRecommendationSortScoreStaysRiskFree(t *testing.T) {
	klines := make([]model.Kline, recommendationKlineDays)
	for i := range klines {
		klines[i] = model.Kline{Close: 10 + float64(i)*0.01}
	}
	low := recommendationCandidateSortScore(80, klines, 1, 5)
	high := recommendationCandidateSortScore(80, klines, 1, 74)
	if low != high {
		t.Fatalf("排序分不得随风险分变化: low_risk=%v high_risk=%v", low, high)
	}
}
