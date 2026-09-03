package store

import (
	"testing"

	"github.com/hoax/go-stock/internal/model"
)

func TestRecommendationOverextendedByGain5(t *testing.T) {
	build := func(gain5 float64) []model.Kline {
		klines := make([]model.Kline, recommendationKlineDays)
		base := 10.0
		for i := range klines {
			klines[i] = model.Kline{Close: base}
		}
		// 尾部 5 根线性爬升到目标涨幅，保持贴近 MA5，隔离乖离项影响。
		for i := 0; i < 5; i++ {
			klines[recommendationKlineDays-5+i] = model.Kline{Close: base * (1 + gain5*float64(i+1)/5)}
		}
		return klines
	}
	// 近 5 日涨 30% 远超硬上限 → 剔除
	if !recommendationOverextended(build(0.30)) {
		t.Fatal("gain5=30% must be filtered as overextended")
	}
	// 近 5 日涨 20%：旧 25% 阈值下会被放行，收紧到 15% 后必须剔除。
	// 该区间正是 2026-09 复盘中追高候选最密集的地带（实际推荐股推荐前
	// 5 日平均涨幅 11.23%，全市场中位数仅 1.92%）。
	// 此构造下 MA5 乖离约 7.1% 未触发乖离规则，可隔离验证 gain5 阈值。
	if !recommendationOverextended(build(0.20)) {
		t.Fatal("gain5=20% must be filtered after tightening threshold to 15%")
	}
	// 近 5 日涨 10% → 保留
	if recommendationOverextended(build(0.10)) {
		t.Fatal("gain5=10% must not be filtered")
	}
}

func TestRecommendationOverextendedByMA5Bias(t *testing.T) {
	klines := make([]model.Kline, recommendationKlineDays)
	for i := range klines {
		klines[i] = model.Kline{Close: 10}
	}
	// 最后一根单日跳涨 15%：5 日涨幅 15% 未触发硬上限，但收盘较 MA5 乖离
	// 15/(1+2+... )：MA5=(10*4+11.5)/5=10.3 → 乖离 11.65% > 8% → 剔除。
	klines[recommendationKlineDays-1] = model.Kline{Close: 11.5}
	if !recommendationOverextended(klines) {
		t.Fatal("large MA5 bias must be filtered as overextended")
	}
	// 温和上涨：乖离小 → 保留
	for i := range klines {
		klines[i] = model.Kline{Close: 10 + float64(i)*0.02}
	}
	if recommendationOverextended(klines) {
		t.Fatal("mild uptrend must not be filtered")
	}
	// 数据不足时不剔除，交由既有过滤处理
	if recommendationOverextended(klines[:4]) {
		t.Fatal("insufficient data must not be filtered here")
	}
}

func TestRecommendationLeaderBoostsRankWithinSector(t *testing.T) {
	pool := []RecommendationCandidate{
		{Symbol: "SH600001", Industry: "算力", Popularity: 500},
		{Symbol: "SH600002", Industry: "算力", Popularity: 400},
		{Symbol: "SH600003", Industry: "算力", Popularity: 300},
		{Symbol: "SH600004", Industry: "算力", Popularity: 200},
		{Symbol: "SZ000001", Industry: "机器人", Popularity: 450},
		{Symbol: "SZ000002", Industry: "机器人", Popularity: 100},
	}
	boosts := recommendationLeaderBoosts(pool)
	// 每个板块的成交额第 1 名拿最高加权
	if boosts["SH600001"] != recommendationLeaderBoostTop1 || boosts["SZ000001"] != recommendationLeaderBoostTop1 {
		t.Fatalf("sector leaders must get top boost: %+v", boosts)
	}
	// 第 2-3 名拿次级加权
	if boosts["SH600002"] != recommendationLeaderBoostTopN || boosts["SH600003"] != recommendationLeaderBoostTopN || boosts["SZ000002"] != recommendationLeaderBoostTopN {
		t.Fatalf("top-3 followers must get secondary boost: %+v", boosts)
	}
	// 第 4 名不加权
	if boosts["SH600004"] != 1 {
		t.Fatalf("rank-4 candidate must not be boosted: %+v", boosts)
	}
	// 输入顺序无关：打乱后结果一致
	shuffled := []RecommendationCandidate{pool[3], pool[5], pool[0], pool[4], pool[2], pool[1]}
	again := recommendationLeaderBoosts(shuffled)
	for symbol, want := range boosts {
		if again[symbol] != want {
			t.Fatalf("boosts must be order-independent: %s got %f want %f", symbol, again[symbol], want)
		}
	}
}
