package backtest

import (
	"fmt"
	"math"
	"sort"

	"github.com/hoax/go-stock/internal/model"
)

const (
	commissionRate = 0.00025
	commissionMin  = 5.0
	stampTax       = 0.0005
	transferFee    = 0.00001
	slippage       = 0.001
)

type rawSignal struct {
	index  int
	action string
	reason string
}

func Run(req Request, klines []model.Kline) (*Result, error) {
	if req.InitialCash <= 0 {
		req.InitialCash = 100000
	}
	if req.Period == "" {
		req.Period = "day"
	}
	if len(klines) < 30 {
		return nil, fmt.Errorf("本地K线不足，至少需要30个交易日")
	}
	signals, err := strategySignals(req.IndicatorID, req.Params, klines)
	if err != nil {
		return nil, err
	}
	result := simulate(req, klines, signals)
	return result, nil
}

func strategySignals(id string, params map[string]any, k []model.Kline) ([]rawSignal, error) {
	closes, volumes := series(k)
	ma5, ma10, ma20 := sma(closes, 5), sma(closes, 10), sma(closes, 20)
	vol5 := sma(volumes, 5)
	var out []rawSignal
	add := func(i int, action, reason string) {
		if i >= 0 && i < len(k)-1 {
			out = append(out, rawSignal{index: i, action: action, reason: reason})
		}
	}
	switch id {
	case "ma_golden_cross":
		fast, slow := intParam(params, "fast", 5), intParam(params, "slow", 10)
		f, s := sma(closes, fast), sma(closes, slow)
		for i := slow; i < len(k); i++ {
			if crossedUp(f, s, i) {
				add(i, "buy", fmt.Sprintf("MA%d 上穿 MA%d", fast, slow))
			} else if crossedDown(f, s, i) {
				add(i, "sell", fmt.Sprintf("MA%d 下穿 MA%d", fast, slow))
			}
		}
	case "volume_breakout":
		period := intParam(params, "period", 20)
		ratio := floatParam(params, "volume_ratio", 2)
		for i := period; i < len(k); i++ {
			high := k[i-period].High
			for j := i - period + 1; j < i; j++ {
				if k[j].High > high {
					high = k[j].High
				}
			}
			if k[i].Close > high && vol5[i] > 0 && float64(k[i].Volume) >= vol5[i]*ratio {
				add(i, "buy", "放量突破近20日阻力")
			}
			if !math.IsNaN(ma10[i]) && k[i].Close < ma10[i] {
				add(i, "sell", "跌破MA10")
			}
		}
	case "shrink_pullback":
		ratio := floatParam(params, "volume_ratio", 0.7)
		for i := 20; i < len(k); i++ {
			upTrend := ma5[i] > ma10[i] && ma10[i] > ma20[i]
			nearMA := math.Abs(k[i].Close-ma10[i])/ma10[i] <= 0.02
			if upTrend && nearMA && vol5[i] > 0 && float64(k[i].Volume) < vol5[i]*ratio && k[i].Close >= k[i].Open {
				add(i, "buy", "多头趋势缩量回踩MA10")
			}
			if k[i].Close < ma20[i] {
				add(i, "sell", "跌破MA20")
			}
		}
	case "bottom_volume":
		for i := 20; i < len(k); i++ {
			high := k[i-20].High
			for j := i - 19; j < i; j++ {
				if k[j].High > high {
					high = k[j].High
				}
			}
			if high > 0 && (high-k[i].Low)/high >= 0.15 && vol5[i] > 0 && float64(k[i].Volume) >= vol5[i]*3 && k[i].Close > k[i].Open {
				add(i, "buy", "20日回撤后底部放量阳线")
			}
			if !math.IsNaN(ma10[i]) && k[i].Close < ma10[i] {
				add(i, "sell", "反转失败跌破MA10")
			}
		}
	case "one_yang_three_yin":
		for i := 4; i < len(k); i++ {
			first, last := k[i-4], k[i]
			valid := first.Close > first.Open*1.02 && last.Close > last.Open && last.Close > first.Close
			for j := i - 3; j <= i-1 && valid; j++ {
				valid = k[j].Low >= first.Open && k[j].Close <= first.Close && k[j].Close >= first.Open
			}
			if valid {
				add(i, "buy", "一阳夹三阴整理后突破")
			}
			if !math.IsNaN(ma10[i]) && k[i].Close < ma10[i] {
				add(i, "sell", "跌破MA10")
			}
		}
	case "box_oscillation":
		for i := 20; i < len(k); i++ {
			lo, hi := k[i-20].Low, k[i-20].High
			for j := i - 19; j < i; j++ {
				if k[j].Low < lo {
					lo = k[j].Low
				}
				if k[j].High > hi {
					hi = k[j].High
				}
			}
			if hi <= lo {
				continue
			}
			pos := (k[i].Close - lo) / (hi - lo)
			if pos <= 0.15 {
				add(i, "buy", "价格进入20日箱体支撑区")
			} else if pos >= 0.85 {
				add(i, "sell", "价格进入20日箱体压力区")
			}
		}
	case "bull_trend":
		for i := 20; i < len(k); i++ {
			bull := ma5[i] > ma10[i] && ma10[i] > ma20[i] && k[i].Close > ma5[i]
			prevBull := ma5[i-1] > ma10[i-1] && ma10[i-1] > ma20[i-1] && k[i-1].Close > ma5[i-1]
			if bull && !prevBull {
				add(i, "buy", "MA5>MA10>MA20 多头排列成立")
			}
			if !math.IsNaN(ma20[i]) && k[i].Close < ma20[i] {
				add(i, "sell", "跌破MA20，多头结构失效")
			}
		}
	default:
		return nil, fmt.Errorf("该指标当前不支持纯K线确定性回测")
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].index < out[j].index })
	return out, nil
}

func simulate(req Request, k []model.Kline, raw []rawSignal) *Result {
	cash, shares := req.InitialCash, int64(0)
	var entryDate, entryReason string
	var entryPrice, entryCost float64
	var trades []Trade
	var signals []Signal
	byIndex := map[int][]rawSignal{}
	for _, sig := range raw {
		byIndex[sig.index] = append(byIndex[sig.index], sig)
	}
	curve := make([]float64, len(k))
	for i, bar := range k {
		if i > 0 {
			for _, sig := range byIndex[i-1] {
				execPrice := bar.Open
				if sig.action == "buy" && shares == 0 && execPrice > 0 {
					execPrice *= 1 + slippage
					qty := int64((cash/(execPrice*(1+commissionRate+transferFee)))/100) * 100
					if qty > 0 {
						fee := buyFee(float64(qty) * execPrice)
						cash -= float64(qty)*execPrice + fee
						shares, entryDate, entryPrice, entryCost, entryReason = qty, bar.Date, execPrice, fee, sig.reason
						signals = append(signals, Signal{Date: bar.Date, Action: "buy", Price: execPrice, Reason: sig.reason + "；下一交易日开盘执行"})
					}
				} else if sig.action == "sell" && shares > 0 {
					execPrice *= 1 - slippage
					fee := sellFee(float64(shares) * execPrice)
					proceeds := float64(shares)*execPrice - fee
					cost := float64(shares)*entryPrice + entryCost
					pnl := proceeds - cost
					trades = append(trades, Trade{EntryDate: entryDate, EntryPrice: entryPrice, ExitDate: bar.Date, ExitPrice: execPrice, Shares: shares, PnL: pnl, ReturnPct: pnl / cost * 100, EntryReason: entryReason, ExitReason: sig.reason})
					cash += proceeds
					signals = append(signals, Signal{Date: bar.Date, Action: "sell", Price: execPrice, Reason: fmt.Sprintf("%s；单笔收益 %.2f%%", sig.reason, pnl/cost*100)})
					shares = 0
				}
			}
		}
		curve[i] = cash + float64(shares)*bar.Close
	}
	if shares > 0 {
		bar := k[len(k)-1]
		execPrice := bar.Close * (1 - slippage)
		fee := sellFee(float64(shares) * execPrice)
		proceeds, cost := float64(shares)*execPrice-fee, float64(shares)*entryPrice+entryCost
		pnl := proceeds - cost
		trades = append(trades, Trade{EntryDate: entryDate, EntryPrice: entryPrice, ExitDate: bar.Date, ExitPrice: execPrice, Shares: shares, PnL: pnl, ReturnPct: pnl / cost * 100, EntryReason: entryReason, ExitReason: "回测期末平仓"})
		cash += proceeds
		signals = append(signals, Signal{Date: bar.Date, Action: "sell", Price: execPrice, Reason: fmt.Sprintf("回测期末平仓；单笔收益 %.2f%%", pnl/cost*100)})
		curve[len(curve)-1] = cash
	}
	result := &Result{Symbol: req.Symbol, IndicatorID: req.IndicatorID, Period: req.Period, Start: k[0].Date, End: k[len(k)-1].Date, InitialCash: req.InitialCash, FinalEquity: cash, Params: req.Params, Trades: trades, Signals: signals, TradeCount: len(trades)}
	result.TotalReturn = cash/req.InitialCash - 1
	years := float64(len(k)) / 252
	if years > 0 && cash > 0 {
		result.AnnualReturn = math.Pow(cash/req.InitialCash, 1/years) - 1
	}
	result.MaxDrawdown = maxDrawdown(curve)
	result.SharpeRatio = sharpe(curve)
	result.WinRate, result.ProfitLossRatio, result.ProfitFactor = tradeMetrics(trades)
	return result
}

func series(k []model.Kline) ([]float64, []float64) {
	c, v := make([]float64, len(k)), make([]float64, len(k))
	for i := range k {
		c[i] = k[i].Close
		v[i] = float64(k[i].Volume)
	}
	return c, v
}
func sma(v []float64, period int) []float64 {
	out := make([]float64, len(v))
	for i := range out {
		out[i] = math.NaN()
	}
	var sum float64
	for i, x := range v {
		sum += x
		if i >= period {
			sum -= v[i-period]
		}
		if i+1 >= period {
			out[i] = sum / float64(period)
		}
	}
	return out
}
func crossedUp(a, b []float64, i int) bool {
	return i > 0 && !math.IsNaN(a[i]) && !math.IsNaN(b[i]) && a[i-1] <= b[i-1] && a[i] > b[i]
}
func crossedDown(a, b []float64, i int) bool {
	return i > 0 && !math.IsNaN(a[i]) && !math.IsNaN(b[i]) && a[i-1] >= b[i-1] && a[i] < b[i]
}
func intParam(p map[string]any, key string, d int) int {
	if v, ok := p[key].(float64); ok && v > 0 {
		return int(v)
	}
	if v, ok := p[key].(int); ok && v > 0 {
		return v
	}
	return d
}
func floatParam(p map[string]any, key string, d float64) float64 {
	if v, ok := p[key].(float64); ok && v > 0 {
		return v
	}
	return d
}
func buyFee(n float64) float64 { return math.Max(n*commissionRate, commissionMin) + n*transferFee }
func sellFee(n float64) float64 {
	return math.Max(n*commissionRate, commissionMin) + n*(transferFee+stampTax)
}
func maxDrawdown(curve []float64) float64 {
	var peak, worst float64
	for _, v := range curve {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			d := (peak - v) / peak
			if d > worst {
				worst = d
			}
		}
	}
	return worst
}
func sharpe(curve []float64) float64 {
	if len(curve) < 3 {
		return 0
	}
	r := make([]float64, 0, len(curve)-1)
	var sum float64
	for i := 1; i < len(curve); i++ {
		if curve[i-1] > 0 {
			x := curve[i]/curve[i-1] - 1
			r = append(r, x)
			sum += x
		}
	}
	if len(r) < 2 {
		return 0
	}
	mean := sum / float64(len(r))
	var variance float64
	for _, x := range r {
		variance += (x - mean) * (x - mean)
	}
	sd := math.Sqrt(variance / float64(len(r)-1))
	if sd == 0 {
		return 0
	}
	return mean / sd * math.Sqrt(252)
}
func tradeMetrics(t []Trade) (float64, float64, float64) {
	if len(t) == 0 {
		return 0, 0, 0
	}
	var wins, losses []float64
	for _, x := range t {
		if x.PnL > 0 {
			wins = append(wins, x.PnL)
		} else if x.PnL < 0 {
			losses = append(losses, -x.PnL)
		}
	}
	avg := func(v []float64) float64 {
		var s float64
		for _, x := range v {
			s += x
		}
		if len(v) == 0 {
			return 0
		}
		return s / float64(len(v))
	}
	var gp, gl float64
	for _, x := range wins {
		gp += x
	}
	for _, x := range losses {
		gl += x
	}
	pl, pf := 0.0, 0.0
	if avg(losses) > 0 {
		pl = avg(wins) / avg(losses)
	}
	if gl > 0 {
		pf = gp / gl
	}
	return float64(len(wins)) / float64(len(t)), pl, pf
}
