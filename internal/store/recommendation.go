package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoax/go-stock/internal/model"
)

// RecommendationCandidate 包含 AI 判断所需的 60 日行情序列。
type RecommendationCandidate struct {
	Symbol   string        `json:"symbol"`
	Code     string        `json:"code"`
	Name     string        `json:"name"`
	Industry string        `json:"industry"`
	TotalMV  float64       `json:"total_mv"`
	Klines   []model.Kline `json:"klines"`
}

// RecommendationCandidates 先按行业近 60 日等权涨幅选出候选板块，再取其市值前十龙头。
func (s *Store) RecommendationCandidates(ctx context.Context) ([]RecommendationCandidate, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT history.industry,AVG((history.last_close/history.first_close-1)*100) sector_return
		FROM (
			SELECT b.symbol,b.industry,
				CAST(SUBSTRING_INDEX(GROUP_CONCAT(k.close ORDER BY k.trade_date ASC),',',1) AS DECIMAL(20,6)) first_close,
				CAST(SUBSTRING_INDEX(GROUP_CONCAT(k.close ORDER BY k.trade_date DESC),',',1) AS DECIMAL(20,6)) last_close,
				COUNT(*) kline_count
			FROM stock_basic b INNER JOIN kline_daily k ON k.symbol=b.symbol
			WHERE b.status='listed' AND b.sec_type='stock' AND b.industry<>'' AND k.trade_date>=DATE_SUB(CURDATE(),INTERVAL 100 DAY)
			GROUP BY b.symbol,b.industry
			HAVING kline_count>=50 AND first_close>0
		) history
		GROUP BY history.industry HAVING COUNT(*)>=3
		ORDER BY sector_return DESC LIMIT 3`)
	if err != nil {
		return nil, fmt.Errorf("query recommendation sectors: %w", err)
	}
	var sectors []string
	for rows.Next() {
		var name string
		var change float64
		if err := rows.Scan(&name, &change); err != nil {
			rows.Close()
			return nil, err
		}
		sectors = append(sectors, name)
	}
	rows.Close()
	if len(sectors) == 0 {
		return []RecommendationCandidate{}, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(sectors)), ",")
	args := make([]interface{}, len(sectors))
	for i := range sectors {
		args[i] = sectors[i]
	}
	query := `SELECT b.symbol,b.code,b.name,b.industry,IFNULL(ms.total_mv,0)
		FROM stock_basic b LEFT JOIN (
			SELECT snap.symbol,snap.total_mv FROM market_snapshot snap
			INNER JOIN (SELECT symbol,MAX(snapshot_at) t FROM market_snapshot GROUP BY symbol) x ON x.symbol=snap.symbol AND x.t=snap.snapshot_at
		) ms ON ms.symbol=b.symbol
		WHERE b.status='listed' AND b.sec_type='stock' AND b.industry IN (` + placeholders + `)
		ORDER BY FIELD(b.industry,` + placeholders + `),ms.total_mv DESC`
	args = append(args, args...)
	stockRows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query recommendation leaders: %w", err)
	}
	defer stockRows.Close()
	counts := map[string]int{}
	var candidates []RecommendationCandidate
	for stockRows.Next() {
		var candidate RecommendationCandidate
		if err := stockRows.Scan(&candidate.Symbol, &candidate.Code, &candidate.Name, &candidate.Industry, &candidate.TotalMV); err != nil {
			return nil, err
		}
		if counts[candidate.Industry] >= 10 {
			continue
		}
		counts[candidate.Industry]++
		klines, err := s.QueryKlines(ctx, candidate.Symbol, "day", "qfq", "", "", 60)
		if err != nil {
			return nil, err
		}
		if len(klines) >= 50 {
			candidate.Klines = klines
			candidates = append(candidates, candidate)
		}
	}
	return candidates, stockRows.Err()
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
	rows, err := s.DB.QueryContext(ctx, `SELECT DATE_FORMAT(r.analysis_date,'%Y-%m-%d'),r.rank_no,r.symbol,b.code,b.name,r.sector_name,r.probability,r.reason,r.model_name FROM stock_recommendation r INNER JOIN stock_basic b ON b.symbol=r.symbol WHERE r.analysis_date=(SELECT MAX(analysis_date) FROM stock_recommendation) ORDER BY r.rank_no LIMIT 3`)
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
