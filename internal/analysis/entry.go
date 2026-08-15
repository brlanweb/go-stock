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
	HoldDays      int                 `json:"hold_days"`
	GraceDaysUsed int                 `json:"entry_grace_days_used,omitempty"`
	Price         *float64            `json:"price"`
	ChangePct     *float64            `json:"change_pct"`
	MA5           float64             `json:"ma5"`
	MA10          float64             `json:"ma10"`
	MA20          float64             `json:"ma20"`
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
		if err := s.st.ExpirePosition(ctx, p.ID, reason); err != nil {
			return err
		}
		if err := s.st.RemoveWatchlist(ctx, p.Symbol); err != nil {
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
	items, err = s.applyHardExitRules(ctx, tradeDate, market, items)
	if err != nil || len(items) == 0 {
		return err
	}

	payload, _ := json.Marshal(map[string]interface{}{"trade_date": tradeDate, "market": market, "positions": items})
	systemPrompt := `你是严格受限的A股盘中趋势交易评审器。输入仅来自本地数据库，包含实时大盘指数、上一交易日行业板块强弱、个股实时行情与日K派生指标。必须逐只评估positions，不能遗漏或增加标的。
核心原则：趋势发起阶段寻找高胜率建仓点；建仓后不按固定天数退出，而是在趋势不再可持续、发生逆转，或大盘系统性风险明显放大时保护收益/规避风险。
1. stage=entry只能返回entry或wait。entry要求多头结构完整、现价未明显追高、板块未显著转弱，并给出可执行建仓价区间；否则wait。
2. stage=exit只能返回hold、reduce或exit。趋势仍健康则hold；趋势减速但未确认破位可reduce；个股趋势逆转、板块退潮、或系统性风险放大时exit。
3. 判断优先级：大盘系统性风险>板块趋势破坏>个股趋势破位>短时噪音。不得因固定持有天数退出。
4. entry/reduce/exit必须返回price_low、price_high且下沿不高于上沿；区间围绕输入现价。wait/hold价格为null。
5. urgency只能是normal、warn、urgent。系统性风险或确认破位退出用urgent；趋势转弱用warn；其余normal。
6. reason不超过120字，说明真正触发决策的层级，不得编造数据。
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
		item := positionAnalysisItem{PositionID: p.ID, Symbol: p.Symbol, Name: p.Name, Stage: stage, PickDate: p.PickDate, EntryDate: p.EntryDate, EntryPrice: p.EntryPrice, HoldDays: p.HoldDays, Sector: sectors[p.Symbol]}
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
		items = append(items, item)
	}
	return items, market, nil
}

func indexQuotesToMarketContext(quotes []*model.Quote) ([]model.IndexQuote, string) {
	names := map[string]string{"SH000001": "上证指数", "SZ399001": "深证成指", "SZ399006": "创业板指", "SH000688": "科创50", "SH000300": "沪深300", "BJ899050": "北证50"}
	indices := make([]model.IndexQuote, 0, len(quotes))
	marketDate := ""
	for _, quote := range quotes {
		if quote == nil {
			continue
		}
		indices = append(indices, model.IndexQuote{Symbol: quote.Symbol, Name: names[quote.Symbol], Price: quote.Price, ChangePct: quote.ChangePct, Amount: quote.Amount, Volume: quote.Volume})
		if marketDate == "" && quote.ProviderTimestamp != "" {
			if at, err := time.Parse(time.RFC3339, quote.ProviderTimestamp); err == nil {
				marketDate = at.In(shanghai()).Format("2006-01-02")
			}
		}
	}
	return indices, marketDate
}

func fillPositionIndicators(item *positionAnalysisItem, klines []model.Kline) {
	if len(klines) < 20 {
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
}

// 硬规则只覆盖高置信风险；普通回踩仍由 AI 结合三层上下文判断。
func (s *Service) applyHardExitRules(ctx context.Context, date string, market intradayMarketContext, items []positionAnalysisItem) ([]positionAnalysisItem, error) {
	remaining := make([]positionAnalysisItem, 0, len(items))
	for _, item := range items {
		if item.Stage != store.EntryStageExit || item.Price == nil {
			remaining = append(remaining, item)
			continue
		}
		price, reason := *item.Price, ""
		systemic := len(market.Indices) >= 3 && market.FallingCount == len(market.Indices) && market.AveragePct <= -2
		switch {
		case systemic:
			reason = fmt.Sprintf("主要指数全部下跌且平均跌幅%.2f%%，系统性风险显著放大，退出规避隔夜风险", market.AveragePct)
		case item.MA20 > 0 && price < item.MA20:
			reason = fmt.Sprintf("现价%.2f跌破MA20 %.2f，中期趋势结构已破坏", price, item.MA20)
		case item.MA10 > 0 && price < item.MA10 && (item.Sector.AvgChange5D < 0 || market.AveragePct <= -1):
			reason = fmt.Sprintf("现价%.2f跌破MA10 %.2f，且板块或大盘同步转弱", price, item.MA10)
		}
		if reason == "" {
			remaining = append(remaining, item)
			continue
		}
		low, high := price*0.995, price*1.005
		if err := s.st.SaveEntryAdvice(ctx, store.EntryAdviceInput{TradeDate: date, Symbol: item.Symbol, Source: store.EntrySourceRule, Stage: store.EntryStageExit, Action: store.EntryActionExit, Reason: reason, PriceLow: &low, PriceHigh: &high, Urgency: store.EntryUrgencyUrgent, RefPrice: item.Price, Model: "local-rule"}); err != nil {
			return nil, err
		}
		if err := s.st.MarkPositionExited(ctx, item.PositionID, date, item.Price, reason); err != nil {
			return nil, err
		}
		if err := s.st.RemoveWatchlist(ctx, item.Symbol); err != nil {
			return nil, err
		}
	}
	return remaining, nil
}

func (s *Service) applyPositionDecisions(ctx context.Context, date string, items []positionAnalysisItem, decisions []positionDecision) error {
	byID := make(map[int64]positionAnalysisItem, len(items))
	for _, item := range items {
		byID[item.PositionID] = item
	}
	seen := make(map[int64]bool, len(decisions))
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
		if err := s.st.SaveEntryAdvice(ctx, store.EntryAdviceInput{TradeDate: date, Symbol: item.Symbol, Source: store.EntrySourceHourlyAI, Stage: item.Stage, Action: d.Action, Reason: d.Reason, PriceLow: d.PriceLow, PriceHigh: d.PriceHigh, Urgency: d.Urgency, RefPrice: item.Price, Model: s.config.Model}); err != nil {
			return err
		}
		switch d.Action {
		case store.EntryActionEntry:
			if err := s.st.MarkPositionEntered(ctx, item.PositionID, date, item.Price); err != nil {
				return err
			}
		case store.EntryActionExit:
			if err := s.st.MarkPositionExited(ctx, item.PositionID, date, item.Price, d.Reason); err != nil {
				return err
			}
			if err := s.st.RemoveWatchlist(ctx, item.Symbol); err != nil {
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
