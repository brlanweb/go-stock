package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// UpsertKlines 批量写入日K线（分批 multi-row upsert，控制包大小）。
func (s *Store) UpsertKlines(ctx context.Context, klines []model.Kline) error {
	const batch = 500
	for i := 0; i < len(klines); i += batch {
		end := i + batch
		if end > len(klines) {
			end = len(klines)
		}
		if err := s.upsertKlineBatch(ctx, klines[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertKlineBatch(ctx context.Context, klines []model.Kline) error {
	if len(klines) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO kline_daily (symbol,trade_date,open,high,low,close,volume,amount,change_pct,turnover_rate,adj_factor) VALUES ")
	args := make([]interface{}, 0, len(klines)*11)
	for i, k := range klines {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?)")
		args = append(args, k.Symbol, k.Date, k.Open, k.High, k.Low, k.Close, k.Volume, k.Amount, k.ChangePct, k.TurnoverRate, k.AdjFactor)
	}
	sb.WriteString(" ON DUPLICATE KEY UPDATE open=VALUES(open),high=VALUES(high),low=VALUES(low),close=VALUES(close),volume=IF(VALUES(volume)>0,VALUES(volume),volume),amount=IF(VALUES(amount)>0,VALUES(amount),amount),change_pct=VALUES(change_pct),turnover_rate=IF(VALUES(turnover_rate)>0,VALUES(turnover_rate),turnover_rate),adj_factor=VALUES(adj_factor)")
	_, err := s.DB.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return fmt.Errorf("upsert klines: %w", err)
	}
	return nil
}

// QueryKlines 查询K线（asc 排序），adjust: none/qfq。period: day/week/month（周月为日线 SQL 聚合）。
func (s *Store) QueryKlines(ctx context.Context, symbol, period, adjust string, startDate, endDate string, limit int) ([]model.Kline, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	where := "WHERE symbol=?"
	args := []interface{}{symbol}
	if startDate != "" {
		where += " AND trade_date>=?"
		args = append(args, startDate)
	}
	if endDate != "" {
		where += " AND trade_date<=?"
		args = append(args, endDate)
	}

	var query string
	switch period {
	case "week", "month":
		grp := "YEARWEEK(trade_date, 3)" // ISO 周
		if period == "month" {
			grp = "DATE_FORMAT(trade_date,'%Y-%m')"
		}
		// 周/月聚合：开=首日开，收=末日收，高低=极值，量额=求和，换手=求和，复权因子取末日
		query = fmt.Sprintf(`SELECT
			MAX(trade_date) AS trade_date,
			SUBSTRING_INDEX(GROUP_CONCAT(open ORDER BY trade_date ASC),',',1) AS open,
			MAX(high) AS high, MIN(low) AS low,
			SUBSTRING_INDEX(GROUP_CONCAT(close ORDER BY trade_date DESC),',',1) AS close,
			SUM(volume) AS volume, SUM(amount) AS amount,
			SUM(turnover_rate) AS turnover_rate,
			SUBSTRING_INDEX(GROUP_CONCAT(adj_factor ORDER BY trade_date DESC),',',1) AS adj_factor
			FROM kline_daily %s GROUP BY %s ORDER BY trade_date DESC LIMIT %d`, where, grp, limit)
	default:
		query = fmt.Sprintf("SELECT trade_date,open,high,low,close,volume,amount,change_pct,turnover_rate,adj_factor FROM kline_daily %s ORDER BY trade_date DESC LIMIT %d", where, limit)
	}

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query klines: %w", err)
	}
	defer rows.Close()

	var out []model.Kline
	for rows.Next() {
		var k model.Kline
		k.Symbol = symbol
		var date []byte
		if period == "week" || period == "month" {
			if err := rows.Scan(&date, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Amount, &k.TurnoverRate, &k.AdjFactor); err != nil {
				return nil, err
			}
		} else {
			if err := rows.Scan(&date, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Amount, &k.ChangePct, &k.TurnoverRate, &k.AdjFactor); err != nil {
				return nil, err
			}
		}
		k.Date = string(date)
		if len(k.Date) > 10 {
			k.Date = k.Date[:10]
		}
		out = append(out, k)
	}
	// 反转为 asc
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	// 前复权：close_qfq = close_raw * adj_factor / latest_adj
	if adjust == "qfq" && len(out) > 0 {
		latestAdj := out[len(out)-1].AdjFactor
		if latestAdj > 0 {
			for i := range out {
				r := out[i].AdjFactor / latestAdj
				out[i].Open *= r
				out[i].High *= r
				out[i].Low *= r
				out[i].Close *= r
			}
		}
		// 周期聚合后重算涨跌幅
		for i := 1; i < len(out); i++ {
			if out[i-1].Close > 0 {
				out[i].ChangePct = (out[i].Close - out[i-1].Close) / out[i-1].Close * 100
			}
		}
	}
	return out, rows.Err()
}

type KlineCoverageInfo struct {
	FirstDate            string
	LastDate             string
	ListDate             string
	Count                int
	HistoryStartComplete bool
	Complete             bool
}

// KlineCoverage 同时检查历史头部和最新日期，避免仅有最新一天时误判完整。
func (s *Store) KlineCoverage(ctx context.Context, symbol string) (*KlineCoverageInfo, error) {
	var first, last, list sql.NullTime
	var count int
	err := s.DB.QueryRowContext(ctx, `
		SELECT MIN(k.trade_date),MAX(k.trade_date),COUNT(k.trade_date),b.list_date
		FROM stock_basic b LEFT JOIN kline_daily k ON k.symbol=b.symbol
		WHERE b.symbol=? GROUP BY b.symbol,b.list_date`, symbol).Scan(&first, &last, &count, &list)
	if err == sql.ErrNoRows {
		return &KlineCoverageInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	info := &KlineCoverageInfo{Count: count}
	if first.Valid {
		info.FirstDate = first.Time.Format("2006-01-02")
	}
	if last.Valid {
		info.LastDate = last.Time.Format("2006-01-02")
	}
	if list.Valid {
		info.ListDate = list.Time.Format("2006-01-02")
	}
	target := latestExpectedDateForCoverage(time.Now())
	info.HistoryStartComplete = !list.Valid || list.Time.After(mustParseDate(target)) || (first.Valid && !first.Time.After(list.Time.AddDate(0, 0, 14)))
	info.Complete = info.HistoryStartComplete && info.LastDate >= target
	return info, nil
}

func latestExpectedDateForCoverage(now time.Time) string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		now = now.In(loc)
	}
	if now.Hour() < 16 {
		now = now.AddDate(0, 0, -1)
	}
	for now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		now = now.AddDate(0, 0, -1)
	}
	return now.Format("2006-01-02")
}

func mustParseDate(value string) time.Time {
	t, _ := time.Parse("2006-01-02", value)
	return t
}

// LatestKlineDate 库内最新交易日（空库返回 ""）。
func (s *Store) LatestKlineDate(ctx context.Context) (string, error) {
	var d []byte
	err := s.DB.QueryRowContext(ctx, "SELECT MAX(trade_date) FROM kline_daily").Scan(&d)
	if err != nil || d == nil {
		return "", err
	}
	date := string(d)
	if len(date) > 10 {
		date = date[:10]
	}
	return date, nil
}

// LatestKlineDateForSymbol 返回单个证券的库内最新日线日期；空历史返回空字符串。
func (s *Store) LatestKlineDateForSymbol(ctx context.Context, symbol string) (string, error) {
	var d sql.NullTime
	if err := s.DB.QueryRowContext(ctx, "SELECT MAX(trade_date) FROM kline_daily WHERE symbol=?", symbol).Scan(&d); err != nil {
		return "", err
	}
	if !d.Valid {
		return "", nil
	}
	return d.Time.Format("2006-01-02"), nil
}

// NextKlineDate 返回单个证券缺失同步的起始日期；空历史从上市日起拉取。
func (s *Store) NextKlineDate(ctx context.Context, symbol string) (string, error) {
	var d []byte
	err := s.DB.QueryRowContext(ctx, "SELECT MAX(trade_date) FROM kline_daily WHERE symbol=?", symbol).Scan(&d)
	if err != nil || d == nil {
		return "0", err
	}
	date, err := time.Parse("2006-01-02", string(d)[:10])
	if err != nil {
		return "0", err
	}
	return date.AddDate(0, 0, 1).Format("20060102"), nil
}
