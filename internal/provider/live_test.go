//go:build live

// 真实上游联调测试：go test -tags live ./internal/provider/ -run TestLive -v
package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLiveQuoteFallbackChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr := NewManager(3)

	q, err := mgr.Quote(ctx, "SH600519")
	if err != nil {
		t.Fatalf("Quote 失败: %v", err)
	}
	b, _ := json.MarshalIndent(q, "", "  ")
	t.Logf("单只行情(%s):\n%s", q.Source, b)
	if q.Price == nil || *q.Price <= 0 {
		t.Fatal("价格无效")
	}
}

func TestLiveBatchQuotes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr := NewManager(3)

	quotes, err := mgr.BatchQuotes(ctx, []string{"SH600519", "SZ000001", "SH510300"})
	if err != nil {
		t.Fatalf("BatchQuotes 失败: %v", err)
	}
	for _, q := range quotes {
		t.Logf("%s %s price=%v chg=%v src=%s", q.Symbol, q.Name, deref(q.Price), deref(q.ChangePct), q.Source)
	}
	if len(quotes) < 3 {
		t.Fatalf("期望 3 只，实际 %d", len(quotes))
	}
}

func TestLiveTencentDirect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tc := NewTencent()
	q, err := tc.Quote(ctx, "SH600519")
	if err != nil {
		t.Fatalf("腾讯源失败: %v", err)
	}
	t.Logf("腾讯: %s price=%v 量比=%v 换手=%v PE=%v 总市值=%v 盘口买%d档",
		q.Name, deref(q.Price), deref(q.VolumeRatio), deref(q.TurnoverRate), deref(q.PERatio), deref(q.TotalMV), len(q.Bids))
}

func TestLiveSinaDirect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sn := NewSina()
	q, err := sn.Quote(ctx, "SH600519")
	if err != nil {
		t.Fatalf("新浪源失败: %v", err)
	}
	t.Logf("新浪: %s price=%v chg=%v%% vol=%v", q.Name, deref(q.Price), deref(q.ChangePct), deref(q.Volume))
}

func TestLiveIndices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mgr := NewManager(3)
	idx, err := mgr.Indices(ctx)
	if err != nil {
		t.Fatalf("指数失败: %v", err)
	}
	for _, i := range idx {
		t.Logf("%s %s price=%v chg=%v%%", i.Symbol, i.Name, deref(i.Price), deref(i.ChangePct))
	}
}

func TestLiveKlineWithAdjFactor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	em := NewEastmoney(3)
	klines, err := em.DailyKlines(ctx, "SH600519", "20240101", "")
	if err != nil {
		t.Fatalf("K线失败: %v", err)
	}
	if len(klines) < 100 {
		t.Fatalf("K线数量异常: %d", len(klines))
	}
	first, last := klines[0], klines[len(klines)-1]
	t.Logf("K线 %d 根: %s(close=%.2f adj=%.4f) ~ %s(close=%.2f adj=%.4f)",
		len(klines), first.Date, first.Close, first.AdjFactor, last.Date, last.Close, last.AdjFactor)
}

func TestLiveTimeshare(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	em := NewEastmoney(3)
	pts, err := em.Timeshare(ctx, "SH600519")
	if err != nil {
		t.Fatalf("分时失败: %v", err)
	}
	if len(pts) == 0 {
		t.Fatal("分时为空")
	}
	t.Logf("分时 %d 点，首: %+v，末: %+v", len(pts), pts[0], pts[len(pts)-1])
}

func deref[T any](p *T) interface{} {
	if p == nil {
		return nil
	}
	return *p
}
