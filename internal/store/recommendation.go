package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hoax/go-stock/internal/model"
)

const (
	recommendationSectorLimit    = 10
	recommendationCandidateLimit = 10
)

// RecommendationCandidate 包含确定性量化评分所需的最近 60 根日 K。
type RecommendationCandidate struct {
	Symbol     string        `json:"symbol"`
	Code       string        `json:"code"`
	Name       string        `json:"name"`
	Industry   string        `json:"industry"`
	SectorType string        `json:"sector_type"`
	Popularity float64       `json:"popularity"`
	SectorHeat float64       `json:"sector_heat"`
	Klines     []model.Kline `json:"klines"`
}

type recommendationSector struct {
	Code       string
	Type       string
	Name       string
	Popularity float64
}

// RecommendationCandidates 先从行业和概念中选出成交额最高的人气板块，再从
// 这些板块按最近交易日成交额选股。股票跨板块去重后严格保留前 10 只，最后
// 为每只股票加载最近 60 根日 K。所有排序均有稳定代码兜底。
func (s *Store) RecommendationCandidates(ctx context.Context) ([]RecommendationCandidate, error) {
	tradeDate, err := s.LatestKlineDate(ctx)
	if err != nil {
		return nil, err
	}
	if tradeDate == "" {
		return []RecommendationCandidate{}, nil
	}

	sectorRows, err := s.DB.QueryContext(ctx, `
		SELECT sb.sector_code,sb.sector_type,sb.sector_name,SUM(k.amount) popularity
		FROM sector_basic sb
		INNER JOIN sector_constituent sc ON sc.sector_code=sb.sector_code
		INNER JOIN stock_basic b ON b.symbol=sc.symbol
		INNER JOIN kline_daily k ON k.symbol=b.symbol AND k.trade_date=?
		WHERE b.status='listed' AND b.sec_type='stock'
		GROUP BY sb.sector_code,sb.sector_type,sb.sector_name
		HAVING popularity>0
		ORDER BY popularity DESC,sb.sector_type ASC,sb.sector_code ASC
		LIMIT ?`, tradeDate, recommendationSectorLimit)
	if err != nil {
		return nil, fmt.Errorf("query popular recommendation sectors: %w", err)
	}
	var sectors []recommendationSector
	for sectorRows.Next() {
		var sector recommendationSector
		if err := sectorRows.Scan(&sector.Code, &sector.Type, &sector.Name, &sector.Popularity); err != nil {
			sectorRows.Close()
			return nil, err
		}
		sectors = append(sectors, sector)
	}
	if err := sectorRows.Close(); err != nil {
		return nil, err
	}
	if len(sectors) == 0 {
		return []RecommendationCandidate{}, nil
	}

	sectorByCode := make(map[string]recommendationSector, len(sectors))
	placeholders := strings.TrimRight(strings.Repeat("?,", len(sectors)), ",")
	args := make([]any, 0, len(sectors)+1)
	args = append(args, tradeDate)
	for _, sector := range sectors {
		sectorByCode[sector.Code] = sector
		args = append(args, sector.Code)
	}
	stockRows, err := s.DB.QueryContext(ctx, `
		SELECT b.symbol,b.code,b.name,sb.sector_code,k.amount
		FROM stock_basic b
		INNER JOIN sector_constituent sc ON sc.symbol=b.symbol
		INNER JOIN sector_basic sb ON sb.sector_code=sc.sector_code
		INNER JOIN kline_daily k ON k.symbol=b.symbol AND k.trade_date=?
		WHERE b.status='listed' AND b.sec_type='stock' AND sb.sector_code IN (`+placeholders+`)
		ORDER BY k.amount DESC,b.symbol ASC,sb.sector_code ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("query popular recommendation stocks: %w", err)
	}
	defer stockRows.Close()

	// 同一股票可能同时属于多个热门行业/概念，保留人气更高的所属板块。
	bySymbol := make(map[string]RecommendationCandidate)
	sectorCodeBySymbol := make(map[string]string)
	for stockRows.Next() {
		var candidate RecommendationCandidate
		var sectorCode string
		if err := stockRows.Scan(&candidate.Symbol, &candidate.Code, &candidate.Name, &sectorCode, &candidate.Popularity); err != nil {
			return nil, err
		}
		sector := sectorByCode[sectorCode]
		candidate.Industry = sector.Name
		candidate.SectorType = sector.Type
		candidate.SectorHeat = sector.Popularity
		current, exists := bySymbol[candidate.Symbol]
		if !exists || candidate.SectorHeat > current.SectorHeat ||
			(candidate.SectorHeat == current.SectorHeat && sectorCode < sectorCodeBySymbol[candidate.Symbol]) {
			bySymbol[candidate.Symbol] = candidate
			sectorCodeBySymbol[candidate.Symbol] = sectorCode
		}
	}
	if err := stockRows.Err(); err != nil {
		return nil, err
	}

	pool := make([]RecommendationCandidate, 0, len(bySymbol))
	for _, candidate := range bySymbol {
		pool = append(pool, candidate)
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].Popularity == pool[j].Popularity {
			return pool[i].Symbol < pool[j].Symbol
		}
		return pool[i].Popularity > pool[j].Popularity
	})

	candidates := make([]RecommendationCandidate, 0, recommendationCandidateLimit)
	for _, candidate := range pool {
		klines, err := s.QueryKlines(ctx, candidate.Symbol, "day", "qfq", "", tradeDate, 60)
		if err != nil {
			return nil, err
		}
		if len(klines) != 60 {
			continue
		}
		candidate.Klines = klines
		candidates = append(candidates, candidate)
		if len(candidates) == recommendationCandidateLimit {
			break
		}
	}
	return candidates, nil
}

func (s *Store) ReplaceRecommendations(ctx context.Context, date, modelName string, items []model.StockRecommendation) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM stock_recommendation WHERE analysis_date=?", date); err != nil {
		return err
	}
	for i, item := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO stock_recommendation (analysis_date,rank_no,symbol,sector_name,probability,reason,model_name) VALUES (?,?,?,?,?,?,?)`, date, i+1, item.Symbol, item.Sector, item.Probability, item.Reason, modelName); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LatestRecommendations(ctx context.Context) ([]model.StockRecommendation, error) {
	return s.RecommendationsByDate(ctx, "")
}

func (s *Store) RecommendationsByDate(ctx context.Context, date string) ([]model.StockRecommendation, error) {
	where := "r.analysis_date=(SELECT MAX(analysis_date) FROM stock_recommendation)"
	var args []interface{}
	if date != "" {
		where = "r.analysis_date=?"
		args = append(args, date)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT DATE_FORMAT(r.analysis_date,'%Y-%m-%d'),r.rank_no,r.symbol,b.code,b.name,r.sector_name,r.probability,r.reason,r.model_name FROM stock_recommendation r INNER JOIN stock_basic b ON b.symbol=r.symbol WHERE `+where+` ORDER BY r.rank_no LIMIT 3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.StockRecommendation
	for rows.Next() {
		var item model.StockRecommendation
		if err := rows.Scan(&item.Date, &item.Rank, &item.Symbol, &item.Code, &item.Name, &item.Sector, &item.Probability, &item.Reason, &item.Model); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if out == nil {
		out = []model.StockRecommendation{}
	}
	return out, rows.Err()
}

func (s *Store) RecommendationHistory(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 3650 {
		limit = 90
	}
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf("SELECT DATE_FORMAT(analysis_date,'%%Y-%%m-%%d') FROM stock_recommendation GROUP BY analysis_date ORDER BY analysis_date DESC LIMIT %d", limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	dates := make([]string, 0)
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			return nil, err
		}
		dates = append(dates, date)
	}
	return dates, rows.Err()
}
