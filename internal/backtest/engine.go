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
	case "ema_cross":
		fast, slow := intParam(params, "fast", 12), intParam(params, "slow", 26)
		f, s := ema(closes, fast), ema(closes, slow)
		for i := slow; i < len(k); i++ {
			if crossedUp(f, s, i) {
				add(i, "buy", fmt.Sprintf("EMA%d 上穿 EMA%d", fast, slow))
			} else if crossedDown(f, s, i) {
				add(i, "sell", fmt.Sprintf("EMA%d 下穿 EMA%d", fast, slow))
			}
		}
	case "macd_cross":
		fast, slow, signalPeriod := intParam(params, "fast", 12), intParam(params, "slow", 26), intParam(params, "signal", 9)
		fastEMA, slowEMA := ema(closes, fast), ema(closes, slow)
		dif := make([]float64, len(k))
		for i := range dif {
			dif[i] = fastEMA[i] - slowEMA[i]
		}
		dea := ema(dif, signalPeriod)
		for i := slow; i < len(k); i++ {
			if crossedUp(dif, dea, i) {
				add(i, "buy", "MACD DIF 上穿 DEA")
			} else if crossedDown(dif, dea, i) {
				add(i, "sell", "MACD DIF 下穿 DEA")
			}
		}
	case "rsi_reversal":
		period := intParam(params, "period", 14)
		oversold, overbought := floatParam(params, "oversold", 30), floatParam(params, "overbought", 70)
		values := rsi(closes, period)
		for i := period + 1; i < len(k); i++ {
			if values[i-1] <= oversold && values[i] > oversold {
				add(i, "buy", fmt.Sprintf("RSI%d 从超卖区回升", period))
			} else if values[i-1] >= overbought && values[i] < overbought {
				add(i, "sell", fmt.Sprintf("RSI%d 从超买区回落", period))
			}
		}
	case "boll_mean_reversion", "boll_breakout":
		period := intParam(params, "period", 20)
		multiplier := floatParam(params, "multiplier", 2)
		middle, upper, lower := bollinger(closes, period, multiplier)
		for i := period; i < len(k); i++ {
			if id == "boll_mean_reversion" {
				if closes[i-1] <= lower[i-1] && closes[i] > lower[i] {
					add(i, "buy", "价格重新站回 BOLL 下轨")
				} else if closes[i] >= middle[i] {
					add(i, "sell", "价格回归 BOLL 中轨")
				}
			} else {
				if closes[i-1] <= upper[i-1] && closes[i] > upper[i] {
					add(i, "buy", "价格突破 BOLL 上轨")
				} else if closes[i] < middle[i] {
					add(i, "sell", "价格跌破 BOLL 中轨")
				}
			}
		}
	case "donchian_breakout":
		entryPeriod, exitPeriod := intParam(params, "entry_period", 20), intParam(params, "exit_period", 10)
		for i := maxInt(entryPeriod, exitPeriod); i < len(k); i++ {
			entryHigh := rangeHigh(k, i-entryPeriod, i)
			exitLow := rangeLow(k, i-exitPeriod, i)
			if k[i].Close > entryHigh {
				add(i, "buy", fmt.Sprintf("收盘突破前%d日 Donchian 上轨", entryPeriod))
			} else if k[i].Close < exitLow {
				add(i, "sell", fmt.Sprintf("收盘跌破前%d日 Donchian 下轨", exitPeriod))
			}
		}
	case "kdj_reversal":
		period := intParam(params, "period", 9)
		oversold, overbought := floatParam(params, "oversold", 30), floatParam(params, "overbought", 70)
		kv, dv := kdj(k, period)
		for i := period; i < len(k); i++ {
			if crossedUp(kv, dv, i) && kv[i] <= oversold {
				add(i, "buy", "KDJ 超卖区金叉")
			} else if crossedDown(kv, dv, i) && kv[i] >= overbought {
				add(i, "sell", "KDJ 超买区死叉")
			}
		}
	case "roc_momentum":
		period := intParam(params, "period", 12)
		threshold := numberParam(params, "threshold", 0)
		roc := make([]float64, len(k))
		for i := range roc {
			roc[i] = math.NaN()
			if i >= period && closes[i-period] != 0 {
				roc[i] = (closes[i]/closes[i-period] - 1) * 100
			}
		}
		for i := period + 1; i < len(k); i++ {
			if roc[i-1] <= threshold && roc[i] > threshold {
				add(i, "buy", fmt.Sprintf("ROC%d 上穿 %.2f", period, threshold))
			} else if roc[i-1] >= threshold && roc[i] < threshold {
				add(i, "sell", fmt.Sprintf("ROC%d 下穿 %.2f", period, threshold))
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
func ema(v []float64, period int) []float64 {
	out := make([]float64, len(v))
	if len(v) == 0 {
		return out
	}
	alpha := 2 / float64(period+1)
	out[0] = v[0]
	for i := 1; i < len(v); i++ {
		out[i] = alpha*v[i] + (1-alpha)*out[i-1]
	}
	return out
}
func rsi(v []float64, period int) []float64 {
	out := make([]float64, len(v))
	for i := range out {
		out[i] = math.NaN()
	}
	if len(v) <= period {
		return out
	}
	var gain, loss float64
	for i := 1; i <= period; i++ {
		delta := v[i] - v[i-1]
		if delta >= 0 {
			gain += delta
		} else {
			loss -= delta
		}
	}
	gain /= float64(period)
	loss /= float64(period)
	out[period] = rsiValue(gain, loss)
	for i := period + 1; i < len(v); i++ {
		delta, up, down := v[i]-v[i-1], 0.0, 0.0
		if delta >= 0 {
			up = delta
		} else {
			down = -delta
		}
		gain = (gain*float64(period-1) + up) / float64(period)
		loss = (loss*float64(period-1) + down) / float64(period)
		out[i] = rsiValue(gain, loss)
	}
	return out
}
func rsiValue(gain, loss float64) float64 {
	if loss == 0 {
		if gain == 0 {
			return 50
		}
		return 100
	}
	return 100 - 100/(1+gain/loss)
}
func bollinger(v []float64, period int, multiplier float64) ([]float64, []float64, []float64) {
	middle := sma(v, period)
	upper, lower := make([]float64, len(v)), make([]float64, len(v))
	for i := range v {
		upper[i], lower[i] = math.NaN(), math.NaN()
		if i+1 < period {
			continue
		}
		var variance float64
		for j := i - period + 1; j <= i; j++ {
			delta := v[j] - middle[i]
			variance += delta * delta
		}
		stddev := math.Sqrt(variance / float64(period))
		upper[i], lower[i] = middle[i]+multiplier*stddev, middle[i]-multiplier*stddev
	}
	return middle, upper, lower
}
func kdj(k []model.Kline, period int) ([]float64, []float64) {
	kv, dv := make([]float64, len(k)), make([]float64, len(k))
	for i := range k {
		kv[i], dv[i] = 50, 50
		if i+1 < period {
			continue
		}
		lo, hi := rangeLow(k, i-period+1, i+1), rangeHigh(k, i-period+1, i+1)
		rsv := 50.0
		if hi > lo {
			rsv = (k[i].Close - lo) / (hi - lo) * 100
		}
		kv[i] = kv[i-1]*2/3 + rsv/3
		dv[i] = dv[i-1]*2/3 + kv[i]/3
	}
	return kv, dv
}
func rangeHigh(k []model.Kline, from, to int) float64 {
	hi := k[from].High
	for i := from + 1; i < to; i++ {
		if k[i].High > hi {
			hi = k[i].High
		}
	}
	return hi
}
func rangeLow(k []model.Kline, from, to int) float64 {
	lo := k[from].Low
	for i := from + 1; i < to; i++ {
		if k[i].Low < lo {
			lo = k[i].Low
		}
	}
	return lo
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
func numberParam(p map[string]any, key string, d float64) float64 {
	if v, ok := p[key].(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return v
	}
	if v, ok := p[key].(int); ok {
		return float64(v)
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
