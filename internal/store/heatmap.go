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

// primaryIndustryCodes 是东方财富一级行业板块。板块接口同时返回一级、二级
// 和三级行业，云图只使用一级行业，避免同一证券跨层级重复并混入“银行Ⅱ”等
// 细分名称。
var primaryIndustryCodes = []string{
	"BK1201", "BK1203", "BK0478", "BK1206", "BK0438", "BK0427",
	"BK1283", "BK1200", "BK1216", "BK1205", "BK1207", "BK1210",
	"BK1204", "BK0437", "BK0464", "BK0456", "BK1208", "BK0433",
	"BK1202", "BK1215", "BK1211", "BK1209", "BK1213", "BK0728",
	"BK0436", "BK0479", "BK0486", "BK1212", "BK1214", "BK1035",
	"BK1217",
}

type heatmapGroupAccumulator struct {
	sectorCode     string
	items          []model.HeatmapItem
	weightedChange float64
	marketValue    float64
	circulatingMV  float64
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
		limit = 32
	}
	if limit > 500 {
		limit = 500
	}
	if metric == "main_net_inflow" {
		return []model.HeatmapGroup{}, "主力资金数据尚未同步", nil
	}

	where := heatmapMarketWhere(market)
	groupSelect := "sb.sector_name"
	groupCodeSelect := "sb.sector_code"
	groupJoin := "INNER JOIN sector_constituent sc ON sc.symbol=b.symbol INNER JOIN sector_basic sb ON sb.sector_code=sc.sector_code AND sb.sector_type='industry' AND sb.sector_code IN (" + sqlStringList(primaryIndustryCodes) + ")"
	if groupBy == "concept" {
		exists, err := s.SectorMembershipExists(ctx, "concept")
		if err != nil {
			return nil, "", err
		}
		if !exists {
			return []model.HeatmapGroup{}, "概念板块数据尚未同步", nil
		}
		groupSelect = "sb.sector_name"
		groupCodeSelect = "sb.sector_code"
		groupJoin = "INNER JOIN sector_constituent sc ON sc.symbol=b.symbol INNER JOIN sector_basic sb ON sb.sector_code=sc.sector_code AND sb.sector_type='concept'"
	}
	query := fmt.Sprintf(`
		SELECT b.symbol,b.code,b.name,%s,%s,b.exchange,
		       k.change_pct,k.close,
		       COALESCE(NULLIF(d.pe_ratio,0),NULLIF(ms.pe_ratio,0),0),
		       COALESCE(NULLIF(d.total_mv,0),NULLIF(ms.total_mv,0),0),
		       COALESCE(NULLIF(d.circ_mv,0),NULLIF(ms.circ_mv,0),NULLIF(d.total_mv,0),NULLIF(ms.total_mv,0),0),
		       COALESCE(NULLIF(k.amount,0),NULLIF(ms.amount,0),0)
		FROM stock_basic b
		%s
		INNER JOIN (SELECT symbol,MAX(trade_date) trade_date FROM kline_daily GROUP BY symbol) latest ON latest.symbol=b.symbol
		INNER JOIN kline_daily k ON k.symbol=latest.symbol AND k.trade_date=latest.trade_date
		LEFT JOIN daily_indicator d ON d.symbol=k.symbol AND d.trade_date=k.trade_date
		LEFT JOIN (
			SELECT snap.symbol,snap.pe_ratio,snap.total_mv,snap.circ_mv,snap.amount
			FROM market_snapshot snap
			INNER JOIN (SELECT symbol,MAX(snapshot_at) snapshot_at FROM market_snapshot GROUP BY symbol) ls
			  ON ls.symbol=snap.symbol AND ls.snapshot_at=snap.snapshot_at
		) ms ON ms.symbol=k.symbol
		WHERE b.status='listed' AND b.sec_type='stock' %s`, groupSelect, groupCodeSelect, groupJoin, where)
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
		var groupCode string
		var close, amount float64
		if err := rows.Scan(&item.Symbol, &item.Code, &item.Name, &item.Industry, &groupCode, &item.Market, &item.ChangePct, &close, &item.PERatio, &item.TotalMV, &item.CircMV, &amount); err != nil {
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
			group = &heatmapGroupAccumulator{sectorCode: groupCode}
			groups[name] = group
		} else if group.sectorCode == "" && groupCode != "" {
			group.sectorCode = groupCode
		}
		weight := math.Max(item.CircMV, 1)
		group.items = append(group.items, item)
		group.weightedChange += item.PeriodChange * weight
		group.marketValue += math.Max(item.TotalMV, 1)
		group.circulatingMV += weight
		group.amount += math.Max(amount, 0)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	ranked := make([]rankedHeatmapGroup, 0, len(groups))
	for name, accumulator := range groups {
		change := 0.0
		if accumulator.circulatingMV > 0 {
			change = accumulator.weightedChange / accumulator.circulatingMV
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
			return accumulator.items[i].CircMV > accumulator.items[j].CircMV
		})
		stockCount := len(accumulator.items)
		if len(accumulator.items) > heatmapItemsPerGroup {
			accumulator.items = accumulator.items[:heatmapItemsPerGroup]
		}
		// 板块热度突出强势方向：正涨幅直接贡献热度，负涨幅只保留
		// 很小的绝对波动贡献；成交额用于区分同方向板块的活跃度。
		positiveStrength := math.Max(change, 0)
		negativeActivity := math.Max(-change, 0) * 0.08
		heat := positiveStrength*0.72 + negativeActivity*0.08 + math.Log10(math.Max(accumulator.amount, 1))*0.20
		ranked = append(ranked, rankedHeatmapGroup{
			group: model.HeatmapGroup{
				Name: name, SectorCode: accumulator.sectorCode, SectorType: groupBy,
				ChangePct: groupValue, TotalMV: accumulator.marketValue,
				CircMV: accumulator.circulatingMV, Amount: accumulator.amount, StockCount: stockCount, Items: accumulator.items,
			},
			heat: heat,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		left, right := ranked[i].group.CircMV, ranked[j].group.CircMV
		if groupBy == "concept" {
			// 概念板块先选取兼具流通市值覆盖和交易活跃度的主要主题；行业则
			// 保持一级行业全景，按流通市值排序。
			left = conceptRankScore(ranked[i].group.CircMV, ranked[i].heat)
			right = conceptRankScore(ranked[j].group.CircMV, ranked[j].heat)
		}
		if left == right {
			return ranked[i].group.Name < ranked[j].group.Name
		}
		return left > right
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	minHeat, maxHeat := 0.0, 0.0
	if len(ranked) > 0 {
		minHeat, maxHeat = ranked[0].heat, ranked[0].heat
		for i := 1; i < len(ranked); i++ {
			minHeat = math.Min(minHeat, ranked[i].heat)
			maxHeat = math.Max(maxHeat, ranked[i].heat)
		}
	}
	out := make([]model.HeatmapGroup, len(ranked))
	for i := range ranked {
		heatScore := normalizedHeat(ranked[i].heat, minHeat, maxHeat)
		ranked[i].group.Heat = heatScore * 100
		ranked[i].group.AreaWeight = math.Max(ranked[i].group.CircMV, 1)
		if groupBy == "concept" {
			// 概念面积以成分市值为基础，热度只做有限修正，防止短期波动
			// 把尾部主题放大到不可读，同时让活跃主题获得可见优势。
			ranked[i].group.AreaWeight *= 0.85 + heatScore*0.30
		}
		out[i] = ranked[i].group
	}
	return out, "", nil
}

func sqlStringList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return strings.Join(quoted, ",")
}

func normalizedHeat(value, minValue, maxValue float64) float64 {
	if maxValue <= minValue {
		return 0.5
	}
	return math.Max(0, math.Min(1, (value-minValue)/(maxValue-minValue)))
}

func conceptRankScore(totalMV, heat float64) float64 {
	return math.Log10(math.Max(totalMV, 1))*0.72 + heat*0.28
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
