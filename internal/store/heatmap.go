package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

type heatmapCandidate struct {
	item  model.HeatmapItem
	group string
	score float64
}

// MarketHeatmap 从本地日 K 和市场快照生成云图。limit 限制最终证券总数；
// 3/5 日收益通过一次批量查询计算，避免逐证券查询。
func (s *Store) MarketHeatmap(ctx context.Context, market, groupBy, metric, period string, limit int) ([]model.HeatmapGroup, string, error) {
	if groupBy != "industry" && groupBy != "concept" {
		groupBy = "industry"
	}
	if metric != "change_pct" && metric != "pe_ttm" && metric != "main_net_inflow" {
		metric = "change_pct"
	}
	if period != "1d" && period != "3d" && period != "5d" {
		period = "1d"
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 5000 {
		limit = 5000
	}
	if metric == "main_net_inflow" {
		return []model.HeatmapGroup{}, "主力资金数据尚未同步", nil
	}

	where := heatmapMarketWhere(market)
	groupSelect := "b.industry"
	groupJoin := ""
	if groupBy == "concept" {
		exists, err := s.SectorMembershipExists(ctx, "concept")
		if err != nil {
			return nil, "", err
		}
		if !exists {
			return []model.HeatmapGroup{}, "概念板块数据尚未同步", nil
		}
		groupSelect = "sb.sector_name"
		groupJoin = "INNER JOIN sector_constituent sc ON sc.symbol=b.symbol INNER JOIN sector_basic sb ON sb.sector_code=sc.sector_code AND sb.sector_type='concept'"
	}
	query := fmt.Sprintf(`
		SELECT b.symbol,b.code,b.name,%s,b.exchange,
		       k.change_pct,k.close,
		       COALESCE(NULLIF(d.pe_ratio,0),NULLIF(ms.pe_ratio,0),0),
		       COALESCE(NULLIF(d.total_mv,0),NULLIF(ms.total_mv,0),0),
		       COALESCE(NULLIF(k.amount,0),NULLIF(ms.amount,0),0)
		FROM stock_basic b
		%s
		INNER JOIN (SELECT symbol,MAX(trade_date) trade_date FROM kline_daily GROUP BY symbol) latest ON latest.symbol=b.symbol
		INNER JOIN kline_daily k ON k.symbol=latest.symbol AND k.trade_date=latest.trade_date
		LEFT JOIN daily_indicator d ON d.symbol=k.symbol AND d.trade_date=k.trade_date
		LEFT JOIN (
			SELECT snap.symbol,snap.pe_ratio,snap.total_mv,snap.amount
			FROM market_snapshot snap
			INNER JOIN (SELECT symbol,MAX(snapshot_at) snapshot_at FROM market_snapshot GROUP BY symbol) ls
			  ON ls.symbol=snap.symbol AND ls.snapshot_at=snap.snapshot_at
		) ms ON ms.symbol=k.symbol
		WHERE b.status='listed' AND b.sec_type='stock' %s`, groupSelect, groupJoin, where)
	rows, err := s.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("查询云图数据: %w", err)
	}
	defer rows.Close()

	offset := 0
	if period == "3d" {
		offset = 2
	} else if period == "5d" {
		offset = 4
	}
	previous := map[string]float64{}
	if offset > 0 {
		previous, err = s.batchPreviousCloses(ctx, offset)
		if err != nil {
			return nil, "", err
		}
	}

	seen := make(map[string]bool)
	candidates := make([]heatmapCandidate, 0, 6000)
	for rows.Next() {
		var item model.HeatmapItem
		var close, amount float64
		if err := rows.Scan(&item.Symbol, &item.Code, &item.Name, &item.Industry, &item.Market, &item.ChangePct, &close, &item.PERatio, &item.TotalMV, &amount); err != nil {
			return nil, "", err
		}
		item.PeriodChange = item.ChangePct
		if offset > 0 {
			if old := previous[item.Symbol]; old > 0 && close > 0 {
				item.PeriodChange = (close - old) / old * 100
			}
		}
		name := item.Industry
		if name == "" || name == "-" {
			name = "其他"
		}
		key := name + "\x00" + item.Symbol
		if seen[key] {
			continue
		}
		seen[key] = true
		// 热度兼顾价格波动和成交活跃度；对成交额取对数，避免超大盘股垄断结果。
		score := math.Abs(item.PeriodChange)*0.65 + math.Log10(math.Max(amount, 1))*0.35
		candidates = append(candidates, heatmapCandidate{item: item, group: name, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].item.TotalMV > candidates[j].item.TotalMV
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	grouped := make(map[string][]model.HeatmapItem)
	groupScore := make(map[string]float64)
	for _, candidate := range candidates {
		grouped[candidate.group] = append(grouped[candidate.group], candidate.item)
		groupScore[candidate.group] += candidate.score
	}
	out := make([]model.HeatmapGroup, 0, len(grouped))
	for name, items := range grouped {
		var weighted, total float64
		for _, item := range items {
			weight := math.Max(item.TotalMV, 1)
			value := item.PeriodChange
			if metric == "pe_ttm" {
				value = item.PERatio
			}
			weighted += value * weight
			total += weight
		}
		sort.Slice(items, func(i, j int) bool { return items[i].TotalMV > items[j].TotalMV })
		out = append(out, model.HeatmapGroup{Name: name, ChangePct: weighted / total, Items: items})
	}
	sort.Slice(out, func(i, j int) bool { return groupScore[out[i].Name] > groupScore[out[j].Name] })
	return out, "", nil
}

// batchPreviousCloses 一次读取最近 14 个自然日的日 K，并取得每只证券第 offset 个历史收盘。
func (s *Store) batchPreviousCloses(ctx context.Context, offset int) (map[string]float64, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT symbol,trade_date,close
		FROM kline_daily
		WHERE trade_date >= DATE_SUB((SELECT MAX(trade_date) FROM kline_daily), INTERVAL 14 DAY)
		ORDER BY symbol,trade_date DESC`)
	if err != nil {
		return nil, fmt.Errorf("批量查询周期收盘价: %w", err)
	}
	defer rows.Close()
	result := make(map[string]float64)
	counts := make(map[string]int)
	for rows.Next() {
		var symbol string
		var date time.Time
		var close float64
		if err := rows.Scan(&symbol, &date, &close); err != nil {
			return nil, err
		}
		if counts[symbol] == offset {
			result[symbol] = close
		}
		counts[symbol]++
	}
	return result, rows.Err()
}

func heatmapMarketWhere(market string) string {
	switch strings.ToLower(market) {
	case "gem":
		return " AND b.code LIKE '30%'"
	case "star":
		return " AND b.code LIKE '68%'"
	case "bse":
		return " AND b.exchange='BJ'"
	default:
		return ""
	}
}
