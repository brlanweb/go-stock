package analysis

import (
	"testing"

	"github.com/hoax/go-stock/internal/store"
)

// 2026-09 复盘回归测试：推荐数量上限必须随风向档位收紧，且任何档位都允许空仓。
//
// 背景：旧链路硬编码「必须恰好选 3 只」，风向红灯只注入提示词、不改变行为。
// 2026-09-02 全市场 5401 家下跌 / 1588 家上涨（平均 -0.81%），系统仍被迫
// 推出 3 只，且三条理由中均写明「指数红灯，系统性风险高」。
func TestRecommendationMaxPicksTightensWithGate(t *testing.T) {
	cases := []struct {
		level string
		want  int
	}{
		{store.MarketGateGreen, 3},
		{store.MarketGateYellow, 2},
		{store.MarketGateRed, 1},
		{"", 3}, // 未知档位按绿灯放行，保持既有行为
	}
	for _, tc := range cases {
		if got := recommendationMaxPicks(tc.level); got != tc.want {
			t.Fatalf("gate=%q max_picks: got=%d want=%d", tc.level, got, tc.want)
		}
	}
}

// 空仓权：上限只约束「最多」，0 只在任何档位下都必须是合法输出。
// 这是本次改动的核心——系统必须有权说「今天不买」。
func TestRecommendationMaxPicksAlwaysAllowsEmpty(t *testing.T) {
	for _, level := range []string{store.MarketGateGreen, store.MarketGateYellow, store.MarketGateRed} {
		if limit := recommendationMaxPicks(level); limit < 1 {
			t.Fatalf("gate=%s 上限必须 >=1 以便至少可推一只: got=%d", level, limit)
		}
		// 0 <= limit 恒成立，空列表永远不会触发「超出上限」校验。
		if 0 > recommendationMaxPicks(level) {
			t.Fatalf("gate=%s 空仓必须合法", level)
		}
	}
}
