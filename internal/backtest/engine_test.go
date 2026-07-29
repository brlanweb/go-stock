package backtest

import (
	"math"
	"strings"
	"testing"

	"github.com/hoax/go-stock/internal/indicator"
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

func TestExecutableCatalogStrategiesAreImplemented(t *testing.T) {
	klines := oscillatingKlines(180)
	for _, definition := range indicator.Catalog() {
		if definition.Kind != "strategy" || definition.Capability != indicator.Executable {
			continue
		}
		t.Run(definition.ID, func(t *testing.T) {
			_, err := strategySignals(definition.ID, definition.DefaultParams, klines)
			if err != nil && strings.Contains(err.Error(), "不支持") {
				t.Fatalf("catalog strategy is not implemented: %v", err)
			}
		})
	}
}

func TestNewStrategiesGenerateBuySignalWithoutLookahead(t *testing.T) {
	klines := oscillatingKlines(180)
	ids := []string{
		"ema_cross", "macd_cross", "rsi_reversal", "boll_mean_reversion",
		"boll_breakout", "donchian_breakout", "kdj_reversal", "roc_momentum",
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			definition := catalogDefinition(t, id)
			raw, err := strategySignals(id, definition.DefaultParams, klines)
			if err != nil {
				t.Fatal(err)
			}
			buyIndex := -1
			for _, signal := range raw {
				if signal.action == "buy" {
					buyIndex = signal.index
					break
				}
			}
			if buyIndex < 0 {
				t.Fatal("expected at least one buy signal")
			}
			result := simulate(Request{Symbol: "SH600000", IndicatorID: id, Period: "day", InitialCash: 100000, Params: definition.DefaultParams}, klines, raw)
			if len(result.Signals) == 0 {
				t.Fatal("expected executed signals")
			}
			if got, want := result.Signals[0].Date, klines[buyIndex+1].Date; got != want {
				t.Fatalf("signal executed on %s, want next bar %s", got, want)
			}
		})
	}
}

func catalogDefinition(t *testing.T, id string) indicator.Definition {
	t.Helper()
	for _, definition := range indicator.Catalog() {
		if definition.ID == id {
			return definition
		}
	}
	t.Fatalf("missing catalog definition %s", id)
	return indicator.Definition{}
}

func oscillatingKlines(count int) []model.Kline {
	klines := make([]model.Kline, count)
	for i := range klines {
		trend := float64(i) * 0.025
		wave := 2.8 * math.Sin(float64(i)*0.22)
		shock := 0.0
		if i%55 >= 42 && i%55 <= 45 {
			shock = float64(i%55-41) * 1.3
		}
		close := 20 + trend + wave + shock
		open := close - 0.12*math.Sin(float64(i)*0.7)
		klines[i] = model.Kline{
			Symbol: "SH600000", Date: dateAt(i), Open: open,
			High: math.Max(open, close) + 0.45, Low: math.Min(open, close) - 0.45,
			Close: close, Volume: int64(100000 + 50000*(1+math.Sin(float64(i)*0.31))), AdjFactor: 1,
		}
	}
	return klines
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
