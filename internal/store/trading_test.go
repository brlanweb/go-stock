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

func TestHoldingTodayPnlUsesUnrealizedPnlForPositionBoughtToday(t *testing.T) {
	got := holdingTodayPnl("2026-08-27", "2026-08-27", 18_500, 53.95, 52.10, -299.50)
	if got != -299.50 {
		t.Fatalf("当日买入应直接采用已含买入费用的未实现盈亏，got=%.2f", got)
	}
}

func TestHoldingTodayPnlUsesPreviousCloseForOvernightPosition(t *testing.T) {
	got := holdingTodayPnl("2026-08-26", "2026-08-27", 55_400, 18.50, 18.00, -1)
	if got != 27_700 {
		t.Fatalf("隔夜持仓今日盈亏应按当前价减前收价计算，got=%.2f", got)
	}
}

func TestSoldTodayPnlUsesPreviousCloseAndDeductsSellFee(t *testing.T) {
	got := soldTodayPnl(18.67, 18.50, 51_800, 773)
	if got < 8032.99 || got > 8033.01 {
		t.Fatalf("今日卖出应按卖价减前收价并扣卖出费用，got=%.2f", got)
	}
}
