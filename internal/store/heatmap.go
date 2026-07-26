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

const heatmapItemsPerGroup = 50

type heatmapGroupAccumulator struct {
	items          []model.HeatmapItem
	weightedChange float64
	marketValue    float64
	amount         float64
}

type rankedHeatmapGroup struct {
	group model.HeatmapGroup
	heat  float64
}

// MarketHeatmap 从本地日 K 和市场快照生成云图。limit 表示最多返回的行业/概念
// 板块数量；入选板块保留其全部成分股，不限制全市场证券总数。
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
	if limit > 500 {
		limit = 500
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

	groups := make(map[string]*heatmapGroupAccumulator)
	seen := make(map[string]bool)
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
		group := groups[name]
		if group == nil {
			group = &heatmapGroupAccumulator{}
			groups[name] = group
		}
		weight := math.Max(item.TotalMV, 1)
		group.items = append(group.items, item)
		group.weightedChange += item.PeriodChange * weight
		group.marketValue += weight
		group.amount += math.Max(amount, 0)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	ranked := make([]rankedHeatmapGroup, 0, len(groups))
	for name, accumulator := range groups {
		change := 0.0
		if accumulator.marketValue > 0 {
			change = accumulator.weightedChange / accumulator.marketValue
		}
		groupValue := change
		if metric == "pe_ttm" {
			var weightedPE, total float64
			for _, item := range accumulator.items {
				weight := math.Max(item.TotalMV, 1)
				weightedPE += item.PERatio * weight
				total += weight
			}
			if total > 0 {
				groupValue = weightedPE / total
			}
		}
		sort.Slice(accumulator.items, func(i, j int) bool {
			return accumulator.items[i].TotalMV > accumulator.items[j].TotalMV
		})
		if len(accumulator.items) > heatmapItemsPerGroup {
			accumulator.items = accumulator.items[:heatmapItemsPerGroup]
		}
		// 板块热度：板块整体涨跌强度为主，板块总成交额活跃度为辅。
		heat := math.Abs(change)*0.65 + math.Log10(math.Max(accumulator.amount, 1))*0.35
		ranked = append(ranked, rankedHeatmapGroup{
			group: model.HeatmapGroup{Name: name, ChangePct: groupValue, Items: accumulator.items},
			heat:  heat,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].heat == ranked[j].heat {
			return ranked[i].group.Name < ranked[j].group.Name
		}
		return ranked[i].heat > ranked[j].heat
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]model.HeatmapGroup, len(ranked))
	for i := range ranked {
		out[i] = ranked[i].group
	}
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
