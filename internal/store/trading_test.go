package store

import "testing"

func TestRoundLotSharesUsesOneMillionDefaultOrder(t *testing.T) {
	if got := roundLotShares(DefaultTradeAmount, 10.01); got != 99900 {
		t.Fatalf("100万元按10.01元应买99900股，got=%d", got)
	}
	if got := roundLotShares(DefaultTradeAmount, 0); got != 0 {
		t.Fatalf("无效价格不得产生股数，got=%d", got)
	}
}

func TestMinimumCommission(t *testing.T) {
	if got := minimumCommission(1000, BuyCommissionRate); got != 5 {
		t.Fatalf("小额交易佣金应按最低5元，got=%.2f", got)
	}
	if got := minimumCommission(1_000_000, BuyCommissionRate); got != 300 {
		t.Fatalf("100万元买入佣金应为300元，got=%.2f", got)
	}
}
