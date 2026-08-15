package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/store"
)

// 30 分钟节奏：避开开盘最初 30 分钟噪音，14:52 增加尾盘隔夜风险检查。
var entryRunSlots = []int{10 * 60, 10*60 + 30, 11 * 60, 11*60 + 30, 13*60 + 30, 14 * 60, 14*60 + 30, 14*60 + 52}

// isTailSlot 判断是否处于当日最后一档（14:52）。趋势破位用当时价格近似收盘价确认，
// 避免盘中插针击穿均线就不可逆清仓——A 股盘中下影线极常见，按盘中价判破位会被噪音扫出。
func isTailSlot(now time.Time) bool {
	tail := entryRunSlots[len(entryRunSlots)-1]
	return now.Hour()*60+now.Minute() >= tail
}

func (s *Service) EntryRunning() bool { return s.entryRunning.Load() }
func (s *Service) EntryLastError() string {
	if value := s.entryLastErr.Load(); value != nil {
		return value.(string)
	}
	return ""
}
func (s *Service) setEntryLastError(err error) {
	if err == nil {
		s.entryLastErr.Store("")
		return
	}
	s.entryLastErr.Store(err.Error())
}

type intradayMarketContext struct {
	CheckedAt    string             `json:"checked_at"`
	SnapshotAt   string             `json:"snapshot_at,omitempty"`
	MarketPhase  string             `json:"market_phase,omitempty"`
	ReviewDate   string             `json:"review_date,omitempty"`
	MarketDate   string             `json:"market_date,omitempty"`
	Indices      []model.IndexQuote `json:"indices"`
	AveragePct   float64            `json:"average_change_pct"`
	FallingCount int                `json:"falling_index_count"`
}

type positionAnalysisItem struct {
	PositionID    int64               `json:"position_id"`
	Symbol        string              `json:"symbol"`
	Name          string              `json:"name"`
	Stage         string              `json:"stage"`
	PickDate      string              `json:"pick_date"`
	EntryDate     string              `json:"entry_date,omitempty"`
	EntryPrice    *float64            `json:"entry_price,omitempty"`
	HighestPrice  *float64            `json:"highest_price,omitempty"`
	ProfitPct     *float64            `json:"profit_pct,omitempty"`
	PeakProfitPct *float64            `json:"peak_profit_pct,omitempty"`
	PositionPct   float64             `json:"position_pct,omitempty"`
	StopLossPrice *float64            `json:"stop_loss_price,omitempty"`
	HoldDays      int                 `json:"hold_days"`
	GraceDaysUsed int                 `json:"entry_grace_days_used,omitempty"`
	Price         *float64            `json:"price"`
	ChangePct     *float64            `json:"change_pct"`
	MA5           float64             `json:"ma5"`
	MA10          float64             `json:"ma10"`
	MA20          float64             `json:"ma20"`
	ATRPct        float64             `json:"atr_pct"`
	Gain5Pct      float64             `json:"gain_5d_pct"`
	Gain20Pct     float64             `json:"gain_20d_pct"`
	VolRatio5     float64             `json:"volume_vs_5d_avg"`
	Sector        store.SectorContext `json:"sector"`
}

type positionDecision struct {
	PositionID int64    `json:"position_id"`
	Action     string   `json:"action"`
	Reason     string   `json:"reason"`
	PriceLow   *float64 `json:"price_low"`
	PriceHigh  *float64 `json:"price_high"`
	Urgency    string   `json:"urgency"`
}

func (s *Service) RunEntryAnalysis(ctx context.Context, now time.Time) error {
	err := s.runEntryAnalysis(ctx, now)
	s.setEntryLastError(err)
	return err
}

// runEntryAnalysis 同时处理全部活跃标的：pending_entry 寻找建仓点，holding 寻找退出点。
func (s *Service) runEntryAnalysis(ctx context.Context, now time.Time) error {
	if !s.Enabled() {
		return fmt.Errorf("AI 趋势持仓分析未配置")
	}
	if !isRecommendationTradingDay(now) {
		return fmt.Errorf("非交易日不执行盘中分析")
	}
	tradeDate := now.Format("2006-01-02")
	if !s.entryRunning.CompareAndSwap(false, true) {
		return fmt.Errorf("盘中趋势分析任务正在执行")
	}
	defer s.entryRunning.Store(false)

	positions, err := s.st.ActivePositions(ctx)
	if err != nil {
		return err
	}
	if len(positions) == 0 {
		return fmt.Errorf("没有待建仓或持有中的 AI 趋势标的")
	}

	active := make([]store.Position, 0, len(positions))
	for _, p := range positions {
		if p.Status != store.PositionPendingEntry {
			active = append(active, p)
			continue
		}
		used, err := s.st.TradingDaysSince(ctx, p.PickDate, tradeDate)
		if err != nil {
			return err
		}
		if used <= store.PositionEntryGraceDays {
			active = append(active, p)
			continue
		}
		reason := fmt.Sprintf("入池后%d个交易日未出现合适建仓点，放弃并腾出自选位", store.PositionEntryGraceDays)
		if err := s.st.ExpirePosition(ctx, p.ID, reason, p.Symbol); err != nil {
			return err
		}
		if err := s.st.SaveEntryAdvice(ctx, store.EntryAdviceInput{TradeDate: tradeDate, Symbol: p.Symbol, Source: store.EntrySourceRule, Stage: store.EntryStageEntry, Action: store.PositionExpired, Reason: reason, Urgency: store.EntryUrgencyNormal, Model: "local-rule"}); err != nil {
			return err
		}
	}
	if len(active) == 0 {
		return nil
	}

	items, market, err := s.buildPositionAnalysisContext(ctx, now, active)
	if err != nil {
		return err
	}
	if s.marketProvider != nil && market.MarketDate == "" {
		return fmt.Errorf("实时指数缺少行情源时间，无法确认交易日")
	}
	if market.MarketDate != "" && market.MarketDate != tradeDate {
		return fmt.Errorf("实时行情日期为%s，%s按休市日跳过", market.MarketDate, tradeDate)
	}
	items, err = s.applyRiskControls(ctx, tradeDate, isTailSlot(now), market, items)
	if err != nil || len(items) == 0 {
		return err
	}

	payload, _ := json.Marshal(map[string]interface{}{"trade_date": tradeDate, "market": market, "positions": items})
	systemPrompt := `你是严格受限的A股盘中趋势交易评审器。输入仅来自本地数据库，包含实时大盘指数、上一交易日行业板块强弱、个股实时行情与日K派生指标。必须逐只评估positions，不能遗漏或增加标的。
核心原则：入场依据是「强板块+强个股+人气+建仓空间」的短线动量，有效期约1-5个交易日；退出判断必须与该尺度对齐，不得按中长线均线尺度扛单。
本地已先行执行确定性风控（硬止损、移动止盈、时间止损、系统性风险、尾盘趋势破位），进入你视野的标的均未触发上述纪律，你只做相机决策。
1. stage=entry只能返回entry或wait。entry要求多头结构完整、现价未明显追高、板块未显著转弱，并给出可执行建仓价区间；否则wait。
2. price_low/price_high是真实成交约束：现价高于price_high时系统不会建仓而是继续等待，因此区间要给在可接受的建仓成本上，不要围绕已冲高的现价随意放宽。
3. stage=exit只能返回hold、reduce或exit。趋势仍健康且量价配合则hold；动量减速、冲高乏力或板块降温用reduce分批减仓保护利润；个股趋势逆转、板块退潮或风险放大用exit。
4. reduce会真实减仓50%并锁定该部分收益，请在「还想留一部分博延续」时使用，不要用它替代应当清仓的情形。
5. 输入含profit_pct、peak_profit_pct、stop_loss_price、position_pct、atr_pct：浮盈已显著回吐、接近止损位或动量明显衰竭时，应更果断reduce或exit。
6. 判断优先级：大盘系统性风险>板块趋势破坏>个股趋势破位>短时噪音。不得因固定持有天数退出，也不得因浮亏而扛单等待回本。
7. entry/reduce/exit必须返回price_low、price_high且下沿不高于上沿；区间围绕输入现价。wait/hold价格为null。
8. urgency只能是normal、warn、urgent。确认破位或风险放大用urgent；趋势转弱用warn；其余normal。
9. reason不超过120字，说明真正触发决策的层级，不得编造数据。
严格返回JSON：{"decisions":[{"position_id":1,"action":"hold","reason":"依据","price_low":null,"price_high":null,"urgency":"normal"}]}`
	request := map[string]interface{}{"model": s.config.Model, "temperature": 0.1, "response_format": map[string]string{"type": "json_object"}, "messages": []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": "逐只评估以下全部持仓：\n" + string(payload)}}}
	body, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("AI position request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("AI position HTTP %d: %.300s", resp.StatusCode, respBody)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil || len(envelope.Choices) == 0 {
		return fmt.Errorf("AI position response invalid")
	}
	var result struct {
		Decisions []positionDecision `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &result); err != nil {
		return fmt.Errorf("AI position JSON: %w", err)
	}
	if len(result.Decisions) != len(items) {
		return fmt.Errorf("AI position decision count=%d, want=%d", len(result.Decisions), len(items))
	}
	return s.applyPositionDecisions(ctx, tradeDate, items, result.Decisions)
}

func (s *Service) buildPositionAnalysisContext(ctx context.Context, now time.Time, positions []store.Position) ([]positionAnalysisItem, intradayMarketContext, error) {
	market := intradayMarketContext{CheckedAt: now.Format("2006-01-02 15:04"), Indices: []model.IndexQuote{}}
	var indices []model.IndexQuote
	var err error
	if s.marketProvider != nil {
		indexSymbols := []string{"SH000001", "SZ399001", "SZ399006", "SH000688", "SH000300", "BJ899050"}
		indexQuotes, fetchErr := s.marketProvider.BatchQuotes(ctx, indexSymbols)
		if fetchErr != nil {
			return nil, market, fmt.Errorf("获取实时指数失败: %w", fetchErr)
		}
		indices, market.MarketDate = indexQuotesToMarketContext(indexQuotes)
	} else {
		indices, err = s.st.LatestIndices(ctx)
		if err == nil {
			trading, tradingErr := s.st.IsTradingDay(ctx, now.Format("2006-01-02"), false)
			if tradingErr != nil {
				return nil, market, tradingErr
			}
			if trading {
				market.MarketDate = now.Format("2006-01-02")
			}
		}
	}
	if err != nil {
		return nil, market, err
	}
	market.Indices = indices
	for _, index := range indices {
		if index.ChangePct != nil {
			market.AveragePct += *index.ChangePct
			if *index.ChangePct < 0 {
				market.FallingCount++
			}
		}
	}
	if len(indices) > 0 {
		market.AveragePct /= float64(len(indices))
	}
	if s.marketProvider == nil {
		if at, err := s.st.LatestSnapshotTime(ctx); err == nil {
			market.SnapshotAt = at.In(shanghai()).Format("2006-01-02 15:04:05")
		}
	} else {
		market.SnapshotAt = now.Format("2006-01-02 15:04:05")
	}
	if g, err := s.st.LatestReviewGuidanceForRecommendation(ctx, now.Format("2006-01-02")); err == nil {
		market.MarketPhase, market.ReviewDate = g.MarketPhase, g.ReviewDate
	}

	symbols := make([]string, 0, len(positions))
	for _, p := range positions {
		symbols = append(symbols, p.Symbol)
	}
	var quotes []*model.Quote
	if s.marketProvider != nil {
		quotes, err = s.marketProvider.BatchQuotes(ctx, symbols)
		if err != nil {
			return nil, market, fmt.Errorf("获取自选股实时行情失败: %w", err)
		}
	} else {
		quotes, err = s.st.LatestQuotes(ctx, symbols)
	}
	if err != nil {
		return nil, market, err
	}
	quoteBySymbol := make(map[string]*model.Quote, len(quotes))
	for _, q := range quotes {
		if q != nil {
			quoteBySymbol[q.Symbol] = q
		}
	}
	sectors, err := s.st.SectorContextForSymbols(ctx, symbols)
	if err != nil {
		return nil, market, err
	}

	items := make([]positionAnalysisItem, 0, len(positions))
	for _, p := range positions {
		stage := store.EntryStageEntry
		if p.Status == store.PositionHolding {
			stage = store.EntryStageExit
		}
		item := positionAnalysisItem{PositionID: p.ID, Symbol: p.Symbol, Name: p.Name, Stage: stage, PickDate: p.PickDate, EntryDate: p.EntryDate, EntryPrice: p.EntryPrice, HighestPrice: p.HighestPrice, PositionPct: p.PositionPct, HoldDays: p.HoldDays, Sector: sectors[p.Symbol]}
		if stage == store.EntryStageEntry {
			item.GraceDaysUsed, err = s.st.TradingDaysSince(ctx, p.PickDate, now.Format("2006-01-02"))
		} else {
			item.HoldDays, err = s.st.TradingDaysSince(ctx, p.EntryDate, now.Format("2006-01-02"))
			item.HoldDays++
			if err == nil {
				err = s.st.UpdatePositionHoldDays(ctx, p.ID, item.HoldDays)
			}
		}
		if err != nil {
			return nil, market, err
		}
		q := quoteBySymbol[p.Symbol]
		if q == nil || q.Price == nil {
			return nil, market, fmt.Errorf("%s 实时行情缺失，终止本轮避免基于陈旧价格决策", p.Symbol)
		}
		item.Name, item.Price, item.ChangePct = q.Name, q.Price, q.ChangePct
		klines, err := s.st.QueryKlines(ctx, p.Symbol, "day", "qfq", "", "", 60)
		if err != nil {
			return nil, market, err
		}
		fillPositionIndicators(&item, klines)
		// 把风控状态一并交给 AI：让它知道止损位、当前浮盈与峰值回撤，
		// 避免给出与本地纪律冲突的建议。
		if item.EntryPrice != nil && *item.EntryPrice > 0 && item.Price != nil {
			profit := (*item.Price/(*item.EntryPrice) - 1) * 100
			item.ProfitPct = &profit
			stop := *item.EntryPrice * (1 - stopLossDistancePct(item.ATRPct)/100)
			item.StopLossPrice = &stop
			if item.HighestPrice != nil && *item.HighestPrice > 0 {
				peak := (*item.HighestPrice/(*item.EntryPrice) - 1) * 100
				item.PeakProfitPct = &peak
			}
		}
		items = append(items, item)
	}
	return items, market, nil
}

// indexQuotesToMarketContext 汇总指数行情，并按多数投票确定行情源交易日。
// 取第一个非空时间戳容易被个别指数的缓存时间误导，进而把交易日误判为休市；
// 多数投票在单一指数异常时仍能给出正确日期。
func indexQuotesToMarketContext(quotes []*model.Quote) ([]model.IndexQuote, string) {
	names := map[string]string{"SH000001": "上证指数", "SZ399001": "深证成指", "SZ399006": "创业板指", "SH000688": "科创50", "SH000300": "沪深300", "BJ899050": "北证50"}
	indices := make([]model.IndexQuote, 0, len(quotes))
	dateVotes := make(map[string]int, len(quotes))
	for _, quote := range quotes {
		if quote == nil {
			continue
		}
		indices = append(indices, model.IndexQuote{Symbol: quote.Symbol, Name: names[quote.Symbol], Price: quote.Price, ChangePct: quote.ChangePct, Amount: quote.Amount, Volume: quote.Volume})
		if quote.ProviderTimestamp == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339, quote.ProviderTimestamp); err == nil {
			dateVotes[at.In(shanghai()).Format("2006-01-02")]++
		}
	}
	marketDate, best := "", 0
	for date, votes := range dateVotes {
		// 票数相同时取较新的日期，保证结果稳定不受 map 遍历顺序影响。
		if votes > best || (votes == best && date > marketDate) {
			marketDate, best = date, votes
		}
	}
	return indices, marketDate
}

func fillPositionIndicators(item *positionAnalysisItem, klines []model.Kline) {
	// 需要访问 closes[last-20]，因此至少要 21 根；原来的 <20 守卫会在恰好 20 根时
	// 越界 panic，而盘中分析是整批处理，一只新股就能中断当轮全部持仓的风控。
	if len(klines) < 21 {
		return
	}
	closes := make([]float64, len(klines))
	for i, k := range klines {
		closes[i] = k.Close
	}
	last := len(closes) - 1
	avg := func(n int) float64 {
		var sum float64
		for _, v := range closes[len(closes)-n:] {
			sum += v
		}
		return sum / float64(n)
	}
	item.MA5, item.MA10, item.MA20 = avg(5), avg(10), avg(20)
	if closes[last-5] > 0 {
		item.Gain5Pct = (closes[last]/closes[last-5] - 1) * 100
	}
	if closes[last-20] > 0 {
		item.Gain20Pct = (closes[last]/closes[last-20] - 1) * 100
	}
	var sum float64
	for i := last - 4; i <= last; i++ {
		sum += float64(klines[i].Volume)
	}
	if sum > 0 {
		item.VolRatio5 = float64(klines[last].Volume) / (sum / 5)
	}
	item.ATRPct = atrPercent(klines)
}

// applyRiskControls 在 AI 判断之前执行确定性风控。
//
// 顺序上必须先于 AI：止损、移动止盈、时间止损属于不可协商的纪律，
// 若交给 AI 判断会出现「AI 说 hold，亏损继续扩大」的情况——这正是原实现
// 亏损失控的根因。AI 只负责风控未命中时的相机决策。
//
// 返回仍需 AI 评估的标的；已被风控清仓的标的不再进入 AI 请求。
func (s *Service) applyRiskControls(ctx context.Context, date string, tailSlot bool, market intradayMarketContext, items []positionAnalysisItem) ([]positionAnalysisItem, error) {
	remaining := make([]positionAnalysisItem, 0, len(items))
	for _, item := range items {
		if item.Stage != store.EntryStageExit || item.Price == nil || item.EntryPrice == nil {
			remaining = append(remaining, item)
			continue
		}
		price := *item.Price
		// 先推进持仓期极值，移动止盈必须基于持仓期间真实峰值。
		if err := s.st.UpdatePositionExtremes(ctx, item.PositionID, price); err != nil {
			return nil, err
		}
		highest := price
		if item.HighestPrice != nil && *item.HighestPrice > highest {
			highest = *item.HighestPrice
		}

		decision := evaluateRisk(riskInput{
			Price:        price,
			EntryPrice:   *item.EntryPrice,
			HighestPrice: highest,
			HoldDays:     item.HoldDays,
			PositionPct:  item.PositionPct,
			ATRPct:       item.ATRPct,
			MA10:         item.MA10,
			MA20:         item.MA20,
			SectorWeak:   item.Sector.AvgChange5D < 0,
			MarketAvgPct: market.AveragePct,
			IndexTotal:   len(market.Indices),
			IndexFalling: market.FallingCount,
			IsTailSlot:   tailSlot,
		})
		if decision.Action == riskActionNone {
			remaining = append(remaining, item)
			continue
		}

		low, high := price*0.995, price*1.005
		advice := store.EntryAdviceInput{
			TradeDate: date, Symbol: item.Symbol, Source: store.EntrySourceRule,
			Stage: store.EntryStageExit, Reason: decision.Reason,
			PriceLow: &low, PriceHigh: &high, Urgency: store.EntryUrgencyUrgent,
			RefPrice: item.Price, Model: "local-risk",
		}

		if decision.Action == riskActionReduce {
			changePct := (price/(*item.EntryPrice) - 1) * 100
			advice.Action = store.EntryActionReduce
			if err := s.st.SaveEntryAdvice(ctx, advice); err != nil {
				return nil, err
			}
			leftPct, err := s.st.ReducePosition(ctx, item.PositionID, date, price, store.PositionReducePct, changePct, decision.Reason)
			if err != nil {
				return nil, err
			}
			// 剩余仓位低于最小阈值时必须立刻清仓，否则会留下既不满足减仓条件、
			// 又永远等不到清仓的「僵尸持仓」，长期占用自选位且继续承担风险。
			if leftPct < store.PositionMinPositionPct {
				reason := fmt.Sprintf("%s（剩余仓位%.0f%%低于最小持仓阈值，全部清仓）", decision.Reason, leftPct)
				if err := s.st.MarkPositionExited(ctx, item.PositionID, date, item.Price, reason, decision.Kind, item.Symbol); err != nil {
					return nil, err
				}
				continue
			}
			// 本轮已执行确定性减仓的标的不再交给 AI，避免同一时段被连续减仓两次。
			item.PositionPct = leftPct
			continue
		}

		advice.Action = store.EntryActionExit
		if err := s.st.SaveEntryAdvice(ctx, advice); err != nil {
			return nil, err
		}
		if err := s.st.MarkPositionExited(ctx, item.PositionID, date, item.Price, decision.Reason, decision.Kind, item.Symbol); err != nil {
			return nil, err
		}
	}
	return remaining, nil
}

// applyPositionDecisions 先全量校验、再统一执行。
//
// 原实现边校验边写库：若第 3 只标的校验失败直接 return，前 2 只已经落库的
// 建仓/退出不会回滚，而调用方只记录日志，从而留下「部分应用」的不一致状态。
// 拆成两段后，任何一条非法决策都会在写库前整批拒绝。
func (s *Service) applyPositionDecisions(ctx context.Context, date string, items []positionAnalysisItem, decisions []positionDecision) error {
	byID := make(map[int64]positionAnalysisItem, len(items))
	for _, item := range items {
		byID[item.PositionID] = item
	}

	type plannedDecision struct {
		item     positionAnalysisItem
		decision positionDecision
	}
	planned := make([]plannedDecision, 0, len(decisions))
	seen := make(map[int64]bool, len(decisions))

	// 第一段：只校验，不产生任何副作用。
	for _, d := range decisions {
		item, ok := byID[d.PositionID]
		if !ok || seen[d.PositionID] {
			return fmt.Errorf("AI position returned unknown/duplicate id: %d", d.PositionID)
		}
		seen[d.PositionID] = true
		d.Reason = strings.TrimSpace(d.Reason)
		if d.Reason == "" || utf8.RuneCountInString(d.Reason) > 120 {
			return fmt.Errorf("AI position reason invalid: id=%d", d.PositionID)
		}
		if d.Urgency != store.EntryUrgencyNormal && d.Urgency != store.EntryUrgencyWarn && d.Urgency != store.EntryUrgencyUrgent {
			return fmt.Errorf("AI position urgency invalid: id=%d", d.PositionID)
		}
		valid := (item.Stage == store.EntryStageEntry && (d.Action == store.EntryActionEntry || d.Action == store.EntryActionWait)) || (item.Stage == store.EntryStageExit && (d.Action == store.EntryActionHold || d.Action == store.EntryActionReduce || d.Action == store.EntryActionExit))
		if !valid {
			return fmt.Errorf("AI position action %s invalid for stage %s", d.Action, item.Stage)
		}
		needsRange := d.Action == store.EntryActionEntry || d.Action == store.EntryActionReduce || d.Action == store.EntryActionExit
		if needsRange {
			if d.PriceLow == nil || d.PriceHigh == nil || *d.PriceLow <= 0 || *d.PriceLow > *d.PriceHigh {
				return fmt.Errorf("AI position price range invalid: id=%d", d.PositionID)
			}
			if item.Price != nil && (*d.PriceLow < *item.Price*0.8 || *d.PriceHigh > *item.Price*1.2) {
				return fmt.Errorf("AI position price range too far from current price: id=%d", d.PositionID)
			}
		} else {
			d.PriceLow, d.PriceHigh = nil, nil
		}
		// 改变仓位的动作必须有可用的建仓价与现价，否则收益无法结算。
		if (d.Action == store.EntryActionReduce || d.Action == store.EntryActionExit) &&
			(item.Price == nil || item.EntryPrice == nil || *item.EntryPrice <= 0) {
			return fmt.Errorf("AI position %s lacks entry/current price: id=%d", d.Action, d.PositionID)
		}
		planned = append(planned, plannedDecision{item: item, decision: d})
	}

	// 第二段：校验全部通过后才写库。
	for _, p := range planned {
		item, d := p.item, p.decision

		// 建仓价必须落在 AI 给出的可执行区间内才成交：AI 的区间本意是「回踩到这里再买」，
		// 若一律按触发时市价记账，会系统性抬高建仓成本（常在冲高时段触发 entry）。
		// 现价高于区间上沿时不建仓，保留 pending_entry 等待后续时段回落。
		if d.Action == store.EntryActionEntry && item.Price != nil && d.PriceHigh != nil && *item.Price > *d.PriceHigh {
			waitReason := fmt.Sprintf("现价%.2f高于建议建仓上沿%.2f，等待回落至区间再建仓", *item.Price, *d.PriceHigh)
			if err := s.st.SaveEntryAdvice(ctx, store.EntryAdviceInput{
				TradeDate: date, Symbol: item.Symbol, Source: store.EntrySourceHourlyAI,
				Stage: item.Stage, Action: store.EntryActionWait, Reason: waitReason,
				Urgency: store.EntryUrgencyNormal, RefPrice: item.Price, Model: s.config.Model,
			}); err != nil {
				return err
			}
			continue
		}

		if err := s.st.SaveEntryAdvice(ctx, store.EntryAdviceInput{TradeDate: date, Symbol: item.Symbol, Source: store.EntrySourceHourlyAI, Stage: item.Stage, Action: d.Action, Reason: d.Reason, PriceLow: d.PriceLow, PriceHigh: d.PriceHigh, Urgency: d.Urgency, RefPrice: item.Price, Model: s.config.Model}); err != nil {
			return err
		}
		switch d.Action {
		case store.EntryActionEntry:
			if err := s.st.MarkPositionEntered(ctx, item.PositionID, date, item.Price); err != nil {
				return err
			}
		case store.EntryActionReduce:
			// reduce 过去只落建议不改仓位，等于 AI 的减仓意图被静默丢弃、继续满仓持有。
			// 现在真实降低仓位并锁定该部分收益；减到最小阈值以下直接清仓。
			changePct := (*item.Price/(*item.EntryPrice) - 1) * 100
			leftPct, err := s.st.ReducePosition(ctx, item.PositionID, date, *item.Price, store.PositionReducePct, changePct, d.Reason)
			if err != nil {
				return err
			}
			if leftPct < store.PositionMinPositionPct {
				reason := fmt.Sprintf("%s（剩余仓位%.0f%%低于最小持仓阈值，全部清仓）", d.Reason, leftPct)
				if err := s.st.MarkPositionExited(ctx, item.PositionID, date, item.Price, reason, store.ExitKindAI, item.Symbol); err != nil {
					return err
				}
			}
		case store.EntryActionExit:
			if err := s.st.MarkPositionExited(ctx, item.PositionID, date, item.Price, d.Reason, store.ExitKindAI, item.Symbol); err != nil {
				return err
			}
		}
	}
	return nil
}

func nextEntryRun(now time.Time) time.Time {
	day := now
	for {
		if isRecommendationTradingDay(day) {
			for _, slot := range entryRunSlots {
				next := time.Date(day.Year(), day.Month(), day.Day(), slot/60, slot%60, 0, 0, now.Location())
				if next.After(now) {
					return next
				}
			}
		}
		day = day.AddDate(0, 0, 1)
		day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, now.Location())
	}
}
