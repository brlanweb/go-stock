package store

import "testing"

// 错误分类是自动重试的安全边界：判错会导致要么数据永久停更，要么每日空撞上游。
// 用例直接取自 2026-08 生产库 sync_checkpoint.last_error 的真实样本。
func TestIsTransientSyncErrorClassifiesRealFailures(t *testing.T) {
	transient := []string{
		// eastmoney 网络超时（生产 5 例）
		`eastmoney kline: Get "https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=0.300862&klt=101": context deadline exceeded`,
		`eastmoney kline: Get "https://push2his.eastmoney.com/api/qt/stock/kline/get?secid=1.600984": net/http: TLS handshake timeout`,
		// 腾讯上游自身后端故障（生产 1 例）：与本地无关，换时间重试即可
		`tencent kline 接口错误: code=11 msg=mysql connect failed, host:30.184.138.60,port:40126`,
		"read tcp 10.0.0.1:443: connection reset by peer",
		"dial tcp: lookup push2his.eastmoney.com: no such host",
		"unexpected EOF",
		"HTTP 503 Service Unavailable",
		"too many requests",
	}
	for _, msg := range transient {
		if !IsTransientSyncError(msg) {
			t.Errorf("应判定为可重试的临时故障，实际不是:\n  %s", msg)
		}
	}

	permanent := []string{
		// 上游明确无该标的数据（生产 4 例：*ST奥维/德邦股份/*ST精伦/*ST万方）
		"akshare kline: No value to decode",
		// 定向可转债：上游返回结构里没有日期列（生产 3 例）
		"akshare kline: 'date'",
		"provider not supported for this security",
		"",
	}
	for _, msg := range permanent {
		if IsTransientSyncError(msg) {
			t.Errorf("应判定为不可重试，实际被当成临时故障:\n  %s", msg)
		}
	}
}

// 无数据签名优先于临时故障签名：某些无数据响应也会带 http 字样，
// 若顺序反了会把结构性问题误判成可重试，导致每日空撞上游。
func TestNoDataSignatureTakesPrecedenceOverTransient(t *testing.T) {
	msg := `akshare kline: Get "https://api.example.com/k": No value to decode`
	if IsTransientSyncError(msg) {
		t.Fatal("同时含 http 与无数据签名时，必须判定为不可重试")
	}
}
