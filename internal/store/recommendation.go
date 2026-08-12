package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/hoax/go-stock/internal/model"
)

const (
	recommendationSectorLimit    = 10
	recommendationCandidateLimit = 10
	recommendationKlineDays      = 60

	// RecommendationCandidateMin 是可分析候选下限：趋势与风险过滤后不足该数量
	// 说明当日可选机会太少，跳过本次推荐而不是强行凑 3 只。
	RecommendationCandidateMin = 5

	// 候选风险上限由最近一次 AI 复盘的 market_phase 自动调节（不做人工配置）：
	// up 放宽以保留强趋势机会，down 收紧以优先控制回撤，无复盘记录时取基准值。
	recommendationBaseMaxRisk  = 70.0
	recommendationMaxRiskUp    = 85.0
	recommendationMaxRiskRange = 75.0
	recommendationMaxRiskDown  = 65.0
)

// RecommendationMaxRiskScore 返回给定复盘市场阶段下的候选风险分上限：
// up=85、range=75、down=65，其余（含尚无复盘）为基准 70。
func RecommendationMaxRiskScore(marketPhase string) float64 {
	switch marketPhase {
	case "up":
		return recommendationMaxRiskUp
	case "range":
		return recommendationMaxRiskRange
	case "down":
		return recommendationMaxRiskDown
	default:
		return recommendationBaseMaxRisk
	}
}

// RecommendationCandidate 包含确定性量化评分所需的最近 60 根日 K。
type RecommendationCandidate struct {
	Symbol     string        `json:"symbol"`
	Code       string        `json:"code"`
	Name       string        `json:"name"`
	Industry   string        `json:"industry"`
	SectorType string        `json:"sector_type"`
	Popularity float64       `json:"popularity"`
	SectorHeat float64       `json:"sector_heat"`
	TrendScore float64       `json:"trend_score"`
	RiskScore  float64       `json:"risk_score"`
	Klines     []model.Kline `json:"klines"`
}

type recommendationSector struct {
	Code       string
	Type       string
	Name       string
	Popularity float64
}

// RecommendationCandidates 从行业和概念的统一热度排名取前 10 个题材，收集其
// 成分股并去重；对全部候选读取最近 60 根前复权日 K，按确定性趋势分排序后取前 10。
// maxRiskScore 是本次候选风险上限（由最近复盘 market_phase 决定，见
// RecommendationMaxRiskScore）；非法值回退到基准值。所有排序均有稳定代码兜底，
// AI 只会接收最终候选及其完整日 K 数据。
func (s *Store) RecommendationCandidates(ctx context.Context, maxRiskScore float64) ([]RecommendationCandidate, error) {
	if maxRiskScore <= 0 || maxRiskScore > 100 {
		maxRiskScore = recommendationBaseMaxRisk
	}
	tradeDate, err := s.LatestKlineDate(ctx)
	if err != nil {
		return nil, err
	}
	if tradeDate == "" {
		return []RecommendationCandidate{}, nil
	}

	sectorRows, err := s.DB.QueryContext(ctx, `
		SELECT sb.sector_code,sb.sector_type,sb.sector_name,
               LOG10(SUM(k.amount)+1)*0.45 + LOG10(SUM(COALESCE(NULLIF(d.circ_mv,0),NULLIF(ms.circ_mv,0),1))+1)*0.30 + GREATEST(AVG(k.change_pct),0)*0.25 popularity
		FROM sector_basic sb
		INNER JOIN sector_constituent sc ON sc.sector_code=sb.sector_code
		INNER JOIN stock_basic b ON b.symbol=sc.symbol
		INNER JOIN kline_daily k ON k.symbol=b.symbol AND k.trade_date=?
		LEFT JOIN daily_indicator d ON d.symbol=k.symbol AND d.trade_date=k.trade_date
		LEFT JOIN (SELECT snap.symbol,snap.circ_mv FROM market_snapshot snap INNER JOIN (SELECT symbol,MAX(snapshot_at) snapshot_at FROM market_snapshot GROUP BY symbol) latest ON latest.symbol=snap.symbol AND latest.snapshot_at=snap.snapshot_at) ms ON ms.symbol=b.symbol
		WHERE b.status='listed' AND b.sec_type='stock'
		GROUP BY sb.sector_code,sb.sector_type,sb.sector_name
		HAVING popularity>0
		ORDER BY popularity DESC,sb.sector_code ASC`, tradeDate)
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
	selected := make([]recommendationSector, 0, recommendationSectorLimit)
	for _, sector := range sectors {
		selected = append(selected, sector)
		if len(selected) == recommendationSectorLimit {
			break
		}
	}
	sectors = selected
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

	// 候选池通常约百只；只接受完整 60 根日 K 的证券，先按趋势筛选，再剔除
	// 风险过高（高波动/深回撤/短期过热）的股票，最后按趋势评分统一取前 10。
	trendCandidates := make([]RecommendationCandidate, 0, len(pool))
	for _, candidate := range pool {
		klines, err := s.QueryKlines(ctx, candidate.Symbol, "day", "qfq", "", tradeDate, recommendationKlineDays)
		if err != nil {
			return nil, err
		}
		score, ok := recommendationTrendScore(klines)
		if !ok {
			continue
		}
		risk, ok := recommendationRiskScore(klines)
		if !ok || risk > maxRiskScore {
			continue
		}
		candidate.Klines = klines
		candidate.TrendScore = score
		candidate.RiskScore = risk
		trendCandidates = append(trendCandidates, candidate)
	}
	sort.Slice(trendCandidates, func(i, j int) bool {
		if trendCandidates[i].TrendScore == trendCandidates[j].TrendScore {
			return trendCandidates[i].Symbol < trendCandidates[j].Symbol
		}
		return trendCandidates[i].TrendScore > trendCandidates[j].TrendScore
	})
	if len(trendCandidates) > recommendationCandidateLimit {
		trendCandidates = trendCandidates[:recommendationCandidateLimit]
	}
	return trendCandidates, nil
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO stock_recommendation (analysis_date,rank_no,symbol,sector_name,probability,risk_score,reason,model_name) VALUES (?,?,?,?,?,?,?,?)`, date, i+1, item.Symbol, item.Sector, item.Probability, item.RiskScore, item.Reason, modelName); err != nil {
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
	rows, err := s.DB.QueryContext(ctx, `SELECT
		DATE_FORMAT(r.analysis_date,'%Y-%m-%d'),r.rank_no,r.symbol,b.code,b.name,r.sector_name,r.probability,r.risk_score,r.reason,r.model_name
		FROM stock_recommendation r INNER JOIN stock_basic b ON b.symbol=r.symbol WHERE `+where+` ORDER BY r.rank_no LIMIT 3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.StockRecommendation
	for rows.Next() {
		var item model.StockRecommendation
		var risk sql.NullFloat64
		if err := rows.Scan(&item.Date, &item.Rank, &item.Symbol, &item.Code, &item.Name, &item.Sector, &item.Probability, &risk, &item.Reason, &item.Model); err != nil {
			return nil, err
		}
		if risk.Valid {
			item.RiskScore = &risk.Float64
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		window, err := s.recommendationWindow(ctx, out[i].Symbol, out[i].Date)
		if err != nil {
			return nil, err
		}
		out[i].EntryPrice, out[i].LatestPrice, out[i].ChangePct = recommendationPerformance(window.entryOpen, window.lastClose)
		out[i].TrackedDays = window.days
	}
	if out == nil {
		out = []model.StockRecommendation{}
	}
	return out, nil
}

// recommendationTrackWindow 是推荐收益追踪窗口：推荐日后首个交易日起 5 个交易日。
const recommendationTrackWindow = 5

type recommendationWindow struct {
	entryOpen sql.NullFloat64
	lastClose sql.NullFloat64
	days      int
}

// recommendationWindow 读取分析日之后（不含分析日）最多 5 个交易日的日 K：
// 推荐在次日盘前 08:00 生成，最早只能以次一交易日开盘价建仓，因此买入基准为
// 分析日后第一个交易日开盘价，收益终点为窗口内最后一个交易日收盘价。
// 超过窗口的新行情不参与计算，因此满 5 个交易日后结果自然冻结。
func (s *Store) recommendationWindow(ctx context.Context, symbol, analysisDate string) (recommendationWindow, error) {
	var window recommendationWindow
	rows, err := s.DB.QueryContext(ctx, `SELECT k.open,k.close FROM kline_daily k
		WHERE k.symbol=? AND k.trade_date>? ORDER BY k.trade_date ASC LIMIT ?`, symbol, analysisDate, recommendationTrackWindow)
	if err != nil {
		return window, err
	}
	defer rows.Close()
	for rows.Next() {
		var open, close sql.NullFloat64
		if err := rows.Scan(&open, &close); err != nil {
			return window, err
		}
		if window.days == 0 {
			window.entryOpen = open
		}
		window.lastClose = close
		window.days++
	}
	return window, rows.Err()
}

// RecommendationDailyPerformance 是“最近 N 个推荐日全部买入”的组合口径统计。
type RecommendationDailyPerformance struct {
	Date         string   `json:"date"`
	Stocks       int      `json:"stocks"`
	TrackedDays  int      `json:"tracked_days"`
	Finished     bool     `json:"finished"`
	SumChangePct *float64 `json:"sum_change_pct"`
	AvgChangePct *float64 `json:"avg_change_pct"`
}

// RecommendationRecentPerformance 汇总最近 days 个推荐日的窗口收益：
// 假设每只推荐股按建仓日（推荐日后首个交易日）开盘价等权买入，输出各日合计与平均涨跌点数。
func (s *Store) RecommendationRecentPerformance(ctx context.Context, days int) ([]RecommendationDailyPerformance, error) {
	if days <= 0 || days > 30 {
		days = recommendationTrackWindow
	}
	dates, err := s.RecommendationHistory(ctx, days)
	if err != nil {
		return nil, err
	}
	out := make([]RecommendationDailyPerformance, 0, len(dates))
	for _, date := range dates {
		items, err := s.RecommendationsByDate(ctx, date)
		if err != nil {
			return nil, err
		}
		summary := RecommendationDailyPerformance{Date: date, Stocks: len(items)}
		var sum float64
		var counted int
		for _, item := range items {
			if item.TrackedDays > summary.TrackedDays {
				summary.TrackedDays = item.TrackedDays
			}
			if item.ChangePct != nil {
				sum += *item.ChangePct
				counted++
			}
		}
		summary.Finished = summary.TrackedDays >= recommendationTrackWindow
		if counted > 0 {
			total := sum
			avg := sum / float64(counted)
			summary.SumChangePct = &total
			summary.AvgChangePct = &avg
		}
		out = append(out, summary)
	}
	return out, nil
}

// recommendationPerformance 将建仓日开盘价与追踪窗口内最后收盘价转换为展示数据。
// 无历史行情或建仓日价格无效时不计算收益率，前端据此显示“-”。
func recommendationPerformance(entryPrice, latestPrice sql.NullFloat64) (*float64, *float64, *float64) {
	var entry, latest, changePct *float64
	if entryPrice.Valid && entryPrice.Float64 > 0 {
		value := entryPrice.Float64
		entry = &value
	}
	if latestPrice.Valid && latestPrice.Float64 > 0 {
		value := latestPrice.Float64
		latest = &value
	}
	if entry != nil && latest != nil {
		value := (*latest - *entry) / *entry * 100
		changePct = &value
	}
	return entry, latest, changePct
}

// RecommendationStats 是 AI 趋势推荐的整体成功率评估，仅统计已冻结
// （满 5 个交易日窗口）的推荐，避免追踪中的浮动数据污染胜率。
type RecommendationStats struct {
	TotalDays     int      `json:"total_days"`
	FrozenPicks   int      `json:"frozen_picks"`
	TrackingPicks int      `json:"tracking_picks"`
	Wins          int      `json:"wins"`
	WinRate       *float64 `json:"win_rate"`
	AvgChangePct  *float64 `json:"avg_change_pct"`
	SumChangePct  *float64 `json:"sum_change_pct"`
	MedianPct     *float64 `json:"median_pct"`
	AvgWinPct     *float64 `json:"avg_win_pct"`
	AvgLossPct    *float64 `json:"avg_loss_pct"`
	BestPct       *float64 `json:"best_pct"`
	BestName      string   `json:"best_name"`
	WorstPct      *float64 `json:"worst_pct"`
	WorstName     string   `json:"worst_name"`
	DayWins       int      `json:"day_wins"`
	DayFrozen     int      `json:"day_frozen"`
	DayWinRate    *float64 `json:"day_win_rate"`
}

// RecommendationOverallStats 汇总最近 days 个推荐日的成功率评估。
// 个股口径：建仓日开盘价 → 第 5 个交易日收盘价；组合口径：单日 3 只求和。
func (s *Store) RecommendationOverallStats(ctx context.Context, days int) (RecommendationStats, error) {
	if days <= 0 || days > 365 {
		days = 60
	}
	var stats RecommendationStats
	dates, err := s.RecommendationHistory(ctx, days)
	if err != nil {
		return stats, err
	}
	stats.TotalDays = len(dates)
	var frozen []float64
	var winSum, lossSum float64
	var winCnt, lossCnt int
	for _, date := range dates {
		items, err := s.RecommendationsByDate(ctx, date)
		if err != nil {
			return stats, err
		}
		var daySum float64
		dayFrozen := true
		dayCounted := 0
		for _, item := range items {
			if item.ChangePct == nil {
				continue
			}
			if item.TrackedDays < recommendationTrackWindow {
				stats.TrackingPicks++
				dayFrozen = false
				continue
			}
			pct := *item.ChangePct
			stats.FrozenPicks++
			frozen = append(frozen, pct)
			daySum += pct
			dayCounted++
			if pct > 0 {
				stats.Wins++
				winSum += pct
				winCnt++
			} else if pct < 0 {
				lossSum += pct
				lossCnt++
			}
			if stats.BestPct == nil || pct > *stats.BestPct {
				value := pct
				stats.BestPct = &value
				stats.BestName = item.Name
			}
			if stats.WorstPct == nil || pct < *stats.WorstPct {
				value := pct
				stats.WorstPct = &value
				stats.WorstName = item.Name
			}
		}
		if dayFrozen && dayCounted > 0 {
			stats.DayFrozen++
			if daySum > 0 {
				stats.DayWins++
			}
		}
	}
	if stats.FrozenPicks > 0 {
		var sum float64
		for _, pct := range frozen {
			sum += pct
		}
		total := sum
		avg := sum / float64(stats.FrozenPicks)
		winRate := float64(stats.Wins) / float64(stats.FrozenPicks) * 100
		stats.SumChangePct = &total
		stats.AvgChangePct = &avg
		stats.WinRate = &winRate
		sort.Float64s(frozen)
		var median float64
		mid := len(frozen) / 2
		if len(frozen)%2 == 1 {
			median = frozen[mid]
		} else {
			median = (frozen[mid-1] + frozen[mid]) / 2
		}
		stats.MedianPct = &median
	}
	if winCnt > 0 {
		value := winSum / float64(winCnt)
		stats.AvgWinPct = &value
	}
	if lossCnt > 0 {
		value := lossSum / float64(lossCnt)
		stats.AvgLossPct = &value
	}
	if stats.DayFrozen > 0 {
		value := float64(stats.DayWins) / float64(stats.DayFrozen) * 100
		stats.DayWinRate = &value
	}
	return stats, nil
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

// recommendationTrendScore 只使用最近 60 个交易日的前复权收盘价。候选必须同时
// 满足价格、均线和中短期斜率上行；分数用于在热点题材成分股中稳定排序。
func recommendationTrendScore(klines []model.Kline) (float64, bool) {
	if len(klines) != recommendationKlineDays {
		return 0, false
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		if k.Close <= 0 {
			return 0, false
		}
		closes[i] = k.Close
	}
	average := func(values []float64) float64 {
		var sum float64
		for _, value := range values {
			sum += value
		}
		return sum / float64(len(values))
	}
	last := len(closes) - 1
	ma5 := average(closes[last-4:])
	ma20 := average(closes[last-19:])
	ma20Earlier := average(closes[last-24 : last-5])
	ma60 := average(closes)
	return5 := (closes[last] / closes[last-5]) - 1
	return20 := (closes[last] / closes[last-20]) - 1
	return60 := (closes[last] / closes[0]) - 1
	if closes[last] <= ma5 || ma5 <= ma20 || ma20 <= ma60 || ma20 <= ma20Earlier || return5 <= 0 || return20 <= 0 || return60 <= 0 {
		return 0, false
	}
	return return60*55 + return20*30 + return5*15 + (ma20/ma60-1)*20, true
}

func isRecommendationUptrend(klines []model.Kline) bool {
	_, ok := recommendationTrendScore(klines)
	return ok
}

// recommendationRiskScore 用最近 60 根前复权日 K 计算 0-100 的确定性风险分：
//   - 年化波动率（日收益率标准差 ×√244）：权重 40，波动 60% 记满
//   - 60 日内最大回撤：权重 45，回撤 30% 记满
//   - 短期过热（近 5 日涨幅）：权重 15，5 日涨 35% 记满
//
// 过热项权重与记满阈值刻意偏宽：趋势筛选已要求近 5/20/60 日收益全为正，
// 过热惩罚过重会系统性误杀强趋势候选；回撤权重相应上调以保持对
// 深回撤股票的排斥。分数越高风险越高；数据不完整或价格非法时返回 false。
func recommendationRiskScore(klines []model.Kline) (float64, bool) {
	if len(klines) != recommendationKlineDays {
		return 0, false
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		if k.Close <= 0 {
			return 0, false
		}
		closes[i] = k.Close
	}

	returns := make([]float64, 0, len(closes)-1)
	var sum float64
	for i := 1; i < len(closes); i++ {
		r := closes[i]/closes[i-1] - 1
		returns = append(returns, r)
		sum += r
	}
	mean := sum / float64(len(returns))
	var variance float64
	for _, r := range returns {
		variance += (r - mean) * (r - mean)
	}
	variance /= float64(len(returns))
	annualVol := math.Sqrt(variance) * math.Sqrt(244)

	var peak, maxDrawdown float64
	for _, c := range closes {
		if c > peak {
			peak = c
		}
		if dd := (peak - c) / peak; dd > maxDrawdown {
			maxDrawdown = dd
		}
	}

	last := len(closes) - 1
	gain5 := closes[last]/closes[last-5] - 1

	clamp01 := func(v float64) float64 {
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	score := clamp01(annualVol/0.60)*40 + clamp01(maxDrawdown/0.30)*45 + clamp01(gain5/0.35)*15
	return score, true
}
