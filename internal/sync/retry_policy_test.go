package sync

import (
	"testing"

	"github.com/hoax/go-stock/internal/store"
)

// 自动重试必须同时受「错误类型」与「次数上限」两道约束。
// 只要有一道失效，就会退回到 2026-08 之前的两种坏状态之一：
// 网络抖动导致数据永久停更，或结构性失败每天空撞上游。
func TestTransientRequeueRequiresBothErrorTypeAndRetryBudget(t *testing.T) {
	cases := []struct {
		name    string
		item    store.FailedCheckpoint
		requeue bool
	}{
		{"网络故障且未达上限，应重排",
			store.FailedCheckpoint{Symbol: "SZ300862", RetryCount: 1, LastError: `eastmoney kline: Get "https://push2his.eastmoney.com/x": i/o timeout`}, true},
		{"网络故障但已达上限，不再自动重排",
			store.FailedCheckpoint{Symbol: "SZ300862", RetryCount: maxTransientRetry, LastError: "i/o timeout"}, false},
		{"上游无数据，任何次数都不重排",
			store.FailedCheckpoint{Symbol: "SZ002231", RetryCount: 0, LastError: "akshare kline: No value to decode"}, false},
		{"定向转债结构性失败，不重排",
			store.FailedCheckpoint{Symbol: "SZ810011", RetryCount: 0, LastError: "akshare kline: 'date'"}, false},
	}
	for _, c := range cases {
		got := c.item.RetryCount < maxTransientRetry && store.IsTransientSyncError(c.item.LastError)
		if got != c.requeue {
			t.Errorf("%s：期望重排=%v，实际=%v（retry=%d err=%q）",
				c.name, c.requeue, got, c.item.RetryCount, c.item.LastError)
		}
	}
}

// 退市阈值按交易日计。10 个交易日约两周，长假不会把正常停牌误判为退市；
// 同时远快于原先 180 自然日兜底。
func TestDelistStaleThresholdIsTradingDayBased(t *testing.T) {
	if delistStaleTradingDays <= 0 {
		t.Fatal("退市阈值必须为正")
	}
	if delistStaleTradingDays > 20 {
		t.Fatalf("阈值 %d 个交易日过宽，失去快速收敛意义", delistStaleTradingDays)
	}
	if maxTransientRetry < 1 {
		t.Fatal("重试上限至少为 1，否则临时故障无法自愈")
	}
}
