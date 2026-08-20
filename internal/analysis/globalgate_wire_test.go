package analysis

import (
	"testing"

	"github.com/hoax/go-stock/internal/provider"
)

// 防回归：provider.Manager 必须满足外盘接口，且 SetMarketProvider 注入后
// 风险感知层自动启用。此前该断言逻辑曾在编辑冲突中丢失，导致线上
// RunGlobalGate 永远走「外盘行情源未配置」降级分支。
func TestSetMarketProviderWiresGlobalProvider(t *testing.T) {
	var _ globalQuoteProvider = (*provider.Manager)(nil)

	s := New(nil, Config{})
	s.SetMarketProvider(provider.NewManager(1))
	if s.globalProvider == nil {
		t.Fatal("SetMarketProvider 必须自动接线外盘行情源（globalProvider 不能为 nil）")
	}
}
