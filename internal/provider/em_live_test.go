//go:build live

package provider

import (
	"context"
	"testing"
	"time"
)

func TestLiveEastmoneyDirect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	em := NewEastmoney(3)
	q, err := em.Quote(ctx, "SH600519")
	if err != nil {
		t.Fatalf("东财失败: %v", err)
	}
	t.Logf("东财: %s price=%v pe=%v pb=%v 52wH=%v 52wL=%v 量比=%v bids=%d ts=%s",
		q.Name, deref(q.Price), deref(q.PERatio), deref(q.PBRatio), deref(q.High52W), deref(q.Low52W), deref(q.VolumeRatio), len(q.Bids), q.ProviderTimestamp)
}
