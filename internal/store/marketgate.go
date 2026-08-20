package store

import (
	"context"
	"fmt"
	"strings"
)

// 指数风向门（Market Direction Gate）：盘前推荐链路的第一道确定性闸门。
// 设计目标是把「指数指标」升级为推荐的风向指标——大盘结构转弱时直接暂停
// 建仓，而不是像旧逻辑那样仅把候选风险上限从 70 微调到 65 后照常开仓。
//
// 三档语义：
//   - green : 指数与市场宽度未见系统性转弱，正常推荐并自动建仓；
//   - yellow: 部分指数走弱或宽度偏弱，仍生成推荐供观察，但不自动建仓，
//     且候选风险上限强制收紧到 down 档；
//   - red   : 多数核心指数破位或全市场普跌，跳过当日推荐与建仓。
//
// 全部输入来自本地 index_snapshot 与 kline_daily，不含 AI 输出，结果确定、可单测。
// AI 复盘的 market_phase 只能在该门之上进一步收紧，永远不能放宽。
const (
	MarketGateGreen  = "green"
	MarketGateYellow = "yellow"
	MarketGateRed    = "red"
)

const (
	// marketGateIndexHistoryDays 是单指数取用的每日收盘样本数：MA20 需要 20 个，
	// 多取 1 个冗余容错。
	marketGateIndexHistoryDays = 21
	// marketGateRedWeakRatio 红灯指数破位占比：≥2/3 核心指数收于 MA20 下方。
	marketGateRedWeakRatio = 2.0 / 3.0
	// marketGateYellowWeakRatio 黄灯指数破位占比：≥1/2 指数收于 MA20 下方。
	marketGateYellowWeakRatio = 0.5
	// marketGateRedUpRatio / marketGateRedAvgPct 红灯宽度阈值：普跌日特征。
	marketGateRedUpRatio = 0.35
	marketGateRedAvgPct  = -1.0
	// marketGateYellowUpRatio / marketGateYellowAvgPct 黄灯宽度阈值：偏弱日特征。
	marketGateYellowUpRatio = 0.45
	marketGateYellowAvgPct  = -0.5
)

// marketGateIndexSymbols 是参与风向判定的核心指数（沪、深、创业板、沪深300）。
// 科创50 与北证50 波动结构差异大，不纳入投票，避免小市值指数噪音放大。
var marketGateIndexSymbols = []string{"SH000001", "SZ399001", "SZ399006", "SH000300"}

var marketGateIndexNames = map[string]string{
	"SH000001": "上证指数", "SZ399001": "深证成指", "SZ399006": "创业板指", "SH000300": "沪深300",
}

// MarketGateIndexFact 是单一指数的确定性风向事实。
type MarketGateIndexFact struct {
	Symbol       string  `json:"symbol"`
	Name         string  `json:"name"`
	Close        float64 `json:"close"`
	MA20         float64 `json:"ma20"`
	Momentum5Pct float64 `json:"momentum_5d_pct"`
	HasMA20      bool    `json:"has_ma20"`
	HasMomentum  bool    `json:"has_momentum"`
}

// MarketGateBreadthFact 是分析日全市场宽度事实（等权口径）。
type MarketGateBreadthFact struct {
	Valid        bool    `json:"valid"`
	StockCount   int     `json:"stock_count"`
	UpRatio      float64 `json:"up_ratio"`
	AvgChangePct float64 `json:"avg_change_pct"`
}

// MarketGate 是一次风向判定的完整结论与依据。
type MarketGate struct {
	TradeDate string                `json:"trade_date"`
	Level     string                `json:"level"`
	Reason    string                `json:"reason"`
	Indices   []MarketGateIndexFact `json:"indices"`
	Breadth   MarketGateBreadthFact `json:"breadth"`
}

// AllowAutoEntry 表示当前风向是否允许自动建仓（仅 green）。
func (g MarketGate) AllowAutoEntry() bool { return g.Level == MarketGateGreen }

// ClassifyMarketGate 是纯函数分类器：按「指数结构投票 + 市场宽度」输出风向档位。
// 判定顺序为 red → yellow → green，数据不足时保守降为 yellow 而不是放行。
func ClassifyMarketGate(tradeDate string, indices []MarketGateIndexFact, breadth MarketGateBreadthFact) MarketGate {
	gate := MarketGate{TradeDate: tradeDate, Indices: indices, Breadth: breadth}

	total, below := 0, 0
	momCount, momSum := 0, 0.0
	weakNames := make([]string, 0, len(indices))
	for _, index := range indices {
		if !index.HasMA20 || index.Close <= 0 || index.MA20 <= 0 {
			continue
		}
		total++
		if index.Close < index.MA20 {
			below++
			weakNames = append(weakNames, index.Name)
		}
		if index.HasMomentum {
			momCount++
			momSum += index.Momentum5Pct
		}
	}
	avgMomentum := 0.0
	if momCount > 0 {
		avgMomentum = momSum / float64(momCount)
	}

	// 红灯 1：多数核心指数收于 MA20 下方且 5 日动量整体为负——趋势性走弱。
	if total >= 2 && float64(below) >= marketGateRedWeakRatio*float64(total) && momCount > 0 && avgMomentum < 0 {
		gate.Level = MarketGateRed
		gate.Reason = fmt.Sprintf("%d/%d核心指数收于MA20下方（%s）且5日平均动量%.2f%%为负，大盘趋势转弱", below, total, strings.Join(weakNames, "、"), avgMomentum)
		return gate
	}
	// 红灯 2：全市场普跌——上涨占比过低且平均跌幅显著。
	if breadth.Valid && breadth.UpRatio < marketGateRedUpRatio && breadth.AvgChangePct <= marketGateRedAvgPct {
		gate.Level = MarketGateRed
		gate.Reason = fmt.Sprintf("全市场上涨占比仅%.0f%%且平均涨跌%.2f%%，普跌环境不具备做多条件", breadth.UpRatio*100, breadth.AvgChangePct)
		return gate
	}

	// 黄灯 1：一半以上指数破位（但未达红灯共振条件）。
	if total >= 2 && float64(below) >= marketGateYellowWeakRatio*float64(total) {
		gate.Level = MarketGateYellow
		gate.Reason = fmt.Sprintf("%d/%d核心指数收于MA20下方（%s），指数结构分化转弱", below, total, strings.Join(weakNames, "、"))
		return gate
	}
	// 黄灯 2：指数尚可但市场宽度偏弱——题材退潮期的典型特征。
	if breadth.Valid && (breadth.UpRatio < marketGateYellowUpRatio || breadth.AvgChangePct <= marketGateYellowAvgPct) {
		gate.Level = MarketGateYellow
		gate.Reason = fmt.Sprintf("市场宽度偏弱：上涨占比%.0f%%、平均涨跌%.2f%%，谨慎参与", breadth.UpRatio*100, breadth.AvgChangePct)
		return gate
	}
	// 黄灯 3：指数动量整体为负（即使均在 MA20 上方，短期方向仍向下）。
	if momCount > 0 && avgMomentum < 0 {
		gate.Level = MarketGateYellow
		gate.Reason = fmt.Sprintf("核心指数5日平均动量%.2f%%为负，短期方向偏空", avgMomentum)
		return gate
	}
	// 黄灯 4：指数与宽度数据均不足——保守降档而不是默认放行。
	if total == 0 && !breadth.Valid {
		gate.Level = MarketGateYellow
		gate.Reason = "指数与市场宽度数据不足，按黄灯保守处理"
		return gate
	}

	gate.Level = MarketGateGreen
	gate.Reason = fmt.Sprintf("%d/%d核心指数收于MA20上方，上涨占比%.0f%%，风向正常", total-below, total, breadth.UpRatio*100)
	return gate
}

// MarketDirectionGate 读取本地数据并执行风向判定。
// tradeDate 是分析基准日（最近一个已收盘交易日）。单一指数历史不足时仅将
// 该指数排除出投票；全部缺失时依靠市场宽度兜底，两者皆缺才按黄灯降档。
func (s *Store) MarketDirectionGate(ctx context.Context, tradeDate string) (MarketGate, error) {
	if tradeDate == "" {
		return MarketGate{}, fmt.Errorf("风向判定日期不能为空")
	}
	indices := make([]MarketGateIndexFact, 0, len(marketGateIndexSymbols))
	for _, symbol := range marketGateIndexSymbols {
		closes, err := s.indexDailyCloses(ctx, symbol, tradeDate, marketGateIndexHistoryDays)
		if err != nil {
			return MarketGate{}, err
		}
		fact := MarketGateIndexFact{Symbol: symbol, Name: marketGateIndexNames[symbol]}
		if len(closes) == 0 {
			continue
		}
		last := len(closes) - 1
		fact.Close = closes[last]
		if len(closes) >= 20 {
			var sum float64
			for _, c := range closes[len(closes)-20:] {
				sum += c
			}
			fact.MA20 = sum / 20
			fact.HasMA20 = true
		}
		if len(closes) >= 6 && closes[last-5] > 0 {
			fact.Momentum5Pct = (closes[last]/closes[last-5] - 1) * 100
			fact.HasMomentum = true
		}
		indices = append(indices, fact)
	}

	var breadth MarketGateBreadthFact
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(k.change_pct>0)/NULLIF(COUNT(*),0),0),COALESCE(AVG(k.change_pct),0)
		FROM kline_daily k INNER JOIN stock_basic b ON b.symbol=k.symbol
		WHERE k.trade_date=? AND b.status='listed' AND b.sec_type='stock'`, tradeDate).
		Scan(&breadth.StockCount, &breadth.UpRatio, &breadth.AvgChangePct)
	if err != nil {
		return MarketGate{}, fmt.Errorf("query market gate breadth: %w", err)
	}
	breadth.Valid = breadth.StockCount > 0

	return ClassifyMarketGate(tradeDate, indices, breadth), nil
}

// indexDailyCloses 从 index_snapshot 取指数最近 limit 个交易日的日终收盘价
// （每个自然日取最后一条快照），按日期升序返回。
func (s *Store) indexDailyCloses(ctx context.Context, symbol, tradeDate string, limit int) ([]float64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.price FROM (
			SELECT MAX(snapshot_at) snapshot_at FROM index_snapshot
			WHERE symbol=? AND DATE(snapshot_at)<=?
			GROUP BY DATE(snapshot_at) ORDER BY DATE(snapshot_at) DESC LIMIT ?
		) t INNER JOIN index_snapshot s ON s.symbol=? AND s.snapshot_at=t.snapshot_at
		ORDER BY s.snapshot_at ASC`, symbol, tradeDate, limit, symbol)
	if err != nil {
		return nil, fmt.Errorf("query index daily closes %s: %w", symbol, err)
	}
	defer rows.Close()
	closes := make([]float64, 0, limit)
	for rows.Next() {
		var price float64
		if err := rows.Scan(&price); err != nil {
			return nil, err
		}
		if price > 0 {
			closes = append(closes, price)
		}
	}
	return closes, rows.Err()
}
