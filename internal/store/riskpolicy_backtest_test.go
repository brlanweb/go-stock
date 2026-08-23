//go:build backtest

// 风险分策略回测：对同一候选池对比「风险硬剔除（旧）」与「风险排序惩罚（新）」
// 的选股表现，验证 2026-08 改动是否真的改善结果而非拍脑袋。
//
// 直接复用包内 recommendationTrendScore / recommendationRiskScore /
// recommendationSortScore / recommendationRiskPenalty 等私有函数，
// 保证回测与生产逻辑逐字一致，不做任何复制实现。
//
// 运行：
//
//	go test -tags backtest -run TestBacktestRiskPolicy -v -timeout 30m ./internal/store/
package store

import (
	"bufio"
	"database/sql"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/hoax/go-stock/internal/model"
)

func loadDotEnv(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	f, err := os.Open("../../.env")
	if err != nil {
		t.Skipf("无 .env，跳过回测: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if p := strings.SplitN(line, "=", 2); len(p) == 2 {
			out[strings.TrimSpace(p[0])] = strings.TrimSpace(p[1])
		}
	}
	return out
}

type btBar struct {
	date  string
	open  float64
	close float64
	pct   float64
}

type btStock struct {
	symbol   string
	code     string
	industry string
	name     string
	bars     []btBar
	index    map[string]int // trade_date -> bars 下标
}

// 一次性把回测窗口内的全市场日K读进内存，避免逐股逐日查询拖垮回测。
func loadUniverse(t *testing.T, db *sql.DB, since string) map[string]*btStock {
	t.Helper()
	rows, err := db.Query(`SELECT b.symbol,b.code,b.name,COALESCE(b.industry,''),
		DATE_FORMAT(k.trade_date,'%Y-%m-%d'),k.open,k.close,COALESCE(k.change_pct,0)
		FROM kline_daily k INNER JOIN stock_basic b ON b.symbol=k.symbol
		WHERE k.trade_date>=? AND b.status='listed' AND b.sec_type='stock'
		ORDER BY b.symbol,k.trade_date`, since)
	if err != nil {
		t.Fatalf("load universe: %v", err)
	}
	defer rows.Close()
	universe := map[string]*btStock{}
	for rows.Next() {
		var symbol, code, name, industry, date string
		var open, close, pct float64
		if err := rows.Scan(&symbol, &code, &name, &industry, &date, &open, &close, &pct); err != nil {
			t.Fatalf("scan universe: %v", err)
		}
		s := universe[symbol]
		if s == nil {
			s = &btStock{symbol: symbol, code: code, name: name, industry: industry, index: map[string]int{}}
			universe[symbol] = s
		}
		s.index[date] = len(s.bars)
		s.bars = append(s.bars, btBar{date: date, open: open, close: close, pct: pct})
	}
	return universe
}

// 合规题材：成分股 5~150 且不在泛概念黑名单内（对应生产的规模闸门 + 名称黑名单）。
// 返回 symbol -> 题材名，用于板块分散约束。
func loadCompliantSectors(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT sb.sector_name,sc.symbol FROM sector_basic sb
		INNER JOIN sector_constituent sc ON sc.sector_code=sb.sector_code
		WHERE sb.sector_code IN (
			SELECT sector_code FROM sector_constituent GROUP BY sector_code
			HAVING COUNT(*) BETWEEN ? AND ?
		) ORDER BY sb.sector_code,sc.symbol`,
		recommendationSectorMinConstituents, recommendationSectorMaxConstituents)
	if err != nil {
		t.Fatalf("load sectors: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sector, symbol string
		if err := rows.Scan(&sector, &symbol); err != nil {
			t.Fatalf("scan sectors: %v", err)
		}
		if isGenericConcept(sector) {
			continue
		}
		if _, exists := out[symbol]; !exists {
			out[symbol] = sector
		}
	}
	return out
}

// toKlines 取截至 asOf（含）的最近 recommendationKlineDays 根，构造生产函数所需切片。
func (s *btStock) toKlines(asOf string) []model.Kline {
	idx, ok := s.index[asOf]
	if !ok || idx+1 < recommendationKlineDays {
		return nil
	}
	window := s.bars[idx+1-recommendationKlineDays : idx+1]
	out := make([]model.Kline, len(window))
	for i, b := range window {
		out[i] = model.Kline{Date: b.date, Open: b.open, Close: b.close, ChangePct: b.pct}
	}
	return out
}

// forwardReturn 计算 asOf 之后首个交易日开盘 -> 第 n 个交易日收盘的收益率(%)。
// 与生产计分口径（买入价=次日开盘）一致。
func (s *btStock) forwardReturn(asOf string, n int) (float64, bool) {
	idx, ok := s.index[asOf]
	if !ok || idx+n >= len(s.bars) {
		return 0, false
	}
	entry := s.bars[idx+1].open
	if entry <= 0 {
		return 0, false
	}
	return (s.bars[idx+n].close/entry - 1) * 100, true
}

type btPick struct {
	symbol string
	sector string
	risk   float64
	trend  float64
}

// selectTop 按给定排序分选出 limit 只，同一题材最多 1 只（复现生产的板块分散约束）。
func selectTop(scored map[string]float64, meta map[string]btPick, limit int) []btPick {
	symbols := make([]string, 0, len(scored))
	for s := range scored {
		symbols = append(symbols, s)
	}
	sort.Slice(symbols, func(i, j int) bool {
		if scored[symbols[i]] == scored[symbols[j]] {
			return symbols[i] < symbols[j]
		}
		return scored[symbols[i]] > scored[symbols[j]]
	})
	picks := make([]btPick, 0, limit)
	used := map[string]bool{}
	for _, s := range symbols {
		if len(picks) == limit {
			break
		}
		m := meta[s]
		if used[m.sector] {
			continue
		}
		used[m.sector] = true
		picks = append(picks, m)
	}
	return picks
}

type btStat struct {
	name    string
	n       int
	wins    int
	sum     float64
	best    float64
	worst   float64
	risks   float64
	skipped int // 候选不足而放弃的交易日数
}

func (st *btStat) add(ret, risk float64) {
	st.n++
	st.sum += ret
	st.risks += risk
	if ret > 0 {
		st.wins++
	}
	if st.n == 1 || ret > st.best {
		st.best = ret
	}
	if st.n == 1 || ret < st.worst {
		st.worst = ret
	}
}

func (st btStat) String() string {
	if st.n == 0 {
		return fmt.Sprintf("%-28s 无样本（放弃 %d 日）", st.name, st.skipped)
	}
	return fmt.Sprintf("%-28s 均值%+6.2f%% 胜率%5.1f%% 最好%+7.2f%% 最差%+7.2f%% 均风险%5.1f n=%-3d 放弃%d日",
		st.name, st.sum/float64(st.n), float64(st.wins)/float64(st.n)*100,
		st.best, st.worst, st.risks/float64(st.n), st.n, st.skipped)
}

func TestBacktestRiskPolicy(t *testing.T) {
	env := loadDotEnv(t)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&timeout=15s&readTimeout=180s",
		env["GOSTOCK_DB_USER"], env["GOSTOCK_DB_PASSWORD"],
		env["GOSTOCK_DB_HOST"], env["GOSTOCK_DB_PORT"], env["GOSTOCK_DB_NAME"])
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Skipf("数据库不可用，跳过回测: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("数据库不可达，跳过回测: %v", err)
	}

	// 回测窗口：预留 60 根日K 的形成期
	const since = "2026-03-01"
	universe := loadUniverse(t, db, since)
	sectors := loadCompliantSectors(t, db)
	t.Logf("回测universe: %d 只股票，合规题材覆盖 %d 只", len(universe), len(sectors))

	// 交易日序列取自任一样本股票，保证与真实交易日历一致
	var calendar []string
	for _, s := range universe {
		if len(s.bars) > len(calendar) {
			calendar = calendar[:0]
			for _, b := range s.bars {
				calendar = append(calendar, b.date)
			}
		}
	}
	sort.Strings(calendar)

	// 四组对照，用于判定风险分到底该怎么用（而不是预设答案）：
	//   A 纯趋势   : 不用风险分，只挡硬安全阀
	//   B 硬剔除   : 生产旧逻辑，风险分超过档位上限直接剔除
	//   C 排序惩罚 : 本次改动，软阈值以上线性降权
	//   D 低risk优先: 反向假设，风险分越低越优先
	variants := []struct {
		name  string
		score func(base, risk, soft float64) (float64, bool)
	}{
		{"A纯趋势", func(base, risk, soft float64) (float64, bool) {
			return base, risk <= recommendationHardRiskCeiling
		}},
		{"B硬剔除", func(base, risk, soft float64) (float64, bool) {
			return base, risk <= soft
		}},
		{"C排序惩罚", func(base, risk, soft float64) (float64, bool) {
			return base * recommendationRiskPenalty(risk, soft), risk <= recommendationHardRiskCeiling
		}},
		{"D低risk优先", func(base, risk, soft float64) (float64, bool) {
			return base * (1 + (100-risk)/100), risk <= recommendationHardRiskCeiling
		}},
	}

	for _, phase := range []string{"down", "range", "up"} {
		soft := RecommendationMaxRiskScore(phase)
		for _, horizon := range []int{5, 10} {
			stats := make([]btStat, len(variants))
			for i := range variants {
				stats[i].name = fmt.Sprintf("%s(%s/%d日)", variants[i].name, phase, horizon)
			}

			for _, day := range calendar {
				meta := map[string]btPick{}
				scoreSets := make([]map[string]float64, len(variants))
				for i := range scoreSets {
					scoreSets[i] = map[string]float64{}
				}

				for symbol, stock := range universe {
					sector, ok := sectors[symbol]
					if !ok || isBrokerCandidate(stock.industry, stock.name) {
						continue
					}
					klines := stock.toKlines(day)
					if klines == nil {
						continue
					}
					trend, ok := recommendationTrendScore(klines)
					if !ok {
						continue
					}
					risk, ok := recommendationRiskScore(klines)
					if !ok {
						continue
					}
					if recommendationGapRiskHigh(klines, stock.code) || recommendationOverextended(klines) {
						continue
					}
					base := recommendationSortScore(trend, klines)
					meta[symbol] = btPick{symbol: symbol, sector: sector, risk: risk, trend: trend}
					for i, v := range variants {
						if s, keep := v.score(base, risk, soft); keep {
							scoreSets[i][symbol] = s
						}
					}
				}

				for i := range variants {
					if len(scoreSets[i]) < RecommendationCandidateMin {
						stats[i].skipped++
						continue
					}
					for _, pick := range selectTop(scoreSets[i], meta, 3) {
						if ret, ok := universe[pick.symbol].forwardReturn(day, horizon); ok {
							stats[i].add(ret, pick.risk)
						}
					}
				}
			}

			for i := range stats {
				t.Logf("%s", stats[i])
			}
			t.Logf("%s", strings.Repeat("-", 100))
		}
	}
}

// 验证候选枯竭问题：统计两种规则下「候选数不足导致当日无法推荐」的天数差异。
// 这是 2026-08 回退泛概念池的直接触发条件。
func TestBacktestCandidateStarvation(t *testing.T) {
	env := loadDotEnv(t)
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&timeout=15s&readTimeout=180s",
		env["GOSTOCK_DB_USER"], env["GOSTOCK_DB_PASSWORD"],
		env["GOSTOCK_DB_HOST"], env["GOSTOCK_DB_PORT"], env["GOSTOCK_DB_NAME"]))
	if err != nil {
		t.Skipf("数据库不可用: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("数据库不可达: %v", err)
	}

	universe := loadUniverse(t, db, "2026-03-01")
	sectors := loadCompliantSectors(t, db)
	var calendar []string
	for _, s := range universe {
		if len(s.bars) > len(calendar) {
			calendar = calendar[:0]
			for _, b := range s.bars {
				calendar = append(calendar, b.date)
			}
		}
	}
	sort.Strings(calendar)

	for _, phase := range []string{"down", "range", "up"} {
		soft := RecommendationMaxRiskScore(phase)
		var oldTotal, newTotal, days int
		oldStarved, newStarved := 0, 0
		for _, day := range calendar {
			oldCount, newCount := 0, 0
			for symbol, stock := range universe {
				if _, ok := sectors[symbol]; !ok {
					continue
				}
				klines := stock.toKlines(day)
				if klines == nil {
					continue
				}
				if _, ok := recommendationTrendScore(klines); !ok {
					continue
				}
				risk, ok := recommendationRiskScore(klines)
				if !ok || recommendationGapRiskHigh(klines, stock.code) || recommendationOverextended(klines) {
					continue
				}
				if risk <= soft {
					oldCount++
				}
				if risk <= recommendationHardRiskCeiling {
					newCount++
				}
			}
			if oldCount == 0 && newCount == 0 {
				continue
			}
			days++
			oldTotal += oldCount
			newTotal += newCount
			if oldCount < RecommendationCandidateMin {
				oldStarved++
			}
			if newCount < RecommendationCandidateMin {
				newStarved++
			}
		}
		if days == 0 {
			t.Fatalf("phase=%s 无有效交易日", phase)
		}
		t.Logf("phase=%-6s 软阈值%.0f | 旧规则日均候选%.1f 枯竭%d/%d日 | 新规则日均候选%.1f 枯竭%d/%d日",
			phase, soft, float64(oldTotal)/float64(days), oldStarved, days,
			float64(newTotal)/float64(days), newStarved, days)
		if newTotal < oldTotal {
			t.Errorf("新规则候选池不应小于旧规则: old=%d new=%d", oldTotal, newTotal)
		}
	}
	_ = math.Abs
}
