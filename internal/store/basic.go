package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
)

// UpsertSecurities 批量写入证券基础信息。
func (s *Store) UpsertSecurities(ctx context.Context, snaps []provider.SecuritySnapshot) error {
	const batch = 500
	for i := 0; i < len(snaps); i += batch {
		end := i + batch
		if end > len(snaps) {
			end = len(snaps)
		}
		if err := s.upsertSecBatch(ctx, snaps[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertSecBatch(ctx context.Context, snaps []provider.SecuritySnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO stock_basic (symbol,market,code,name,sec_type,exchange,industry,list_date,last_trade_date,total_share,float_share,status) VALUES ")
	args := make([]interface{}, 0, len(snaps)*12)
	for i, sn := range snaps {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?)")
		var listDate, lastTradeDate interface{}
		if sn.ListDate != "" {
			listDate = sn.ListDate
		}
		if sn.TradeDate != "" && (sn.Volume > 0 || sn.Amount > 0) {
			lastTradeDate = sn.TradeDate
		}
		status := sn.Status
		if status == "" {
			status = "listed"
		}
		args = append(args, sn.Symbol, "cn", sn.Code, sn.Name, string(sn.SecType), sn.Exchange, sn.Industry, listDate, lastTradeDate, sn.TotalShare, sn.FloatShare, status)
	}
	sb.WriteString(" ON DUPLICATE KEY UPDATE name=VALUES(name),industry=VALUES(industry),list_date=COALESESCE_PLACEHOLDER,last_trade_date=COALESCE(VALUES(last_trade_date),last_trade_date),total_share=VALUES(total_share),float_share=VALUES(float_share),status=CASE WHEN status='listed' AND VALUES(status)='delisted' THEN status ELSE VALUES(status) END,updated_at=NOW()")
	q := strings.Replace(sb.String(), "COALESESCE_PLACEHOLDER", "COALESCE(VALUES(list_date), list_date)", 1)
	if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("upsert securities: %w", err)
	}
	return nil
}

// MarkSecuritiesMigrated 将已切换代码的旧证券停止参与回填，历史数据仍保留。
func (s *Store) MarkSecuritiesMigrated(ctx context.Context, symbols []string) (int64, error) {
	if len(symbols) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(symbols)), ",")
	args := make([]interface{}, len(symbols))
	for i, symbol := range symbols {
		args[i] = symbol
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE stock_basic b
		 LEFT JOIN (SELECT symbol,MAX(trade_date) last_date FROM kline_daily GROUP BY symbol) k ON k.symbol=b.symbol
		 SET b.status='delisted',b.last_trade_date=COALESCE(k.last_date,b.last_trade_date),b.updated_at=NOW()
		 WHERE b.symbol IN (`+placeholders+")", args...)
	if err != nil {
		return 0, fmt.Errorf("mark migrated securities: %w", err)
	}
	return res.RowsAffected()
}

// MarkSecuritiesWithStaleTradeDateDelisted 将上游明确给出旧交易日期、且本地已有历史达到该日期的
// 股票标记为退市。使用 180 天阈值避免把短期停牌证券误判为退市；ETF 不使用此规则。
func (s *Store) MarkSecuritiesWithStaleTradeDateDelisted(ctx context.Context, targetDate string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE stock_basic b
		INNER JOIN (SELECT symbol,MAX(trade_date) last_date FROM kline_daily GROUP BY symbol) k ON k.symbol=b.symbol
		SET b.status='delisted',b.last_trade_date=k.last_date,b.updated_at=NOW()
		WHERE b.status='listed' AND b.sec_type='stock'
		  AND k.last_date<DATE_SUB(?, INTERVAL 180 DAY)
		  AND (b.last_trade_date IS NULL OR b.last_trade_date<DATE_SUB(?, INTERVAL 180 DAY))`, targetDate, targetDate)
	if err != nil {
		return 0, fmt.Errorf("mark stale securities delisted: %w", err)
	}
	return res.RowsAffected()
}

// NormalizeConvertibleBondSecurities 修正被上游误标为 stock 的定向可转债。
//
// 深交所定向可转债使用 810xxx 代码段、名称以「定转」结尾（如优机定转），
// 上游全市场列表把它们与股票混在一起返回，导致：
//  1. 走股票回填链路，但日 K 接口对该类标的不返回日期列，akshare 直接抛
//     'date' KeyError，每轮必失败且永远无法成功；
//  2. 计入股票池，污染同步失败计数。
//
// 归类为 bond 后即退出 ClaimPending（要求 sec_type='stock'）与推荐候选池。
func (s *Store) NormalizeConvertibleBondSecurities(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE stock_basic SET sec_type='bond',updated_at=NOW()
		WHERE sec_type='stock' AND (code LIKE '810%' OR name LIKE '%定转')`)
	if err != nil {
		return 0, fmt.Errorf("normalize convertible bond securities: %w", err)
	}
	return res.RowsAffected()
}

// MarkDelistedByStaleFailure 收敛「上游已无数据 + 本地历史长期停滞」的证券。
//
// 与 MarkSecuritiesWithStaleTradeDateDelisted 的 180 天兜底相比，这条规则针对的是
// 已经明确失败、且上游返回“无数据可解析”的标的：此类标的继续留在 listed 只会每轮
// 重复请求并失败。判定需同时满足：
//   - 断点处于 failed，且错误信息属于上游无数据签名（noDataErrorPatterns）；
//   - 最后一根日 K 早于 lagTradingDays 个交易日之前（按库内真实交易日计数，
//     不用自然日，避免长假误判）。
//
// 该标记不是单向门：证券若恢复交易，上游列表会重新给出行情，UpsertSecurities
// 会把状态改回 listed，下一轮即恢复同步。
func (s *Store) MarkDelistedByStaleFailure(ctx context.Context, task, targetDate string, lagTradingDays int) (int64, error) {
	if lagTradingDays <= 0 {
		return 0, nil
	}
	conds := make([]string, 0, len(noDataErrorPatterns))
	args := []interface{}{task}
	for _, pattern := range noDataErrorPatterns {
		conds = append(conds, "cp.last_error LIKE ?")
		args = append(args, "%"+pattern+"%")
	}
	// LIMIT 取 lag+1 根交易日再取最小值，得到「lagTradingDays 个交易日之前」的界限。
	args = append(args, targetDate, lagTradingDays+1)
	res, err := s.DB.ExecContext(ctx, `
		UPDATE stock_basic b
		INNER JOIN (SELECT symbol,MAX(trade_date) last_date FROM kline_daily GROUP BY symbol) k
			ON k.symbol=b.symbol
		INNER JOIN sync_checkpoint cp ON cp.symbol=b.symbol AND cp.task=? AND cp.status='failed'
		SET b.status='delisted',b.last_trade_date=k.last_date,b.updated_at=NOW()
		WHERE b.status='listed' AND b.sec_type='stock'
		  AND (`+strings.Join(conds, " OR ")+`)
		  AND k.last_date < (
			SELECT MIN(d) FROM (
				SELECT DISTINCT trade_date d FROM kline_daily WHERE trade_date<=? ORDER BY d DESC LIMIT ?
			) recent
		  )`, args...)
	if err != nil {
		return 0, fmt.Errorf("mark delisted by stale failure: %w", err)
	}
	return res.RowsAffected()
}

// MarkDelistedByName 按退市整理期命名规范识别退市股票：沪市"退市XX"、深市/北交所
// "XX退"。上游全市场列表在摘牌后一段时间仍包含这些代码，而 180 天兜底阈值尚未触发，
// 若不提前收敛会反复请求上游并失败。
func (s *Store) MarkDelistedByName(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE stock_basic b
		LEFT JOIN (SELECT symbol,MAX(trade_date) last_date FROM kline_daily GROUP BY symbol) k ON k.symbol=b.symbol
		SET b.status='delisted',b.last_trade_date=COALESCE(k.last_date,b.last_trade_date),b.updated_at=NOW()
		WHERE b.status='listed' AND b.sec_type='stock'
		  AND (b.name LIKE '退市%' OR b.name LIKE '%退')`)
	if err != nil {
		return 0, fmt.Errorf("mark delisted by name: %w", err)
	}
	return res.RowsAffected()
}

// NormalizeInactiveLastTradeDates 以本地已保存的最后一根日 K 作为非在市证券的
// 实际覆盖终点。退市、转板旧代码不应继续追赶全市场最近交易日。
func (s *Store) NormalizeInactiveLastTradeDates(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE stock_basic b
		INNER JOIN (
			SELECT symbol,MAX(trade_date) last_date
			FROM kline_daily
			GROUP BY symbol
		) k ON k.symbol=b.symbol
		SET b.last_trade_date=k.last_date,b.updated_at=NOW()
		WHERE b.status<>'listed'
		  AND (b.last_trade_date IS NULL OR b.last_trade_date<>k.last_date)`)
	if err != nil {
		return 0, fmt.Errorf("normalize inactive last trade dates: %w", err)
	}
	return res.RowsAffected()
}

// MarkMissingListedSecuritiesDelisted 仅在本轮列表完整时调用：未出现在当前上游清单、此前仍标为 listed 的证券
// 转为 delisted，并保留库内最后交易日作为历史覆盖终点。
func (s *Store) MarkMissingListedSecuritiesDelisted(ctx context.Context, currentSymbols []string) (int64, error) {
	if len(currentSymbols) == 0 {
		return 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TEMPORARY TABLE current_security_symbols (
		symbol VARCHAR(24) NOT NULL PRIMARY KEY
	) ENGINE=MEMORY DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci`); err != nil {
		return 0, err
	}
	const batch = 500
	for start := 0; start < len(currentSymbols); start += batch {
		end := start + batch
		if end > len(currentSymbols) {
			end = len(currentSymbols)
		}
		var query strings.Builder
		query.WriteString("INSERT IGNORE INTO current_security_symbols(symbol) VALUES ")
		args := make([]interface{}, 0, end-start)
		for i, symbol := range currentSymbols[start:end] {
			if i > 0 {
				query.WriteByte(',')
			}
			query.WriteString("(?)")
			args = append(args, symbol)
		}
		if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE stock_basic b
		LEFT JOIN current_security_symbols current ON current.symbol=b.symbol
		LEFT JOIN (SELECT symbol,MAX(trade_date) last_date FROM kline_daily GROUP BY symbol) k ON k.symbol=b.symbol
		SET b.status='delisted',b.last_trade_date=COALESCE(k.last_date,b.last_trade_date),b.updated_at=NOW()
		WHERE b.market='cn' AND b.status='listed' AND current.symbol IS NULL`)
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// ListSecurities 证券列表（可按类型过滤）。
func (s *Store) ListSecurities(ctx context.Context, secType string) ([]model.Security, error) {
	q := "SELECT symbol,market,code,name,sec_type,exchange,industry,IFNULL(DATE_FORMAT(list_date,'%Y-%m-%d'),''),total_share,float_share,status FROM stock_basic WHERE status='listed'"
	var args []interface{}
	if secType != "" {
		q += " AND sec_type=?"
		args = append(args, secType)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Security
	for rows.Next() {
		var sec model.Security
		var market, secT string
		if err := rows.Scan(&sec.Symbol, &market, &sec.Code, &sec.Name, &secT, &sec.Exchange, &sec.Industry, &sec.ListDate, &sec.TotalShare, &sec.FloatShare, &sec.Status); err != nil {
			return nil, err
		}
		sec.Market = model.Market(market)
		sec.Type = model.SecurityType(secT)
		out = append(out, sec)
	}
	return out, rows.Err()
}

// SearchSecurities 按代码前缀或名称模糊搜索。
func (s *Store) SearchSecurities(ctx context.Context, keyword string, limit int) ([]model.Security, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return nil, nil
	}
	q := `SELECT symbol,code,name,sec_type,exchange,industry FROM stock_basic
		WHERE status='listed' AND (code LIKE ? OR name LIKE ? OR symbol LIKE ?)
		ORDER BY CASE WHEN code=? THEN 0 WHEN code LIKE ? THEN 1 ELSE 2 END, code LIMIT ?`
	like := kw + "%"
	rows, err := s.DB.QueryContext(ctx, q, like, "%"+kw+"%", strings.ToUpper(like), kw, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Security
	for rows.Next() {
		var sec model.Security
		var secT string
		if err := rows.Scan(&sec.Symbol, &sec.Code, &sec.Name, &secT, &sec.Exchange, &sec.Industry); err != nil {
			return nil, err
		}
		sec.Type = model.SecurityType(secT)
		sec.Market = model.MarketCN
		out = append(out, sec)
	}
	return out, rows.Err()
}

// GetSecurity 单只基础信息。
func (s *Store) GetSecurity(ctx context.Context, symbol string) (*model.Security, error) {
	var sec model.Security
	var market, secT string
	err := s.DB.QueryRowContext(ctx,
		"SELECT symbol,market,code,name,sec_type,exchange,industry,IFNULL(DATE_FORMAT(list_date,'%Y-%m-%d'),''),total_share,float_share,status FROM stock_basic WHERE symbol=?",
		symbol).Scan(&sec.Symbol, &market, &sec.Code, &sec.Name, &secT, &sec.Exchange, &sec.Industry, &sec.ListDate, &sec.TotalShare, &sec.FloatShare, &sec.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sec.Market = model.Market(market)
	sec.Type = model.SecurityType(secT)
	return &sec, nil
}

// UpsertDailyIndicators 批量写入每日指标快照。
func (s *Store) UpsertDailyIndicators(ctx context.Context, date string, snaps []provider.SecuritySnapshot) error {
	const batch = 500
	for i := 0; i < len(snaps); i += batch {
		end := i + batch
		if end > len(snaps) {
			end = len(snaps)
		}
		if err := s.upsertIndicatorBatch(ctx, date, snaps[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upsertIndicatorBatch(ctx context.Context, date string, snaps []provider.SecuritySnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO daily_indicator (symbol,trade_date,close,pe_ratio,pb_ratio,total_mv,circ_mv,turnover_rate,volume_ratio) VALUES ")
	args := make([]interface{}, 0, len(snaps)*9)
	for i, sn := range snaps {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?)")
		args = append(args, sn.Symbol, date, sn.Price, sn.PERatio, sn.PBRatio, sn.TotalMV, sn.CircMV, sn.TurnoverRate, sn.VolumeRatio)
	}
	sb.WriteString(" ON DUPLICATE KEY UPDATE close=VALUES(close),pe_ratio=VALUES(pe_ratio),pb_ratio=VALUES(pb_ratio),total_mv=VALUES(total_mv),circ_mv=VALUES(circ_mv),turnover_rate=VALUES(turnover_rate),volume_ratio=VALUES(volume_ratio)")
	if _, err := s.DB.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("upsert indicators: %w", err)
	}
	return nil
}

// QueryDailyIndicators 查询某只股票的历史指标。
func (s *Store) QueryDailyIndicators(ctx context.Context, symbol string, startDate, endDate string, limit int) ([]model.DailyIndicator, error) {
	if limit <= 0 || limit > 2000 {
		limit = 250
	}
	q := "SELECT DATE_FORMAT(trade_date,'%Y-%m-%d'),close,pe_ratio,pb_ratio,total_mv,circ_mv,turnover_rate,volume_ratio FROM daily_indicator WHERE symbol=?"
	args := []interface{}{symbol}
	if startDate != "" {
		q += " AND trade_date>=?"
		args = append(args, startDate)
	}
	if endDate != "" {
		q += " AND trade_date<=?"
		args = append(args, endDate)
	}
	q += fmt.Sprintf(" ORDER BY trade_date DESC LIMIT %d", limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DailyIndicator
	for rows.Next() {
		var di model.DailyIndicator
		di.Symbol = symbol
		if err := rows.Scan(&di.Date, &di.Close, &di.PERatio, &di.PBRatio, &di.TotalMV, &di.CircMV, &di.TurnoverRate, &di.VolumeRatio); err != nil {
			return nil, err
		}
		out = append(out, di)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}
