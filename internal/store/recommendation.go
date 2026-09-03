package store

import (
	"context"
	"database/sql"
	"errors"
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

	// RecommendationProductionMaxRisk 是生产链路的风险硬否决线：确定性风险分
	// ≥ 该值的候选在进入 AI 评审前直接剔除，不参与任何排序权衡。
	//
	// 该规则是 2026-09 复盘的结论。此前设计把 RiskScore 降级为「仅展示」，
	// 禁止其参与筛选与否决，导致系统能识别风险却无权规避：2026-09-02 三只
	// 推荐 risk 分别为 86/71/57、理由中均写明「指数红灯，系统性风险高」，
	// 仍被强制推出。75 天全量复盘显示 risk>=70 分桶是唯一负超额分桶
	// （5 日超额 -0.19%）。风险分回归为硬闸门，而非排序维度：
	// 排序仍由趋势/龙头决定（见 recommendationCandidateSortScore），
	// 风险只做二值否决，避免高风险候选靠高趋势分把自己"补回来"。
	RecommendationProductionMaxRisk = 75.0

	// 以下阈值仅保留给历史回测对照与旧接口兼容，不参与生产推荐链路。
	recommendationBaseMaxRisk  = 70.0
	recommendationMaxRiskUp    = 85.0
	recommendationMaxRiskRange = 75.0
	recommendationMaxRiskDown  = 65.0

	// 历史风险策略回测参数；生产推荐不再调用 recommendationRiskPenalty，
	// 也不再应用硬风险上限。
	recommendationHardRiskCeiling = 92.0
	recommendationRiskMaxPenalty  = 0.35

	// 计分口径的买入价是分析日后首个交易日开盘价，昨日封板/近端暴涨的候选
	// 高开损耗最大：涨停判定按“昨日涨幅 ≥ 交易所涨跌幅上限 × 0.93”近似，
	// 过热降权对近 5 日涨幅超过 15% 的候选做排序惩罚（35% 处惩罚封顶一半）。
	recommendationLimitUpRatio       = 0.93
	recommendationOverheatFloor      = 0.15
	recommendationOverheatCeiling    = 0.35
	recommendationOverheatMaxPenalty = 0.5

	// 短线追高硬过滤：趋势筛选要求 5/20/60 日收益全为正，天然偏向已大涨的
	// 候选；仅靠排序降权仍会让乖离极大的候选进入推荐（计分买入价是次日开盘，
	// 追高候选高开低走损耗最大）。以下两条为硬剔除，不参与排序权衡：
	//   - 近 5 日涨幅超过 15%；
	//   - 收盘价高于 MA5 超过 8%（短期乖离过大，均值回归风险高）。
	//
	// 阈值 2026-09 由 25% 收紧至 15%。触发动机：75 条推荐的全量复盘显示，
	// 推荐股在推荐前 5 日平均已上涨 11.23%，而全市场同期中位数仅 1.92%
	// （超出 9.31pct），存在系统性买在动量末端的倾向。
	//
	// 但必须如实记录：该阈值目前【没有收益层面的实证支持】。
	// 对同一批样本做阈值敏感性分析（放行组相对全市场中位数的超额）：
	//
	//	阈值    放行/拦截   D+1      D+5      D+10
	//	10%     49/26     -0.92    +0.09    +5.99
	//	15%     57/18     -0.73    +0.13    +6.11
	//	20%     61/14     -0.63    +0.35    +5.84
	//	25%     66/ 9     -0.64    +0.15    +4.63
	//	不过滤   75/ 0     -0.91    +0.80    +5.53
	//
	// D+5 口径下「不过滤」反而最优；按前 5 日涨幅分桶后，后续超额与涨幅
	// 之间不存在单调关系（5-10% 桶 +3.14%，20-30% 桶 -2.26%，>50% 桶
	// +8.58%），各桶样本仅 3~22 条，基本是噪声。
	//
	// 因此该值应理解为【波动/尾部风险控制】而非收益增强：它降低了单笔最大
	// 回撤暴露，代价是拦掉约 24% 的候选（含数个大幅盈利样本）。
	// 在完成覆盖完整下跌周期的回测前，不要把它当成已验证的收益规则，
	// 也不要在缺乏新证据时进一步收紧。调整前请先跑回测与影子基线对照。
	recommendationHardOverheat5 = 0.15
	recommendationMaxBiasMA5    = 0.08

	// 题材成分股规模闸门（与 HotspotCandidates 的 5~150 口径一致）：
	// 低于下限说明样本太少不足以形成题材效应，高于上限基本都是「融资融券 /
	// 深股通 / 央国企改革 / 昨日高振幅」这类覆盖全市场的统计标签。
	recommendationSectorMinConstituents = 5
	recommendationSectorMaxConstituents = 150

	// 龙头加权：落实「热点板块 → 板块龙头 → 优质个股」的选择链路。
	// 同一热点板块内按当日成交额排名，第 1 名视为人气龙头加权 10%，
	// 第 2-3 名加权 5%；仅影响排序，不放宽任何硬过滤。
	recommendationLeaderRankLimit = 3
	recommendationLeaderBoostTop1 = 1.10
	recommendationLeaderBoostTopN = 1.05
)

// isBrokerCandidate 判断候选是否为券商类股票（或所属板块为证券类题材）。
// 券商股受市场 beta 与政策驱动，个股趋势持续性差，按产品要求全量排除推荐。
func isBrokerCandidate(industry, name string) bool {
	for _, keyword := range []string{"证券", "券商"} {
		if strings.Contains(industry, keyword) || strings.Contains(name, keyword) {
			return true
		}
	}
	return false
}

// recommendationExcludeBrokerSQL 是成分股查询共用的券商排除条件（东财口径下
// 券商的申万/东财行业名均含“证券”，个别互金券商用名称兜底）。
const recommendationExcludeBrokerSQL = " AND b.industry NOT LIKE '%证券%' AND b.name NOT LIKE '%证券%' "

// RecommendationMaxRiskScore 仅供历史风险策略回测与旧接口兼容测试使用。
// 生产推荐不调用该函数，也不存在按市场阶段变化的个股风险阈值。
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
	LeaderRank int           `json:"leader_rank"`
	Klines     []model.Kline `json:"klines"`
}

type recommendationSector struct {
	Code       string
	Type       string
	Name       string
	Popularity float64
}

// selectRecommendationSectors 从热度降序的题材列表中取前 N 个可用题材。
// 融资融券/深股通等泛概念成分股数千只，成交额与市值求和后必然霸榜，但它们
// 不代表真实题材、贴到个股上会误导决策，因此与热点漏斗共用黑名单剔除。
func selectRecommendationSectors(sectors []recommendationSector) []recommendationSector {
	selected := make([]recommendationSector, 0, recommendationSectorLimit)
	for _, sector := range sectors {
		if isGenericConcept(sector.Name) {
			continue
		}
		selected = append(selected, sector)
		if len(selected) == recommendationSectorLimit {
			break
		}
	}
	return selected
}

// RecommendationCandidates 从行业和概念的统一热度排名取前 10 个题材，收集其
// 成分股并去重；对全部候选读取最近 60 根前复权日 K，按确定性趋势、建仓空间
// 与板块内龙头地位排序后取前 10。RiskScore 仅随候选传给前端展示，不参与
// 过滤、排序或 AI 选举。所有排序均有稳定代码兜底。
func (s *Store) RecommendationCandidates(ctx context.Context) ([]RecommendationCandidate, error) {
	tradeDate, err := s.LatestKlineDate(ctx)
	if err != nil {
		return nil, err
	}
	if tradeDate == "" {
		return []RecommendationCandidate{}, nil
	}

	// 成分股规模闸门与热点漏斗（HotspotCandidates）保持同一口径：
	// 融资融券(3838)/深股通(1875)/央国企改革(1438)/昨日高振幅(672)/百元股(201)
	// 这类纯统计标签成分股数以千计，成交额与市值求和后必然霸榜人气排名，
	// 但贴到个股上没有任何产业逻辑。仅靠名称黑名单无法穷举，改用规模判据兜底。
	sectorRows, err := s.DB.QueryContext(ctx, `
		SELECT sb.sector_code,sb.sector_type,sb.sector_name,
               LOG10(SUM(k.amount)+1)*0.45 + LOG10(SUM(COALESCE(NULLIF(d.circ_mv,0),NULLIF(ms.circ_mv,0),1))+1)*0.30 + GREATEST(AVG(k.change_pct),0)*0.25 popularity,
               COUNT(DISTINCT b.symbol) constituent_count
		FROM sector_basic sb
		INNER JOIN sector_constituent sc ON sc.sector_code=sb.sector_code
		INNER JOIN stock_basic b ON b.symbol=sc.symbol
		INNER JOIN kline_daily k ON k.symbol=b.symbol AND k.trade_date=?
		LEFT JOIN daily_indicator d ON d.symbol=k.symbol AND d.trade_date=k.trade_date
		LEFT JOIN (SELECT snap.symbol,snap.circ_mv FROM market_snapshot snap INNER JOIN (SELECT symbol,MAX(snapshot_at) snapshot_at FROM market_snapshot GROUP BY symbol) latest ON latest.symbol=snap.symbol AND latest.snapshot_at=snap.snapshot_at) ms ON ms.symbol=b.symbol
		WHERE b.status='listed' AND b.sec_type='stock'
		GROUP BY sb.sector_code,sb.sector_type,sb.sector_name
		HAVING popularity>0 AND constituent_count BETWEEN ? AND ?
		ORDER BY popularity DESC,sb.sector_code ASC`, tradeDate, recommendationSectorMinConstituents, recommendationSectorMaxConstituents)
	if err != nil {
		return nil, fmt.Errorf("query popular recommendation sectors: %w", err)
	}
	var sectors []recommendationSector
	for sectorRows.Next() {
		var sector recommendationSector
		var constituentCount int
		if err := sectorRows.Scan(&sector.Code, &sector.Type, &sector.Name, &sector.Popularity, &constituentCount); err != nil {
			sectorRows.Close()
			return nil, err
		}
		sectors = append(sectors, sector)
	}
	if err := sectorRows.Close(); err != nil {
		return nil, err
	}
	sectors = selectRecommendationSectors(sectors)
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
		WHERE b.status='listed' AND b.sec_type='stock'`+recommendationExcludeBrokerSQL+`AND sb.sector_code IN (`+placeholders+`)
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

	return s.rankRecommendationPool(ctx, tradeDate, pool)
}

// rankRecommendationPool 是候选池的统一收口：只接受完整 60 根日 K 的证券，
// 先按趋势筛选，再依次执行风险硬否决、昨日涨停与短线追高过滤，最后按
// “趋势分 × 过热惩罚 × 龙头加权”排序取前 10。
// RiskScore 保留原始分值随候选展示，同时作为二值硬闸门
// （>= RecommendationProductionMaxRisk 直接剔除），但不参与排序打分。
// 券商候选在此兜底剔除（SQL 层已过滤一次）。
func (s *Store) rankRecommendationPool(ctx context.Context, tradeDate string, pool []RecommendationCandidate) ([]RecommendationCandidate, error) {
	leaderRanks := recommendationLeaderRanks(pool)
	leaderBoosts := recommendationLeaderBoosts(pool)
	trendCandidates := make([]RecommendationCandidate, 0, len(pool))
	sortScores := make(map[string]float64, len(pool))
	for _, candidate := range pool {
		if isBrokerCandidate(candidate.Industry, candidate.Name) {
			continue
		}
		klines, err := s.QueryKlines(ctx, candidate.Symbol, "day", "qfq", "", tradeDate, recommendationKlineDays)
		if err != nil {
			return nil, err
		}
		score, ok := recommendationTrendScore(klines)
		if !ok {
			continue
		}
		risk, ok := recommendationRiskScore(klines)
		// 计算失败说明行情窗口不完整，按既有完整数据要求跳过。
		if !ok {
			continue
		}
		// 风险硬否决：高风险候选直接出局，不进入 AI 评审，也不做排序权衡。
		// 风险分不参与排序（见 recommendationCandidateSortScore），只做二值闸门。
		if risk >= RecommendationProductionMaxRisk {
			continue
		}
		if recommendationGapRiskHigh(klines, candidate.Code) {
			continue
		}
		if recommendationOverextended(klines) {
			continue
		}
		candidate.Klines = klines
		candidate.TrendScore = score
		candidate.RiskScore = risk
		candidate.LeaderRank = leaderRanks[candidate.Symbol]
		boost := leaderBoosts[candidate.Symbol]
		if boost <= 0 {
			boost = 1
		}
		sortScores[candidate.Symbol] = recommendationCandidateSortScore(score, klines, boost, risk)
		trendCandidates = append(trendCandidates, candidate)
	}
	return selectStrongSectorLeaders(trendCandidates, sortScores, recommendationCandidateLimit), nil
}

func selectStrongSectorLeaders(candidates []RecommendationCandidate, sortScores map[string]float64, limit int) []RecommendationCandidate {
	if limit <= 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		// 先比较强板块，再比较板块内龙头的趋势与建仓空间得分。
		if a.SectorHeat != b.SectorHeat {
			return a.SectorHeat > b.SectorHeat
		}
		si, sj := sortScores[a.Symbol], sortScores[b.Symbol]
		if si == sj {
			return a.Symbol < b.Symbol
		}
		return si > sj
	})
	// 每个强板块最多保留前三只龙头，避免单一板块铺满全部 AI 候选位。
	selected := make([]RecommendationCandidate, 0, min(limit, len(candidates)))
	sectorCounts := make(map[string]int, recommendationSectorLimit)
	for _, candidate := range candidates {
		if sectorCounts[candidate.Industry] >= recommendationLeaderRankLimit {
			continue
		}
		sectorCounts[candidate.Industry]++
		selected = append(selected, candidate)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

// RecommendationHotspotConcept 是热点漏斗 final 报告中一个已回验概念的最小引用，
// 由 analysis 层解析报告后传入，避免 store 反向依赖 analysis 的报告结构。
type RecommendationHotspotConcept struct {
	SectorCode string
	SectorName string
	Confidence float64
}

// RecommendationCandidatesFromHotspot 直接复用热点漏斗产出的概念作为候选来源：
// 以漏斗卡点概念的成分股为唯一候选池（Industry 记为概念名、SectorHeat 记为
// AI 置信度），再走与旧逻辑一致的趋势、过热、龙头与建仓空间收口。漏斗结果为空或
// 过滤后候选不足时由调用方回退 RecommendationCandidates。
func (s *Store) RecommendationCandidatesFromHotspot(ctx context.Context, concepts []RecommendationHotspotConcept) ([]RecommendationCandidate, error) {
	if len(concepts) == 0 {
		return []RecommendationCandidate{}, nil
	}
	tradeDate, err := s.LatestKlineDate(ctx)
	if err != nil {
		return nil, err
	}
	if tradeDate == "" {
		return []RecommendationCandidate{}, nil
	}
	codes := make([]string, 0, len(concepts))
	conceptByCode := make(map[string]RecommendationHotspotConcept, len(concepts))
	for _, concept := range concepts {
		if concept.SectorCode == "" || conceptByCode[concept.SectorCode].SectorCode != "" {
			continue
		}
		conceptByCode[concept.SectorCode] = concept
		codes = append(codes, concept.SectorCode)
	}
	if len(codes) == 0 {
		return []RecommendationCandidate{}, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(codes)), ",")
	args := make([]any, 0, len(codes)+1)
	args = append(args, tradeDate)
	for _, code := range codes {
		args = append(args, code)
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT b.symbol,b.code,b.name,sc.sector_code,k.amount
		FROM stock_basic b
		INNER JOIN sector_constituent sc ON sc.symbol=b.symbol
		INNER JOIN kline_daily k ON k.symbol=b.symbol AND k.trade_date=?
		WHERE b.status='listed' AND b.sec_type='stock'`+recommendationExcludeBrokerSQL+`AND sc.sector_code IN (`+placeholders+`)
		ORDER BY k.amount DESC,b.symbol ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("query hotspot recommendation stocks: %w", err)
	}
	defer rows.Close()

	// 同一股票可能属于多个漏斗概念，保留 AI 置信度更高的概念归属。
	bySymbol := make(map[string]RecommendationCandidate)
	for rows.Next() {
		var candidate RecommendationCandidate
		var sectorCode string
		if err := rows.Scan(&candidate.Symbol, &candidate.Code, &candidate.Name, &sectorCode, &candidate.Popularity); err != nil {
			return nil, err
		}
		concept := conceptByCode[sectorCode]
		candidate.Industry = concept.SectorName
		candidate.SectorType = "concept"
		candidate.SectorHeat = concept.Confidence
		current, exists := bySymbol[candidate.Symbol]
		if !exists || candidate.SectorHeat > current.SectorHeat {
			bySymbol[candidate.Symbol] = candidate
		}
	}
	if err := rows.Err(); err != nil {
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
	return s.rankRecommendationPool(ctx, tradeDate, pool)
}

// recommendationLimitPct 按交易代码近似涨跌幅上限：创业板/科创板 20%，
// 北交所 30%，其余按主板 10%。ST 等特殊限幅不单独识别，宁可略偏严。
func recommendationLimitPct(code string) float64 {
	switch {
	case strings.HasPrefix(code, "30"), strings.HasPrefix(code, "68"):
		return 20
	case strings.HasPrefix(code, "92"), strings.HasPrefix(code, "8"), strings.HasPrefix(code, "4"):
		return 30
	default:
		return 10
	}
}

// recommendationGapRiskHigh 判断昨日是否接近涨停收盘：计分买入价为次日开盘价，
// 封板股次日大概率高开甚至一字，开盘建仓的期望损耗最大，直接剔除。
// 优先使用交易所口径 change_pct；缺失时用最后两根收盘价近似。
func recommendationGapRiskHigh(klines []model.Kline, code string) bool {
	if len(klines) < 2 {
		return false
	}
	last := klines[len(klines)-1]
	changePct := last.ChangePct
	if changePct == 0 {
		prev := klines[len(klines)-2]
		if prev.Close > 0 {
			changePct = (last.Close/prev.Close - 1) * 100
		}
	}
	return changePct >= recommendationLimitPct(code)*recommendationLimitUpRatio
}

// recommendationSortScore 在原始趋势分上做短期过热排序惩罚：近 5 日涨幅
// 超过 15% 起惩罚，35% 处惩罚封顶（趋势分打五折）。仅影响候选排序，
// 不改变 TrendScore 原值，也不改变趋势/风险硬过滤结果。
func recommendationSortScore(trendScore float64, klines []model.Kline) float64 {
	last := len(klines) - 1
	if last < 5 || klines[last-5].Close <= 0 {
		return trendScore
	}
	gain5 := klines[last].Close/klines[last-5].Close - 1
	if gain5 <= recommendationOverheatFloor {
		return trendScore
	}
	ratio := (gain5 - recommendationOverheatFloor) / (recommendationOverheatCeiling - recommendationOverheatFloor)
	if ratio > 1 {
		ratio = 1
	}
	return trendScore * (1 - recommendationOverheatMaxPenalty*ratio)
}

// recommendationCandidateSortScore 是生产候选最终排序分。riskScore 参数刻意只接收
// 不使用，用测试锁定“风险分只展示”的产品约束，防止后续再次悄悄加入降权。
func recommendationCandidateSortScore(trendScore float64, klines []model.Kline, leaderBoost, riskScore float64) float64 {
	_ = riskScore
	if leaderBoost <= 0 {
		leaderBoost = 1
	}
	return recommendationSortScore(trendScore, klines) * leaderBoost
}

// recommendationRiskPenalty 仅供 riskpolicy_backtest_test.go 复现历史策略。
// 生产推荐禁止调用；当前排序由 recommendationCandidateSortScore 完成且忽略风险分。
func recommendationRiskPenalty(risk, soft float64) float64 {
	if soft <= 0 || soft > 100 {
		soft = recommendationBaseMaxRisk
	}
	if risk <= soft {
		return 1
	}
	span := recommendationHardRiskCeiling - soft
	if span <= 0 {
		return 1 - recommendationRiskMaxPenalty
	}
	ratio := (risk - soft) / span
	if ratio > 1 {
		ratio = 1
	}
	return 1 - recommendationRiskMaxPenalty*ratio
}

// recommendationOverextended 是短线追高硬过滤：近 5 日涨幅超过 25%，或收盘价
// 高于 MA5 超过 8%（乖离过大）的候选直接剔除。计分买入价为次日开盘价，此类
// 候选的高开损耗与均值回归风险最大，不能只靠排序降权兜底。数据不足时不剔除，
// 交由既有趋势/风险过滤决定。
func recommendationOverextended(klines []model.Kline) bool {
	last := len(klines) - 1
	if last < 5 {
		return false
	}
	if prev := klines[last-5].Close; prev > 0 {
		if klines[last].Close/prev-1 > recommendationHardOverheat5 {
			return true
		}
	}
	var sum float64
	for i := last - 4; i <= last; i++ {
		if klines[i].Close <= 0 {
			return false
		}
		sum += klines[i].Close
	}
	ma5 := sum / 5
	return ma5 > 0 && klines[last].Close/ma5-1 > recommendationMaxBiasMA5
}

// recommendationLeaderRanks 返回每只股票在所属热点板块内按成交额计算的人气排名，
// 并作为结构化字段交给 AI，明确执行“先选强板块，再抓板块龙头”。
func recommendationLeaderRanks(pool []RecommendationCandidate) map[string]int {
	ranked := make([]RecommendationCandidate, len(pool))
	copy(ranked, pool)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Popularity == ranked[j].Popularity {
			return ranked[i].Symbol < ranked[j].Symbol
		}
		return ranked[i].Popularity > ranked[j].Popularity
	})
	ranks := make(map[string]int, len(ranked))
	rankBySector := make(map[string]int, len(ranked))
	for _, candidate := range ranked {
		rankBySector[candidate.Industry]++
		ranks[candidate.Symbol] = rankBySector[candidate.Industry]
	}
	return ranks
}

// recommendationLeaderBoosts 按「同一热点板块内当日成交额排名」给出龙头排序
// 加权系数：第 1 名 ×1.10、第 2-3 名 ×1.05，其余 ×1。Popularity 字段在两条候选
// 链路（题材热度 / 热点漏斗）中均为当日成交额，可直接用作板块内人气排名依据。
func recommendationLeaderBoosts(pool []RecommendationCandidate) map[string]float64 {
	ranked := make([]RecommendationCandidate, len(pool))
	copy(ranked, pool)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Popularity == ranked[j].Popularity {
			return ranked[i].Symbol < ranked[j].Symbol
		}
		return ranked[i].Popularity > ranked[j].Popularity
	})
	boosts := make(map[string]float64, len(ranked))
	rankBySector := make(map[string]int, len(ranked))
	for _, candidate := range ranked {
		rankBySector[candidate.Industry]++
		switch rank := rankBySector[candidate.Industry]; {
		case rank == 1:
			boosts[candidate.Symbol] = recommendationLeaderBoostTop1
		case rank <= recommendationLeaderRankLimit:
			boosts[candidate.Symbol] = recommendationLeaderBoostTopN
		default:
			boosts[candidate.Symbol] = 1
		}
	}
	return boosts
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

// RecommendationRun 是一次推荐运行的留痕，PickCount=0 表示当日主动空仓。
type RecommendationRun struct {
	AnalysisDate   string `json:"analysis_date"`
	GateLevel      string `json:"gate_level"`
	GateReason     string `json:"gate_reason"`
	MaxPicks       int    `json:"max_picks"`
	PickCount      int    `json:"pick_count"`
	CandidateCount int    `json:"candidate_count"`
	ModelName      string `json:"model_name"`
}

// SaveRecommendationRun 记录当日推荐运行结果（含 0 只的空仓日）。
// 必须在 ReplaceRecommendations 之后调用：它是「最近推荐日」的权威来源。
func (s *Store) SaveRecommendationRun(ctx context.Context, run RecommendationRun) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO recommendation_run
		(analysis_date,gate_level,gate_reason,max_picks,pick_count,candidate_count,model_name)
		VALUES (?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE gate_level=VALUES(gate_level),gate_reason=VALUES(gate_reason),
			max_picks=VALUES(max_picks),pick_count=VALUES(pick_count),
			candidate_count=VALUES(candidate_count),model_name=VALUES(model_name)`,
		run.AnalysisDate, run.GateLevel, run.GateReason, run.MaxPicks, run.PickCount, run.CandidateCount, run.ModelName)
	return err
}

// LatestRecommendationRun 返回最近一次推荐运行留痕，无记录时返回 nil。
// 前端据此区分「今日主动空仓」与「今日尚未运行」。
func (s *Store) LatestRecommendationRun(ctx context.Context) (*RecommendationRun, error) {
	var run RecommendationRun
	err := s.DB.QueryRowContext(ctx, `SELECT DATE_FORMAT(analysis_date,'%Y-%m-%d'),gate_level,gate_reason,max_picks,pick_count,candidate_count,model_name
		FROM recommendation_run ORDER BY analysis_date DESC LIMIT 1`).
		Scan(&run.AnalysisDate, &run.GateLevel, &run.GateReason, &run.MaxPicks, &run.PickCount, &run.CandidateCount, &run.ModelName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Store) LatestRecommendations(ctx context.Context) ([]model.StockRecommendation, error) {
	return s.RecommendationsByDate(ctx, "")
}

func (s *Store) RecommendationsByDate(ctx context.Context, date string) ([]model.StockRecommendation, error) {
	// 「最近推荐日」以 recommendation_run 为准，而不是 stock_recommendation
	// 的 MAX(analysis_date)：空仓日在 stock_recommendation 中没有行，
	// 用后者会静默回退到上一个有推荐的交易日，把旧推荐当成当日结论展示。
	// recommendation_run 为空（迁移前的旧库）时回退到旧口径保持兼容。
	where := `r.analysis_date=(SELECT COALESCE(
		(SELECT MAX(analysis_date) FROM recommendation_run),
		(SELECT MAX(analysis_date) FROM stock_recommendation)))`
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
	if len(out) > 0 {
		settlements, err := s.PositionSettlementsByAnalysisDate(ctx, out[0].Date)
		if err != nil {
			return nil, err
		}
		for i := range out {
			if err := s.applyRecommendationPerformance(ctx, &out[i], settlements); err != nil {
				return nil, err
			}
			// 无生命周期记录的推荐补充参考口径展示（Settled=false，不进统计），
			// 让推荐历史仍能看到推荐后的实际走势。
			if out[i].PositionStatus == "" {
				if err := s.applyReferencePerformance(ctx, &out[i]); err != nil {
					return nil, err
				}
			}
		}
	}
	if out == nil {
		out = []model.StockRecommendation{}
	}
	return out, nil
}

// applyRecommendationPerformance 只按真实持仓生命周期填充收益。
// 没有 position 记录的历史推荐只是候选历史，不代表发生过交易，不能进入
// 胜率、累计收益或权益曲线。holding 使用最新市场快照计算浮盈；只有 exited
// 使用用户手动确认的平仓参考价冻结收益。
func (s *Store) applyRecommendationPerformance(ctx context.Context, item *model.StockRecommendation, settlements map[string]PositionSettlement) error {
	settlement, hasPosition := settlements[item.Symbol]
	if !hasPosition {
		return nil
	}
	item.PositionStatus = settlement.Status
	item.DataQuality = settlement.DataQuality
	switch settlement.Status {
	case PositionExpired, PositionRemoved:
		// expired（未建仓放弃）与 removed（手动移除自选放弃跟踪）均无可信结算价：
		// 只展示放弃原因，不产生收益样本，也不再补参考口径继续追踪。
		item.ExitReason = settlement.ExitReason
		return nil
	case PositionPendingEntry:
		return nil
	case PositionExited:
		if settlement.EntryPrice == nil || settlement.ExitPrice == nil || *settlement.EntryPrice <= 0 {
			return nil
		}
		entry, exit := *settlement.EntryPrice, *settlement.ExitPrice
		// 分批减仓后单笔收益必须按仓位加权，并扣除往返交易成本，
		// 否则会高估收益、把「微盈实亏」的交易统计成盈利单。
		gross := positionBlendedChangePct(settlement.RealizedPct, settlement.PositionPct, (exit/entry-1)*100)
		pct := PositionNetChangePct(gross)
		item.EntryPrice, item.LatestPrice, item.ChangePct = &entry, &exit, &pct
		item.Exited, item.Settled = true, true
		item.ExitReason = settlement.ExitReason
		item.TrackedDays = settlement.HoldDays
		if item.TrackedDays <= 0 {
			startDate := item.Date
			if settlement.EntryDate != "" {
				startDate = settlement.EntryDate
			}
			days, err := s.TradingDaysSince(ctx, startDate, settlement.ExitDate)
			if err != nil {
				return err
			}
			item.TrackedDays = days + 1
		}
		return nil
	case PositionHolding:
		if settlement.EntryPrice == nil || *settlement.EntryPrice <= 0 {
			return nil
		}
		latest, err := s.latestPositionReferencePrice(ctx, item.Symbol)
		if err != nil {
			return err
		}
		if latest == nil || *latest <= 0 {
			return nil
		}
		entry := *settlement.EntryPrice
		gross := positionBlendedChangePct(settlement.RealizedPct, settlement.PositionPct, (*latest/entry-1)*100)
		pct := PositionNetChangePct(gross)
		item.EntryPrice, item.LatestPrice, item.ChangePct = &entry, latest, &pct
		item.TrackedDays = settlement.HoldDays
		item.Exited, item.Settled = false, true
		return nil
	default:
		return nil
	}
}

// applyReferencePerformance 为未加入建仓池的推荐补充固定 10 个交易日参考走势：
// 推荐后首个交易日开盘为起点，第 10 个交易日收盘冻结；窗口未满则暂用最新收盘。
// 该结果保持 Settled=false，不进入真实交易胜率与收益统计。
func (s *Store) applyReferencePerformance(ctx context.Context, item *model.StockRecommendation) error {
	window, err := s.recommendationWindow(ctx, item.Symbol, item.Date)
	if err != nil {
		return err
	}
	item.EntryPrice, item.LatestPrice, item.ChangePct = recommendationPerformance(window.entryOpen, window.lastClose)
	item.TrackedDays = window.days
	item.ReferenceOnly = true
	if window.exited {
		item.ExitReason = window.exitReason
	}
	return nil
}

func (s *Store) latestPositionReferencePrice(ctx context.Context, symbol string) (*float64, error) {
	var price sql.NullFloat64
	err := s.DB.QueryRowContext(ctx, `SELECT price FROM market_snapshot WHERE symbol=? ORDER BY snapshot_at DESC LIMIT 1`, symbol).Scan(&price)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if !price.Valid || price.Float64 <= 0 {
		err = s.DB.QueryRowContext(ctx, `SELECT close FROM kline_daily WHERE symbol=? ORDER BY trade_date DESC LIMIT 1`, symbol).Scan(&price)
		if err == sql.ErrNoRows {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	if !price.Valid || price.Float64 <= 0 {
		return nil, nil
	}
	value := price.Float64
	return &value, nil
}

// 非生命周期推荐固定跟踪 10 个交易日：次日开盘为起点，第 10 个交易日收盘冻结。
const (
	recommendationReferenceDays = 10
	recommendationPerfDefault   = 5
	RecommendationExitReasonMA  = "收盘跌破10日线，趋势不再可持续" // 保留给历史记录与影子策略
	RecommendationExitReasonCap = "达到固定10个交易日结算期"
)

type recommendationWindow struct {
	entryOpen  sql.NullFloat64
	lastClose  sql.NullFloat64
	days       int
	exited     bool
	exitReason string
}

// recommendationWindow 为未加入建仓池的推荐计算固定 10 个交易日表现。
func (s *Store) recommendationWindow(ctx context.Context, symbol, analysisDate string) (recommendationWindow, error) {
	var window recommendationWindow
	rows, err := s.DB.QueryContext(ctx, `SELECT k.open,k.close FROM kline_daily k
		WHERE k.symbol=? AND k.trade_date>? ORDER BY k.trade_date ASC LIMIT ?`,
		symbol, analysisDate, recommendationReferenceDays)
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
	if window.days >= recommendationReferenceDays {
		window.exited = true
		window.exitReason = RecommendationExitReasonCap
	}
	return window, rows.Err()
}

// trailingAverage 计算序列末尾 period 个值的均值；样本不足时返回 false
// （新股/停牌导致历史不足，趋势破位判断顺延到样本充足之后）。
func trailingAverage(values []float64, period int) (float64, bool) {
	if len(values) < period {
		return 0, false
	}
	var sum float64
	for _, value := range values[len(values)-period:] {
		sum += value
	}
	return sum / float64(period), true
}

// RecommendationDailyPerformance 按推荐日汇总真实生命周期交易表现。
type RecommendationDailyPerformance struct {
	Date         string   `json:"date"`
	Stocks       int      `json:"stocks"`
	TrackedDays  int      `json:"tracked_days"`
	Finished     bool     `json:"finished"`
	SumChangePct *float64 `json:"sum_change_pct"`
	AvgChangePct *float64 `json:"avg_change_pct"`
}

// RecommendationRecentPerformance 汇总最近 days 个推荐日的真实生命周期收益。
// holding 使用最新市场快照展示浮盈，exited 使用冻结收益；没有 position 的推荐、
// pending_entry 和 expired 不产生收益样本。
func (s *Store) RecommendationRecentPerformance(ctx context.Context, days int) ([]RecommendationDailyPerformance, error) {
	if days <= 0 || days > 30 {
		days = recommendationPerfDefault
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
		summary := RecommendationDailyPerformance{Date: date}
		var sum float64
		var counted int
		// 趋势跟踪口径：只统计已建仓的有效样本；全部有效样本退出后完结。
		allExited := true
		for _, item := range items {
			// 未建仓的标的不参与当日组合收益与完结判定。
			if !item.Settled {
				continue
			}
			summary.Stocks++
			if item.TrackedDays > summary.TrackedDays {
				summary.TrackedDays = item.TrackedDays
			}
			if !item.Exited {
				allExited = false
			}
			if item.ChangePct != nil {
				sum += *item.ChangePct
				counted++
			}
		}
		summary.Finished = allExited && counted > 0
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

// RecommendationBasketDailyPerformance 把每日 3 只趋势推荐视为等权参考组合。
// 它独立于真实 position 生命周期，仅用于回答“当日推荐集合后来表现如何”。
type RecommendationBasketDailyPerformance struct {
	Date           string   `json:"date"`
	Stocks         int      `json:"stocks"`
	FrozenStocks   int      `json:"frozen_stocks"`
	TrackingStocks int      `json:"tracking_stocks"`
	TrackedDays    int      `json:"tracked_days"`
	Finished       bool     `json:"finished"`
	SumChangePct   *float64 `json:"sum_change_pct"`
	AvgChangePct   *float64 `json:"avg_change_pct"`
}

func summarizeRecommendationBasket(date string, items []model.StockRecommendation) RecommendationBasketDailyPerformance {
	summary := RecommendationBasketDailyPerformance{Date: date}
	var sum float64
	for _, item := range items {
		if item.ChangePct == nil {
			continue
		}
		summary.Stocks++
		sum += *item.ChangePct
		if item.TrackedDays > summary.TrackedDays {
			summary.TrackedDays = item.TrackedDays
		}
		if item.ExitReason == "" {
			summary.TrackingStocks++
		} else {
			summary.FrozenStocks++
		}
	}
	if summary.Stocks > 0 {
		total := sum
		avg := sum / float64(summary.Stocks)
		summary.SumChangePct = &total
		summary.AvgChangePct = &avg
		summary.Finished = summary.FrozenStocks == summary.Stocks
	}
	return summary
}

// RecommendationBasketPerformance 返回最近若干推荐日的每日 3 只等权表现。
// 唯一最强标的按真实手动建仓/平仓生命周期统计；其余两只按次日开盘至
// 第 10 个交易日收盘的固定窗口统计，窗口未满时暂随最新收盘更新。
func (s *Store) RecommendationBasketPerformance(ctx context.Context, days int) ([]RecommendationBasketDailyPerformance, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	dates, err := s.RecommendationHistory(ctx, days)
	if err != nil {
		return nil, err
	}
	out := make([]RecommendationBasketDailyPerformance, 0, len(dates))
	for _, date := range dates {
		items, err := s.RecommendationsByDate(ctx, date)
		if err != nil {
			return nil, err
		}
		for i := range items {
			// RecommendationsByDate 已为未入池标的应用固定 10 日参考口径；
			// 有生命周期的唯一最强标的保留真实手动交易收益，不得被覆盖。
			if items[i].PositionStatus == "" && !items[i].ReferenceOnly {
				if err := s.applyReferencePerformance(ctx, &items[i]); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, summarizeRecommendationBasket(date, items))
	}
	return out, nil
}

// recommendationPerformance 将两个有效价格转换为收益展示数据。
// 该函数仍供影子策略研究使用，不参与真实持仓生命周期统计。
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

// RecommendationStats 是真实持仓生命周期的整体表现统计。
// 只有 exited 的有效结算进入胜率和已实现收益，holding 单独统计浮盈。
type RecommendationStats struct {
	TotalDays      int `json:"total_days"`
	LifecyclePicks int `json:"lifecycle_picks"`
	PendingPicks   int `json:"pending_picks"`
	HoldingPicks   int `json:"holding_picks"`
	ExitedPicks    int `json:"exited_picks"`
	ExpiredPicks   int `json:"expired_picks"`
	// RemovedPicks 是用户手动移除自选后放弃跟踪的样本数：无确认平仓价，
	// 与 expired 一样不进入胜率与收益，显式计数避免统计口径悄悄变化。
	RemovedPicks           int      `json:"removed_picks"`
	FrozenPicks            int      `json:"frozen_picks"`
	TrackingPicks          int      `json:"tracking_picks"`
	Wins                   int      `json:"wins"`
	Losses                 int      `json:"losses"`
	Breakeven              int      `json:"breakeven"`
	WinRate                *float64 `json:"win_rate"`
	AvgChangePct           *float64 `json:"avg_change_pct"`
	SumChangePct           *float64 `json:"sum_change_pct"`
	MedianPct              *float64 `json:"median_pct"`
	AvgWinPct              *float64 `json:"avg_win_pct"`
	AvgLossPct             *float64 `json:"avg_loss_pct"`
	GrossProfitPct         *float64 `json:"gross_profit_pct"`
	GrossLossPct           *float64 `json:"gross_loss_pct"`
	ProfitFactor           *float64 `json:"profit_factor"`
	UnrealizedSumPct       *float64 `json:"unrealized_sum_pct"`
	UnrealizedAvgPct       *float64 `json:"unrealized_avg_pct"`
	AvgHoldDays            *float64 `json:"avg_hold_days"`
	BestPct                *float64 `json:"best_pct"`
	BestName               string   `json:"best_name"`
	WorstPct               *float64 `json:"worst_pct"`
	WorstName              string   `json:"worst_name"`
	DayWins                int      `json:"day_wins"`
	DayFrozen              int      `json:"day_frozen"`
	DayWinRate             *float64 `json:"day_win_rate"`
	ReferencePicks         int      `json:"reference_picks"`
	ReferenceFrozenPicks   int      `json:"reference_frozen_picks"`
	ReferenceTrackingPicks int      `json:"reference_tracking_picks"`
	ReferenceWins          int      `json:"reference_wins"`
	ReferenceLosses        int      `json:"reference_losses"`
	ReferenceWinRate       *float64 `json:"reference_win_rate"`
	ReferenceSumChangePct  *float64 `json:"reference_sum_change_pct"`
	ReferenceAvgChangePct  *float64 `json:"reference_avg_change_pct"`
	// ExcludedPicks 是因数据质量问题被排除出统计的样本数（当前只有 t0_violation）。
	// 显式返回而不是静默丢弃，避免出现「统计口径悄悄变化」而无从察觉。
	ExcludedPicks int `json:"excluded_picks"`
}

// addRecommendationReferenceSample 汇总旧推荐的趋势规则参考结果。
// 这些字段单独展示，绝不并入真实生命周期胜率和收益。
func addRecommendationReferenceSample(stats *RecommendationStats, item model.StockRecommendation) {
	if !item.ReferenceOnly || item.ChangePct == nil {
		return
	}
	stats.ReferencePicks++
	if item.ExitReason == "" {
		stats.ReferenceTrackingPicks++
		return
	}
	stats.ReferenceFrozenPicks++
	pct := *item.ChangePct
	if stats.ReferenceSumChangePct == nil {
		stats.ReferenceSumChangePct = new(float64)
	}
	*stats.ReferenceSumChangePct += pct
	if pct > 0 {
		stats.ReferenceWins++
	} else if pct < 0 {
		stats.ReferenceLosses++
	}
}

func finalizeRecommendationReferenceStats(stats *RecommendationStats) {
	if stats.ReferenceFrozenPicks == 0 || stats.ReferenceSumChangePct == nil {
		return
	}
	winRate := float64(stats.ReferenceWins) / float64(stats.ReferenceFrozenPicks) * 100
	avg := *stats.ReferenceSumChangePct / float64(stats.ReferenceFrozenPicks)
	stats.ReferenceWinRate = &winRate
	stats.ReferenceAvgChangePct = &avg
}

// RecommendationOverallStats 汇总最近 days 个推荐日的真实交易表现。
// 只有 position 生命周期记录参与统计：holding 仅计浮动收益，exited 才进入
// 胜率和已实现收益；pending_entry、expired 以及纯推荐历史均不产生收益样本。
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
	var winSum, lossSum, unrealizedSum float64
	var winCnt, lossCnt, unrealizedCnt, totalHoldDays int
	for _, date := range dates {
		items, err := s.RecommendationsByDate(ctx, date)
		if err != nil {
			return stats, err
		}
		var daySum float64
		dayFrozen := true
		dayCounted := 0
		for _, item := range items {
			if item.ReferenceOnly {
				addRecommendationReferenceSample(&stats, item)
				continue
			}
			// 数据质量不合格的样本（当日进出违反 T+1）只计数不参与任何收益统计：
			// 这些交易现实中无法成交，纳入统计会系统性高估退出类规则的表现。
			if item.DataQuality != PositionDataQualityOK {
				stats.ExcludedPicks++
				continue
			}
			if item.PositionStatus != "" {
				stats.LifecyclePicks++
			}
			switch item.PositionStatus {
			case PositionPendingEntry:
				stats.PendingPicks++
			case PositionHolding:
				stats.HoldingPicks++
			case PositionExited:
				stats.ExitedPicks++
			case PositionExpired:
				stats.ExpiredPicks++
			case PositionRemoved:
				stats.RemovedPicks++
			}
			// 未产生有效收益样本（盘前入池未建仓、宽限期满未建仓）的标的
			// 既不计入追踪中也不计入冻结，避免用从未持有的仓位污染胜率。
			if !item.Settled || item.ChangePct == nil {
				continue
			}
			if !item.Exited {
				stats.TrackingPicks++
				unrealizedSum += *item.ChangePct
				unrealizedCnt++
				dayFrozen = false
				continue
			}
			pct := *item.ChangePct
			stats.FrozenPicks++
			frozen = append(frozen, pct)
			totalHoldDays += item.TrackedDays
			daySum += pct
			dayCounted++
			if pct > 0 {
				stats.Wins++
				winSum += pct
				winCnt++
			} else if pct < 0 {
				stats.Losses++
				lossSum += pct
				lossCnt++
			} else {
				stats.Breakeven++
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
	finalizeRecommendationReferenceStats(&stats)
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
		gross := winSum
		stats.GrossProfitPct = &gross
	}
	if lossCnt > 0 {
		value := lossSum / float64(lossCnt)
		stats.AvgLossPct = &value
		gross := lossSum
		stats.GrossLossPct = &gross
		factor := winSum / -lossSum
		stats.ProfitFactor = &factor
	}
	if unrealizedCnt > 0 {
		total := unrealizedSum
		avg := unrealizedSum / float64(unrealizedCnt)
		stats.UnrealizedSumPct = &total
		stats.UnrealizedAvgPct = &avg
	}
	if stats.FrozenPicks > 0 {
		value := float64(totalHoldDays) / float64(stats.FrozenPicks)
		stats.AvgHoldDays = &value
	}
	if stats.DayFrozen > 0 {
		value := float64(stats.DayWins) / float64(stats.DayFrozen) * 100
		stats.DayWinRate = &value
	}
	return stats, nil
}

// ===== 影子基线（确定性规则对照组）=====
//
// 每次盘前推荐在调用 AI 之前，把候选池按两条确定性规则各选 3 只落库：
//   - trend：原始趋势分最高的 3 只（AI 不存在时的朴素基线）
//   - low_risk：风险分最低的 3 只（保守基线）
//
// 与 AI 推荐共用趋势跟踪冻结口径统计，用于回答“AI 相对确定性规则有无超额”。
// 另外并行计算三组执行基线：mechanical_5d（固定持有5日）、stop_4 / stop_8
//（相同AI选股、不同固定止损），用来区分“选股贡献”与“执行参数贡献”。
// AI 请求失败时影子记录依然存在，因此基线样本天数 ≥ AI 样本天数。

// SaveRecommendationShadow 以 REPLACE 语义保存分析日的影子基线选股。
// 候选不足 3 只时跳过（当日推荐本身也会因候选不足而跳过）。
func (s *Store) SaveRecommendationShadow(ctx context.Context, date string, candidates []RecommendationCandidate) error {
	if len(candidates) < 3 {
		return nil
	}
	pickTop3 := func(less func(a, b RecommendationCandidate) bool) []RecommendationCandidate {
		sorted := make([]RecommendationCandidate, len(candidates))
		copy(sorted, candidates)
		sort.Slice(sorted, func(i, j int) bool { return less(sorted[i], sorted[j]) })
		return sorted[:3]
	}
	shadow := map[string][]RecommendationCandidate{
		"trend": pickTop3(func(a, b RecommendationCandidate) bool {
			if a.TrendScore == b.TrendScore {
				return a.Symbol < b.Symbol
			}
			return a.TrendScore > b.TrendScore
		}),
		"low_risk": pickTop3(func(a, b RecommendationCandidate) bool {
			if a.RiskScore == b.RiskScore {
				return a.Symbol < b.Symbol
			}
			return a.RiskScore < b.RiskScore
		}),
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM recommendation_shadow WHERE analysis_date=?", date); err != nil {
		return err
	}
	for strategy, picks := range shadow {
		for i, pick := range picks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO recommendation_shadow
				(analysis_date,strategy,rank_no,symbol,sector_name,trend_score,risk_score) VALUES (?,?,?,?,?,?,?)`,
				date, strategy, i+1, pick.Symbol, pick.Industry, pick.TrendScore, pick.RiskScore); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// RecommendationShadowStrategyStats 是单一策略在共同日期集上的趋势退出冻结口径统计。
type RecommendationShadowStrategyStats struct {
	Strategy      string   `json:"strategy"`
	TotalDays     int      `json:"total_days"`
	FrozenPicks   int      `json:"frozen_picks"`
	TrackingPicks int      `json:"tracking_picks"`
	Wins          int      `json:"wins"`
	WinRate       *float64 `json:"win_rate"`
	AvgChangePct  *float64 `json:"avg_change_pct"`
	SumChangePct  *float64 `json:"sum_change_pct"`
	DayWins       int      `json:"day_wins"`
	DayFrozen     int      `json:"day_frozen"`
	DayWinRate    *float64 `json:"day_win_rate"`
}

// RecommendationShadowStats 在影子基线覆盖的最近 days 个分析日上，
// 对 ai / trend / low_risk 三组用完全相同的窗口口径统计，保证可比。
// ai 组只统计影子日期上存在的 AI 推荐（AI 失败日不计入 ai 组样本）。
func (s *Store) RecommendationShadowStats(ctx context.Context, days int) ([]RecommendationShadowStrategyStats, error) {
	if days <= 0 || days > 365 {
		days = 60
	}
	dateRows, err := s.DB.QueryContext(ctx, fmt.Sprintf(
		"SELECT DATE_FORMAT(analysis_date,'%%Y-%%m-%%d') FROM recommendation_shadow GROUP BY analysis_date ORDER BY analysis_date DESC LIMIT %d", days))
	if err != nil {
		return nil, err
	}
	var dates []string
	for dateRows.Next() {
		var date string
		if err := dateRows.Scan(&date); err != nil {
			dateRows.Close()
			return nil, err
		}
		dates = append(dates, date)
	}
	if err := dateRows.Close(); err != nil {
		return nil, err
	}

	order := []string{"ai", "trend", "low_risk", "mechanical_5d", "stop_4", "stop_8"}
	stats := make(map[string]*RecommendationShadowStrategyStats, len(order))
	for _, name := range order {
		stats[name] = &RecommendationShadowStrategyStats{Strategy: name}
	}
	frozenSums := make(map[string]float64, len(order))

	// accumulate 累计单个策略在单个分析日的三只表现；只统计已冻结（趋势退出）数据。
	accumulate := func(strategy string, picks []struct {
		changePct *float64
		exited    bool
	}) {
		target := stats[strategy]
		if len(picks) == 0 {
			return
		}
		target.TotalDays++
		var daySum float64
		dayFrozen := true
		dayCounted := 0
		for _, pick := range picks {
			if pick.changePct == nil {
				continue
			}
			if !pick.exited {
				target.TrackingPicks++
				dayFrozen = false
				continue
			}
			pct := *pick.changePct
			target.FrozenPicks++
			frozenSums[strategy] += pct
			daySum += pct
			dayCounted++
			if pct > 0 {
				target.Wins++
			}
		}
		if dayFrozen && dayCounted > 0 {
			target.DayFrozen++
			if daySum > 0 {
				target.DayWins++
			}
		}
	}

	type pickResult = struct {
		changePct *float64
		exited    bool
	}
	for _, date := range dates {
		aiItems, err := s.RecommendationsByDate(ctx, date)
		if err != nil {
			return nil, err
		}
		aiPicks := make([]pickResult, 0, len(aiItems))
		mechanicalPicks := make([]pickResult, 0, len(aiItems))
		stop4Picks := make([]pickResult, 0, len(aiItems))
		stop8Picks := make([]pickResult, 0, len(aiItems))
		for _, item := range aiItems {
			aiPicks = append(aiPicks, pickResult{item.ChangePct, item.Exited})
			mechanical, err := s.MechanicalWindow(ctx, item.Symbol, date, MechanicalHoldDays)
			if err != nil {
				return nil, err
			}
			if mechanical == nil {
				mechanicalPicks = append(mechanicalPicks, pickResult{nil, false})
			} else {
				value := mechanical.NetPct
				mechanicalPicks = append(mechanicalPicks, pickResult{&value, true})
			}
			for _, scan := range []struct {
				stop   float64
				target *[]pickResult
			}{{4, &stop4Picks}, {8, &stop8Picks}} {
				value, frozen, err := s.fixedRiskWindow(ctx, item.Symbol, date, scan.stop)
				if err != nil {
					return nil, err
				}
				*scan.target = append(*scan.target, pickResult{value, frozen})
			}
		}
		accumulate("ai", aiPicks)
		accumulate("mechanical_5d", mechanicalPicks)
		accumulate("stop_4", stop4Picks)
		accumulate("stop_8", stop8Picks)

		shadowRows, err := s.DB.QueryContext(ctx,
			"SELECT strategy,symbol FROM recommendation_shadow WHERE analysis_date=? ORDER BY strategy,rank_no", date)
		if err != nil {
			return nil, err
		}
		byStrategy := make(map[string][]string, 2)
		for shadowRows.Next() {
			var strategy, symbol string
			if err := shadowRows.Scan(&strategy, &symbol); err != nil {
				shadowRows.Close()
				return nil, err
			}
			byStrategy[strategy] = append(byStrategy[strategy], symbol)
		}
		if err := shadowRows.Close(); err != nil {
			return nil, err
		}
		for strategy, symbols := range byStrategy {
			if stats[strategy] == nil {
				continue
			}
			picks := make([]pickResult, 0, len(symbols))
			for _, symbol := range symbols {
				window, err := s.recommendationWindow(ctx, symbol, date)
				if err != nil {
					return nil, err
				}
				_, _, changePct := recommendationPerformance(window.entryOpen, window.lastClose)
				picks = append(picks, pickResult{changePct, window.exited})
			}
			accumulate(strategy, picks)
		}
	}

	out := make([]RecommendationShadowStrategyStats, 0, len(order))
	for _, name := range order {
		target := stats[name]
		if target.FrozenPicks > 0 {
			sum := frozenSums[name]
			avg := sum / float64(target.FrozenPicks)
			winRate := float64(target.Wins) / float64(target.FrozenPicks) * 100
			target.SumChangePct = &sum
			target.AvgChangePct = &avg
			target.WinRate = &winRate
		}
		if target.DayFrozen > 0 {
			value := float64(target.DayWins) / float64(target.DayFrozen) * 100
			target.DayWinRate = &value
		}
		out = append(out, *target)
	}
	return out, nil
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
