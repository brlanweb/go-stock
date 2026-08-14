package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// HotspotSectorStat 是漏斗第一层使用的确定性板块统计。
type HotspotSectorStat struct {
	SectorCode   string  `json:"sector_code"`
	SectorName   string  `json:"sector_name"`
	StockCount   int     `json:"stock_count"`
	AvgChange    float64 `json:"avg_change"`
	AvgChange5D  float64 `json:"avg_change_5d"`
	AvgChange20D float64 `json:"avg_change_20d"`
	UpRatio      float64 `json:"up_ratio"`
	LimitUpCount int     `json:"limit_up_count"`
	TotalAmount  float64 `json:"total_amount"`
	AmountRatio  float64 `json:"amount_ratio"`
	AvgTurnover  float64 `json:"avg_turnover"`
	HeatScore    float64 `json:"heat_score"`
}

type HotspotRelation struct {
	FromCode  string  `json:"from_code"`
	FromName  string  `json:"from_name"`
	ToCode    string  `json:"to_code"`
	ToName    string  `json:"to_name"`
	CommonCnt int     `json:"common_count"`
	Jaccard   float64 `json:"jaccard"`
}

type HotspotStock struct {
	Symbol    string  `json:"symbol"`
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	ChangePct float64 `json:"change_pct"`
	CircMV    float64 `json:"circ_mv"`
	Amount    float64 `json:"amount"`
}

type hotspotSectorSeries struct {
	name   string
	stocks map[string][]hotspotPoint
}

type hotspotPoint struct {
	date     time.Time
	close    float64
	change   float64
	amount   float64
	turnover float64
}

// RecomputeHotspotStats 从本地日 K 生成概念板块统计，不在页面请求链路访问上游。
// 按板块分批查询：全量单条 SQL 在真实数据量下（数百概念×数万成分×120天）
// 流式读取会超过 DSN readTimeout 导致 invalid connection，分批后每批秒级完成。
func (s *Store) RecomputeHotspotStats(ctx context.Context, tradeDate string) error {
	if tradeDate == "" {
		var err error
		tradeDate, err = s.LatestKlineDate(ctx)
		if err != nil || tradeDate == "" {
			return err
		}
	}
	codeRows, err := s.DB.QueryContext(ctx, `SELECT sector_code FROM sector_basic WHERE sector_type='concept' ORDER BY sector_code`)
	if err != nil {
		return fmt.Errorf("查询概念板块列表: %w", err)
	}
	var sectorCodes []string
	for codeRows.Next() {
		var code string
		if err := codeRows.Scan(&code); err != nil {
			codeRows.Close()
			return err
		}
		sectorCodes = append(sectorCodes, code)
	}
	if err := codeRows.Close(); err != nil {
		return err
	}
	if len(sectorCodes) == 0 {
		return nil
	}

	const sectorBatch = 25
	stats := make([]HotspotSectorStat, 0, len(sectorCodes))
	for start := 0; start < len(sectorCodes); start += sectorBatch {
		end := min(start+sectorBatch, len(sectorCodes))
		batch := sectorCodes[start:end]
		batchStats, err := s.hotspotStatsForSectors(ctx, tradeDate, batch)
		if err != nil {
			return err
		}
		stats = append(stats, batchStats...)
	}
	assignHotspotHeatScores(stats)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sector_daily_stats
		(sector_code,trade_date,sector_type,stock_count,avg_change,avg_change_5d,avg_change_20d,up_ratio,limit_up_count,total_amount,amount_ratio,avg_turnover,heat_score)
		VALUES (?,?,'concept',?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE
		stock_count=VALUES(stock_count),avg_change=VALUES(avg_change),avg_change_5d=VALUES(avg_change_5d),avg_change_20d=VALUES(avg_change_20d),up_ratio=VALUES(up_ratio),limit_up_count=VALUES(limit_up_count),total_amount=VALUES(total_amount),amount_ratio=VALUES(amount_ratio),avg_turnover=VALUES(avg_turnover),heat_score=VALUES(heat_score)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, stat := range stats {
		if _, err := stmt.ExecContext(ctx, stat.SectorCode, tradeDate, stat.StockCount, stat.AvgChange, stat.AvgChange5D, stat.AvgChange20D, stat.UpRatio, stat.LimitUpCount, stat.TotalAmount, stat.AmountRatio, stat.AvgTurnover, stat.HeatScore); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// hotspotStatsForSectors 查询并聚合一批概念板块的日 K 统计。
func (s *Store) hotspotStatsForSectors(ctx context.Context, tradeDate string, batch []string) ([]HotspotSectorStat, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT sb.sector_code,sb.sector_name,k.symbol,k.trade_date,k.close,k.change_pct,k.amount,k.turnover_rate
		FROM sector_basic sb
		INNER JOIN sector_constituent sc ON sc.sector_code=sb.sector_code
		INNER JOIN stock_basic b ON b.symbol=sc.symbol AND b.status='listed' AND b.sec_type='stock'
		INNER JOIN kline_daily k ON k.symbol=sc.symbol
		WHERE sb.sector_type='concept' AND k.trade_date<=? AND k.trade_date>=DATE_SUB(?, INTERVAL 120 DAY)
		  AND sb.sector_code IN (`+sqlStringList(batch)+`)
		ORDER BY sb.sector_code,k.symbol,k.trade_date DESC`, tradeDate, tradeDate)
	if err != nil {
		return nil, fmt.Errorf("查询热点板块日K: %w", err)
	}
	defer rows.Close()
	series := make(map[string]*hotspotSectorSeries)
	for rows.Next() {
		var code, name, symbol string
		var point hotspotPoint
		if err := rows.Scan(&code, &name, &symbol, &point.date, &point.close, &point.change, &point.amount, &point.turnover); err != nil {
			return nil, err
		}
		item := series[code]
		if item == nil {
			item = &hotspotSectorSeries{name: name, stocks: make(map[string][]hotspotPoint)}
			series[code] = item
		}
		item.stocks[symbol] = append(item.stocks[symbol], point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stats := make([]HotspotSectorStat, 0, len(series))
	for code, item := range series {
		stat := HotspotSectorStat{SectorCode: code, SectorName: item.name}
		valid, up, valid5, valid20, validAmount := 0, 0, 0, 0, 0
		for _, points := range item.stocks {
			if len(points) == 0 || points[0].close <= 0 {
				continue
			}
			valid++
			latest := points[0]
			stat.AvgChange += latest.change
			stat.TotalAmount += latest.amount
			stat.AvgTurnover += latest.turnover
			if latest.change > 0 {
				up++
			}
			if latest.change >= 9.5 {
				stat.LimitUpCount++
			}
			if len(points) >= 5 && points[4].close > 0 {
				stat.AvgChange5D += (latest.close/points[4].close - 1) * 100
				valid5++
			}
			if len(points) >= 20 && points[19].close > 0 {
				stat.AvgChange20D += (latest.close/points[19].close - 1) * 100
				valid20++
			}
			var amount5, amount60 float64
			for i, p := range points {
				if i < 5 {
					amount5 += p.amount
				}
				if i < 60 {
					amount60 += p.amount
				}
			}
			if len(points) >= 20 && amount60 > 0 {
				n5, n60 := min(len(points), 5), min(len(points), 60)
				stat.AmountRatio += (amount5 / float64(n5)) / (amount60 / float64(n60))
				validAmount++
			}
		}
		if valid == 0 {
			continue
		}
		denom := float64(valid)
		stat.StockCount = valid
		stat.AvgChange /= denom
		if valid5 > 0 {
			stat.AvgChange5D /= float64(valid5)
		}
		if valid20 > 0 {
			stat.AvgChange20D /= float64(valid20)
		}
		stat.UpRatio = float64(up) / denom
		stat.AvgTurnover /= denom
		if validAmount > 0 {
			stat.AmountRatio /= float64(validAmount)
		}
		stats = append(stats, stat)
	}
	return stats, nil
}

func assignHotspotHeatScores(stats []HotspotSectorStat) {
	type metric struct{ mean, sd float64 }
	calc := func(values []float64) metric {
		if len(values) == 0 {
			return metric{}
		}
		var sum float64
		for _, value := range values {
			sum += value
		}
		mean := sum / float64(len(values))
		var variance float64
		for _, value := range values {
			variance += (value - mean) * (value - mean)
		}
		return metric{mean: mean, sd: math.Sqrt(variance/float64(len(values))) + 1e-9}
	}
	changes, changes5, breadths, volumes := make([]float64, len(stats)), make([]float64, len(stats)), make([]float64, len(stats)), make([]float64, len(stats))
	for i, stat := range stats {
		changes[i], changes5[i], breadths[i], volumes[i] = stat.AvgChange, stat.AvgChange5D, stat.UpRatio, stat.AmountRatio
	}
	m1, m5, mb, mv := calc(changes), calc(changes5), calc(breadths), calc(volumes)
	for i := range stats {
		z := func(value float64, m metric) float64 { return (value - m.mean) / m.sd }
		stats[i].HeatScore = z(stats[i].AvgChange, m1)*0.30 + z(stats[i].AvgChange5D, m5)*0.30 + z(stats[i].UpRatio, mb)*0.20 + z(stats[i].AmountRatio, mv)*0.20
	}
}

// RebuildSectorOverlaps 重建概念成分重叠关系。
func (s *Store) RebuildSectorOverlaps(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT sb.sector_code,sc.symbol FROM sector_basic sb INNER JOIN sector_constituent sc ON sc.sector_code=sb.sector_code WHERE sb.sector_type='concept' ORDER BY sb.sector_code`)
	if err != nil {
		return err
	}
	defer rows.Close()
	sets := make(map[string]map[string]struct{})
	for rows.Next() {
		var code, symbol string
		if err := rows.Scan(&code, &symbol); err != nil {
			return err
		}
		if sets[code] == nil {
			sets[code] = make(map[string]struct{})
		}
		sets[code][symbol] = struct{}{}
	}
	codes := make([]string, 0, len(sets))
	for code := range sets {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sector_overlap"); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO sector_overlap (sector_a,sector_b,common_cnt,jaccard) VALUES (?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := 0; i < len(codes); i++ {
		for j := i + 1; j < len(codes); j++ {
			common := 0
			for symbol := range sets[codes[i]] {
				if _, ok := sets[codes[j]][symbol]; ok {
					common++
				}
			}
			if common < 2 {
				continue
			}
			jaccard := float64(common) / float64(len(sets[codes[i]])+len(sets[codes[j]])-common)
			if jaccard >= 0.05 {
				if _, err := stmt.ExecContext(ctx, codes[i], codes[j], common, jaccard); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

// genericConceptNames 是泛概念黑名单的内置兜底，需与 config/hotspot_blacklist.txt
// 保持同步（有一致性测试守护）：外部文件读取失败时仅靠本列表过滤。
var (
	genericConceptNames  = []string{"融资融券", "深股通", "沪股通", "MSCI", "富时罗素", "标普", "机构重仓", "基金重仓", "证金持股", "转融券", "预盈预增", "昨日涨停", "昨日连板", "AH股", "QFII重仓", "社保重仓", "参股新三板"}
	hotspotBlacklistOnce sync.Once
)

func hotspotBlacklist() []string {
	hotspotBlacklistOnce.Do(func() {
		path := os.Getenv("GOSTOCK_HOTSPOT_BLACKLIST_FILE")
		if path == "" {
			path = "config/hotspot_blacklist.txt"
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			// 工作目录不含 config/ 时静默降级到内置列表会掩盖配置失效，必须留痕。
			slog.Warn("泛概念黑名单文件读取失败，仅使用内置兜底列表", "path", path, "err", err)
			return
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				genericConceptNames = append(genericConceptNames, line)
			}
		}
	})
	return genericConceptNames
}

func isGenericConcept(name string) bool {
	for _, keyword := range hotspotBlacklist() {
		if strings.Contains(strings.ToUpper(name), strings.ToUpper(keyword)) {
			return true
		}
	}
	return false
}

func (s *Store) HotspotCandidates(ctx context.Context, limit int) ([]HotspotSectorStat, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT st.sector_code,sb.sector_name,st.stock_count,st.avg_change,st.avg_change_5d,st.avg_change_20d,st.up_ratio,st.limit_up_count,st.total_amount,st.amount_ratio,st.avg_turnover,st.heat_score
		FROM sector_daily_stats st INNER JOIN sector_basic sb ON sb.sector_code=st.sector_code
		WHERE st.trade_date=(SELECT MAX(trade_date) FROM sector_daily_stats) AND st.stock_count BETWEEN 5 AND 150
		ORDER BY st.heat_score DESC LIMIT ?`, limit*3)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]HotspotSectorStat, 0, limit)
	for rows.Next() {
		var item HotspotSectorStat
		if err := rows.Scan(&item.SectorCode, &item.SectorName, &item.StockCount, &item.AvgChange, &item.AvgChange5D, &item.AvgChange20D, &item.UpRatio, &item.LimitUpCount, &item.TotalAmount, &item.AmountRatio, &item.AvgTurnover, &item.HeatScore); err != nil {
			return nil, err
		}
		if !isGenericConcept(item.SectorName) {
			out = append(out, item)
		}
		if len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *Store) HotspotRelations(ctx context.Context, codes []string, minJaccard float64) ([]HotspotRelation, error) {
	if len(codes) == 0 {
		return []HotspotRelation{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT o.sector_a,a.sector_name,o.sector_b,b.sector_name,o.common_cnt,o.jaccard FROM sector_overlap o INNER JOIN sector_basic a ON a.sector_code=o.sector_a INNER JOIN sector_basic b ON b.sector_code=o.sector_b WHERE o.jaccard>=? AND (o.sector_a IN (`+sqlStringList(codes)+`) OR o.sector_b IN (`+sqlStringList(codes)+`)) ORDER BY o.jaccard DESC LIMIT 200`, minJaccard)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HotspotRelation{}
	for rows.Next() {
		var item HotspotRelation
		if err := rows.Scan(&item.FromCode, &item.FromName, &item.ToCode, &item.ToName, &item.CommonCnt, &item.Jaccard); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) HotspotStocks(ctx context.Context, sectorCode string, limit int) ([]HotspotStock, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT b.symbol,b.code,b.name,k.change_pct,COALESCE(NULLIF(d.circ_mv,0),0),k.amount FROM sector_constituent sc INNER JOIN stock_basic b ON b.symbol=sc.symbol INNER JOIN kline_daily k ON k.symbol=b.symbol AND k.trade_date=(SELECT MAX(trade_date) FROM kline_daily) LEFT JOIN daily_indicator d ON d.symbol=k.symbol AND d.trade_date=k.trade_date WHERE sc.sector_code=? AND b.status='listed' ORDER BY k.change_pct DESC,k.amount DESC LIMIT ?`, sectorCode, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HotspotStock{}
	for rows.Next() {
		var item HotspotStock
		if err := rows.Scan(&item.Symbol, &item.Code, &item.Name, &item.ChangePct, &item.CircMV, &item.Amount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) HotspotStatsByCodes(ctx context.Context, codes []string) (map[string]HotspotSectorStat, error) {
	out := make(map[string]HotspotSectorStat)
	if len(codes) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT st.sector_code,sb.sector_name,st.stock_count,st.avg_change,st.avg_change_5d,st.avg_change_20d,st.up_ratio,st.limit_up_count,st.total_amount,st.amount_ratio,st.avg_turnover,st.heat_score
		FROM sector_daily_stats st INNER JOIN sector_basic sb ON sb.sector_code=st.sector_code
		WHERE st.trade_date=(SELECT MAX(trade_date) FROM sector_daily_stats) AND st.sector_code IN (`+sqlStringList(codes)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item HotspotSectorStat
		if err := rows.Scan(&item.SectorCode, &item.SectorName, &item.StockCount, &item.AvgChange, &item.AvgChange5D, &item.AvgChange20D, &item.UpRatio, &item.LimitUpCount, &item.TotalAmount, &item.AmountRatio, &item.AvgTurnover, &item.HeatScore); err != nil {
			return nil, err
		}
		out[item.SectorCode] = item
	}
	return out, rows.Err()
}

// SaveHotspotReport 追加保存一次运行的阶段结果；同日多次运行全部保留为历史记录。
func (s *Store) SaveHotspotReport(ctx context.Context, date, stage, model string, payload interface{}) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO hotspot_report (report_date,stage,payload,model) VALUES (?,?,?,?)`, date, stage, string(raw), model)
	return err
}

// HotspotRunSummary 描述一次已完成的漏斗运行（final 阶段）。
type HotspotRunSummary struct {
	ID         int64  `json:"id"`
	ReportDate string `json:"report_date"`
	Model      string `json:"model"`
	CreatedAt  string `json:"created_at"`
}

// HotspotRunHistory 返回最近的 final 运行记录列表，供前端历史选择。
func (s *Store) HotspotRunHistory(ctx context.Context, limit int) ([]HotspotRunSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,DATE_FORMAT(report_date,'%Y-%m-%d'),model,DATE_FORMAT(created_at,'%Y-%m-%d %H:%i') FROM hotspot_report WHERE stage='final' ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HotspotRunSummary{}
	for rows.Next() {
		var item HotspotRunSummary
		if err := rows.Scan(&item.ID, &item.ReportDate, &item.Model, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// HotspotReportByID 按运行 id 读取 final 报告。
func (s *Store) HotspotReportByID(ctx context.Context, id int64) (json.RawMessage, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT payload FROM hotspot_report WHERE id=? AND stage='final'`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

func (s *Store) LatestHotspotReport(ctx context.Context) (json.RawMessage, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT payload FROM hotspot_report WHERE stage='final' ORDER BY id DESC LIMIT 1`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
