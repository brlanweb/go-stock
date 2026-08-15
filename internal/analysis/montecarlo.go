package analysis

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"

	"github.com/hoax/go-stock/internal/model"
)

const (
	monteCarloDefaultDays  = 10
	monteCarloMaxDays      = 60
	monteCarloPaths        = 5000
	monteCarloMinSample    = 60
	monteCarloSampleWindow = 250
)

// MonteCarloResult 是基于历史日收益率自助抽样（bootstrap）的路径模拟结果。
// 模拟不假设收益分布形态，直接从最近 SampleDays 个真实日收益率中有放回抽样，
// 生成 Paths 条未来 Days 个交易日的价格路径后统计分布，仅供风险参考。
type MonteCarloResult struct {
	Symbol     string  `json:"symbol"`
	Days       int     `json:"days"`
	Paths      int     `json:"paths"`
	SampleDays int     `json:"sample_days"`
	BasePrice  float64 `json:"base_price"`
	// WinRate 是期末价格高于基准价的路径占比（%）。
	WinRate      float64 `json:"win_rate"`
	AvgReturnPct float64 `json:"avg_return_pct"`
	MedianPct    float64 `json:"median_pct"`
	P5Pct        float64 `json:"p5_pct"`
	P25Pct       float64 `json:"p25_pct"`
	P75Pct       float64 `json:"p75_pct"`
	P95Pct       float64 `json:"p95_pct"`
	// ProbGain5 / ProbLoss5 是期末涨超 +5% / 跌超 -5% 的路径占比（%）。
	ProbGain5 float64 `json:"prob_gain_5_pct"`
	ProbLoss5 float64 `json:"prob_loss_5_pct"`
}

// RunMonteCarlo 对单只股票执行蒙特卡洛模拟。klines 必须为前复权日 K（时间正序）；
// 结果对相同输入完全确定（随机种子由 symbol+最后交易日派生），便于前端复现与缓存。
func RunMonteCarlo(symbol string, klines []model.Kline, days int) (MonteCarloResult, error) {
	if days <= 0 {
		days = monteCarloDefaultDays
	}
	if days > monteCarloMaxDays {
		days = monteCarloMaxDays
	}
	if len(klines) > monteCarloSampleWindow+1 {
		klines = klines[len(klines)-monteCarloSampleWindow-1:]
	}
	if len(klines) < monteCarloMinSample+1 {
		return MonteCarloResult{}, fmt.Errorf("历史日K不足（需至少 %d 根，实际 %d 根）", monteCarloMinSample+1, len(klines))
	}
	returns := make([]float64, 0, len(klines)-1)
	for i := 1; i < len(klines); i++ {
		prev, curr := klines[i-1].Close, klines[i].Close
		if prev <= 0 || curr <= 0 {
			continue
		}
		returns = append(returns, curr/prev-1)
	}
	if len(returns) < monteCarloMinSample {
		return MonteCarloResult{}, fmt.Errorf("有效日收益率样本不足（%d）", len(returns))
	}
	base := klines[len(klines)-1].Close

	seedHash := fnv.New64a()
	seedHash.Write([]byte(symbol + "|" + klines[len(klines)-1].Date))
	rng := rand.New(rand.NewSource(int64(seedHash.Sum64())))

	finalPct := make([]float64, monteCarloPaths)
	var sum float64
	var wins, gain5, loss5 int
	for p := 0; p < monteCarloPaths; p++ {
		price := base
		for d := 0; d < days; d++ {
			price *= 1 + returns[rng.Intn(len(returns))]
		}
		pct := (price/base - 1) * 100
		finalPct[p] = pct
		sum += pct
		if pct > 0 {
			wins++
		}
		if pct >= 5 {
			gain5++
		}
		if pct <= -5 {
			loss5++
		}
	}
	sort.Float64s(finalPct)
	percentile := func(q float64) float64 {
		idx := int(q * float64(len(finalPct)-1))
		return finalPct[idx]
	}
	return MonteCarloResult{
		Symbol:       symbol,
		Days:         days,
		Paths:        monteCarloPaths,
		SampleDays:   len(returns),
		BasePrice:    base,
		WinRate:      float64(wins) / float64(monteCarloPaths) * 100,
		AvgReturnPct: sum / float64(monteCarloPaths),
		MedianPct:    percentile(0.5),
		P5Pct:        percentile(0.05),
		P25Pct:       percentile(0.25),
		P75Pct:       percentile(0.75),
		P95Pct:       percentile(0.95),
		ProbGain5:    float64(gain5) / float64(monteCarloPaths) * 100,
		ProbLoss5:    float64(loss5) / float64(monteCarloPaths) * 100,
	}, nil
}
