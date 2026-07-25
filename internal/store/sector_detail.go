package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// SectorListItem 板块基础信息。
type SectorListItem struct {
	SectorCode string `json:"sector_code"`
	SectorName string `json:"sector_name"`
	SectorType string `json:"sector_type"`
	StockCount int    `json:"stock_count"`
}

// SectorConstituentItem 板块成分股及最近行情。
type SectorConstituentItem struct {
	Symbol     string     `json:"symbol"`
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Industry   string     `json:"industry"`
	Price      float64    `json:"price"`
	ChangePct  float64    `json:"change_pct"`
	IsTrading  bool       `json:"is_trading"`
	SnapshotAt *time.Time `json:"snapshot_at,omitempty"`
}

// ListSectors 返回行业或概念板块列表及成分股数量。
func (s *Store) ListSectors(ctx context.Context, sectorType string) ([]SectorListItem, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT sb.sector_code, sb.sector_name, sb.sector_type, COUNT(sc.symbol)
		FROM sector_basic sb
		LEFT JOIN sector_constituent sc ON sc.sector_code=sb.sector_code
		WHERE sb.sector_type=?
		GROUP BY sb.sector_code, sb.sector_name, sb.sector_type
		ORDER BY sb.sector_name`, sectorType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SectorListItem{}
	for rows.Next() {
		var item SectorListItem
		if err := rows.Scan(&item.SectorCode, &item.SectorName, &item.SectorType, &item.StockCount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ListSectorConstituents 返回某板块的成分股及最近行情快照。
// 交易日判定：快照时间在最近 14 个自然日之内，且当日是否交易日按本地快照判断为非交易窗口（周末/节假日）的，
// 这里以快照时距离最近 9 小时之内视为交易时段；否则标记 is_trading=false，列表仍按涨跌幅排序。
func (s *Store) ListSectorConstituents(ctx context.Context, sectorCode string, limit int) ([]SectorConstituentItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT b.symbol,b.code,b.name,b.industry,
		       COALESCE(ms.price,0),COALESCE(ms.change_pct,0),ms.snapshot_at
		FROM sector_constituent sc
		INNER JOIN stock_basic b ON b.symbol=sc.symbol AND b.status='listed'
		LEFT JOIN (
			SELECT snap.symbol,snap.price,snap.change_pct,snap.snapshot_at
			FROM market_snapshot snap
			INNER JOIN (SELECT symbol,MAX(snapshot_at) AS snapshot_at FROM market_snapshot GROUP BY symbol) x
			  ON x.symbol=snap.symbol AND x.snapshot_at=snap.snapshot_at
		) ms ON ms.symbol=sc.symbol
		WHERE sc.sector_code=?
		ORDER BY ms.change_pct DESC, ms.price DESC
		LIMIT ?`, sectorCode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []SectorConstituentItem{}
	for rows.Next() {
		var item SectorConstituentItem
		var snapAt *time.Time
		if err := rows.Scan(&item.Symbol, &item.Code, &item.Name, &item.Industry, &item.Price, &item.ChangePct, &snapAt); err != nil {
			return nil, err
		}
		item.SnapshotAt = snapAt
		if snapAt != nil {
			loc := time.FixedZone("CST", 8*3600)
			t := snapAt.In(loc)
			// A股交易窗口：工作日 09:30-11:30 与 13:00-15:00，按小时粗略判定。
			weekday := t.Weekday()
			hour := t.Hour()
			inSession := weekday >= time.Monday && weekday <= time.Friday &&
				((hour >= 9 && hour < 12) || (hour >= 13 && hour < 15))
			// 同时要求快照不晚于"now+12h"（避免旧历史快照仍被判为盘中）
			if inSession && now.Sub(t) < 12*time.Hour {
				item.IsTrading = true
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SectorsForSymbol 返回某证券所属行业与所属概念的 sector_code/sector_name 列表。
func (s *Store) SectorsForSymbol(ctx context.Context, symbol string) (industry, industryCode string, concepts []SectorListItem, err error) {
	row := s.DB.QueryRowContext(ctx, `SELECT industry FROM stock_basic WHERE symbol=?`, symbol)
	if err := row.Scan(&industry); err != nil {
		return "", "", nil, err
	}
	industryCode = ""
	if industry != "" {
		// 行业名 -> sector_code（取行业表中匹配的第一个）。同一行业名通常只有一条。
		_ = s.DB.QueryRowContext(ctx, `SELECT sector_code FROM sector_basic WHERE sector_type='industry' AND sector_name=? LIMIT 1`, industry).Scan(&industryCode)
	}
	if industryCode == "" {
		// 没有板块映射时直接以行业名作为展示用代码，便于前端跳转。
		industryCode = "industry:" + industry
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT sb.sector_code, sb.sector_name, sb.sector_type,
		       (SELECT COUNT(*) FROM sector_constituent sc WHERE sc.sector_code=sb.sector_code)
		FROM sector_constituent sc INNER JOIN sector_basic sb ON sb.sector_code=sc.sector_code
		WHERE sc.symbol=? AND sb.sector_type='concept'
		ORDER BY sb.sector_name`, symbol)
	if err != nil {
		return industry, industryCode, nil, err
	}
	defer rows.Close()
	concepts = []SectorListItem{}
	for rows.Next() {
		var item SectorListItem
		if err := rows.Scan(&item.SectorCode, &item.SectorName, &item.SectorType, &item.StockCount); err != nil {
			return industry, industryCode, nil, err
		}
		concepts = append(concepts, item)
	}
	if err := rows.Err(); err != nil {
		return industry, industryCode, nil, err
	}
	return industry, industryCode, concepts, nil
}

// DetailForSymbol 汇总个股用于 AI 会话的上下文：基础信息、最新快照、最近 30 个交易日日 K、最近 60 日日 K。
type DetailForSymbol struct {
	Symbol       string           `json:"symbol"`
	Code         string           `json:"code"`
	Name         string           `json:"name"`
	Industry     string           `json:"industry"`
	IndustryCode string           `json:"industry_code"`
	Concepts     []SectorListItem `json:"concepts"`
	ListDate     string           `json:"list_date"`
	Quote        *model.Quote     `json:"quote,omitempty"`
	Klines60     []model.Kline    `json:"klines_60"`
}

func (s *Store) DetailForSymbol(ctx context.Context, symbol string) (*DetailForSymbol, error) {
	var code, name, industry, listDate string
	row := s.DB.QueryRowContext(ctx, `SELECT code,name,industry,IFNULL(DATE_FORMAT(list_date,'%Y-%m-%d'),'') FROM stock_basic WHERE symbol=?`, symbol)
	if err := row.Scan(&code, &name, &industry, &listDate); err != nil {
		return nil, err
	}
	industryOnly, industryCode, concepts, err := s.SectorsForSymbol(ctx, symbol)
	if err != nil {
		return nil, err
	}
	detail := &DetailForSymbol{Symbol: symbol, Code: code, Name: name, Industry: industryOnly, IndustryCode: industryCode, Concepts: concepts, ListDate: listDate}
	if q, err := s.LatestQuote(ctx, symbol); err == nil {
		detail.Quote = q
	}
	if klines, err := s.QueryKlines(ctx, symbol, "day", "qfq", "", "", 60); err == nil {
		detail.Klines60 = klines
	}
	return detail, nil
}

// ensureTableNote unused to keep imports referenced when migrating.
var _ = fmt.Sprintf
var _ = strings.Builder{}
