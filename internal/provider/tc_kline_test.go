//go:build live

package provider

import (
	"context"
	"testing"
	"time"
)

func TestLiveTencentKline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tc := NewTencent()
	klines, err := tc.DailyKlines(ctx, "SH600519", "20240101", "")
	if err != nil {
		t.Fatalf("腾讯K线失败: %v", err)
	}
	if len(klines) < 100 {
		t.Fatalf("K线数量异常: %d", len(klines))
	}
	f, l := klines[0], klines[len(klines)-1]
	t.Logf("腾讯K线 %d 根: %s(c=%.2f adj=%.4f) ~ %s(c=%.2f adj=%.4f)", len(klines), f.Date, f.Close, f.AdjFactor, l.Date, l.Close, l.AdjFactor)
}
