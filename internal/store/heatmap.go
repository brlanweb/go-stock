package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hoax/go-stock/internal/model"
)

// MarketHeatmap 从每个证券的最新日K生成行业云图。查询先在 kline_daily
// 上按主键前缀 (symbol, trade_date) 汇总，随后再关联基础信息，避免相关子查询。
func (s *Store) MarketHeatmap(ctx context.Context, market, groupBy, metric, period string) ([]model.HeatmapGroup, string, error) {
	if groupBy != "industry" && groupBy != "concept" {
		groupBy = "industry"
	}
	if metric != "change_pct" && metric != "pe_ttm" && metric != "main_net_inflow" {
		metric = "change_pct"
	}
	if period != "1d" && period != "3d" && period != "5d" {
		period = "1d"
	}
	if groupBy == "concept" {
		return []model.HeatmapGroup{}, "概念板块数据尚未同步", nil
	}
	if metric == "main_net_inflow" {
		return []model.HeatmapGroup{}, "主力资金数据尚未同步", nil
	}

	where := heatmapMarketWhere(market)
	query := fmt.Sprintf(`
		SELECT b.symbol,b.code,b.name,b.industry,b.exchange,
		       k.change_pct, k.close,
		       COALESCE(NULLIF(d.pe_ratio,0), NULLIF(ms.pe_ratio,0), 0),
		       COALESCE(NULLIF(d.total_mv,0), NULLIF(ms.total_mv,0), 0)
		FROM stock_basic b
		INNER JOIN (
			SELECT symbol, MAX(trade_date) AS trade_date
			FROM kline_daily
			GROUP BY symbol
		) latest ON latest.symbol=b.symbol
		INNER JOIN kline_daily k ON k.symbol=latest.symbol AND k.trade_date=latest.trade_date
		LEFT JOIN daily_indicator d ON d.symbol=k.symbol AND d.trade_date=k.trade_date
		LEFT JOIN (
			SELECT snap.symbol,snap.pe_ratio,snap.total_mv
			FROM market_snapshot snap
			INNER JOIN (SELECT symbol,MAX(snapshot_at) AS snapshot_at FROM market_snapshot GROUP BY symbol) latest_snap
			  ON latest_snap.symbol=snap.symbol AND latest_snap.snapshot_at=snap.snapshot_at
		) ms ON ms.symbol=k.symbol
		WHERE b.status='listed' AND b.sec_type='stock' %s
		ORDER BY COALESCE(NULLIF(d.total_mv,0), NULLIF(ms.total_mv,0), 0) DESC, b.code`, where)
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
	groups := make(map[string][]model.HeatmapItem)
	for rows.Next() {
		var item model.HeatmapItem
		var close float64
		if err := rows.Scan(&item.Symbol, &item.Code, &item.Name, &item.Industry, &item.Market, &item.ChangePct, &close, &item.PERatio, &item.TotalMV); err != nil {
			return nil, "", err
		}
		item.PeriodChange = item.ChangePct
		if offset > 0 {
			change, err := s.periodChange(ctx, item.Symbol, close, offset)
			if err != nil {
				return nil, "", err
			}
			item.PeriodChange = change
		}
		name := item.Industry
		if name == "" || name == "-" {
			name = "其他"
		}
		groups[name] = append(groups[name], item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	out := make([]model.HeatmapGroup, 0, len(groups))
	for name, items := range groups {
		var weighted, total float64
		for _, item := range items {
			weight := item.TotalMV
			if weight <= 0 {
				weight = 1
			}
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
	sort.Slice(out, func(i, j int) bool {
		var left, right float64
		for _, item := range out[i].Items {
			left += item.TotalMV
		}
		for _, item := range out[j].Items {
			right += item.TotalMV
		}
		return left > right
	})
	return out, "", nil
}

func (s *Store) periodChange(ctx context.Context, symbol string, latest float64, offset int) (float64, error) {
	var previous float64
	err := s.DB.QueryRowContext(ctx, `SELECT close FROM kline_daily WHERE symbol=? ORDER BY trade_date DESC LIMIT 1 OFFSET ?`, symbol, offset).Scan(&previous)
	if err != nil || previous <= 0 || latest <= 0 {
		return 0, nil
	}
	return (latest - previous) / previous * 100, nil
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

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
