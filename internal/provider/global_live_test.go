//go:build live

package provider

import (
	"context"
	"testing"
	"time"
)

// TestLiveGlobalQuotes 真实外盘采集冒烟：东财批量 + 金龙单只 + 新浪 VIX。
// go test -tags live ./internal/provider/ -run TestLiveGlobalQuotes -v
func TestLiveGlobalQuotes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr := NewManager(3)
	quotes, err := mgr.GlobalQuotes(ctx)
	if err != nil {
		t.Fatalf("外盘采集失败: %v", err)
	}
	if len(quotes) < 4 {
		t.Fatalf("外盘因子数量不足: got=%d", len(quotes))
	}
	seen := map[string]bool{}
	for _, q := range quotes {
		seen[q.Symbol] = true
		t.Logf("%-8s %-12s cat=%-10s price=%v chg=%v src=%s ts=%s",
			q.Symbol, q.Name, q.Category, deref(q.Price), deref(q.ChangePct), q.Source, q.ProviderTimestamp)
	}
	for _, core := range []string{"CN00Y", "SPX", "NDX"} {
		if !seen[core] {
			t.Errorf("核心因子缺失: %s", core)
		}
	}
}
