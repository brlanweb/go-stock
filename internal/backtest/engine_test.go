package backtest

import (
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

func TestRunMAGoldenCrossUsesNextBar(t *testing.T) {
	klines := make([]model.Kline, 40)
	for i := range klines {
		price := 10.0
		if i >= 25 {
			price = 10 + float64(i-24)*0.4
		}
		klines[i] = model.Kline{Symbol: "SH600000", Date: dateAt(i), Open: price, High: price * 1.01, Low: price * 0.99, Close: price, Volume: 100000, AdjFactor: 1}
	}
	result, err := Run(Request{Symbol: "SH600000", IndicatorID: "ma_golden_cross", Period: "day", InitialCash: 100000, Params: map[string]any{"fast": 5.0, "slow": 10.0}}, klines)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Signals) == 0 {
		t.Fatal("expected trade signals")
	}
	if result.Signals[0].Date == klines[25].Date {
		t.Fatal("signal must execute on the next bar")
	}
	if result.TradeCount == 0 {
		t.Fatal("expected completed trade")
	}
}

func TestUnsupportedStrategy(t *testing.T) {
	klines := make([]model.Kline, 40)
	for i := range klines {
		klines[i] = model.Kline{Date: dateAt(i), Open: 10, High: 11, Low: 9, Close: 10, Volume: 100}
	}
	_, err := Run(Request{IndicatorID: "wave_theory"}, klines)
	if err == nil {
		t.Fatal("expected unsupported strategy error")
	}
}

func dateAt(i int) string {
	day := i + 1
	if day <= 31 {
		return "2026-01-" + two(day)
	}
	return "2026-02-" + two(day-31)
}
func two(v int) string {
	if v < 10 {
		return "0" + string(rune('0'+v))
	}
	return string([]byte{byte('0' + v/10), byte('0' + v%10)})
}
