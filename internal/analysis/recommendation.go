package analysis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/store"
)

type Config struct {
	BaseURL       string
	APIKey        string
	Model         string
	Prompt        string
	HotspotPrompt string
	ReviewPrompt  string
}

type marketQuoteProvider interface {
	BatchQuotes(context.Context, []string) ([]*model.Quote, error)
}

type Service struct {
	st             *store.Store
	marketProvider marketQuoteProvider
	config         Config
	client         *http.Client
	running        atomic.Bool
	hotspotRunning atomic.Bool
	reviewRunning  atomic.Bool
	entryRunning   atomic.Bool
	reviewLastErr  atomic.Value
	recLastErr     atomic.Value
	entryLastErr   atomic.Value
}

func New(st *store.Store, config Config) *Service {
	return &Service{st: st, config: config, client: &http.Client{Timeout: 90 * time.Second}}
}

// SetMarketProvider 注入实时行情源。盘中生命周期分析必须优先使用实时行情，
// 本地 market_snapshot 仅作为测试或旧部署兼容回退。
func (s *Service) SetMarketProvider(provider marketQuoteProvider) { s.marketProvider = provider }

func (s *Service) Enabled() bool {
	return s.config.BaseURL != "" && s.config.APIKey != "" && s.config.Model != ""
}

func (s *Service) Running() bool { return s.running.Load() }

func (s *Service) RecommendationLastError() string {
	if value := s.recLastErr.Load(); value != nil {
		return value.(string)
	}
	return ""
}

func (s *Service) SetRecommendationLastError(err error) {
	if err == nil {
		s.recLastErr.Store("")
		return
	}
	s.recLastErr.Store(err.Error())
}

func (s *Service) ChatStock(ctx context.Context, symbol, question, ctxText string) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("AI 推荐未配置")
	}
	systemPrompt := "你是 go-stock 的 AI 行情助理，仅基于用户在消息中提供的本地数据库字段回答。回答用简洁中文，不要编造数据。"
	user := "已携带本地数据库内容如下：\n" + ctxText + "\n\n用户问题：\n" + question
	reqBody := map[string]interface{}{
		"model":       s.config.Model,
		"temperature": 0.2,
		"max_tokens":  600,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": user},
		},
	}
	body, _ := json.Marshal(reqBody)
	url := strings.TrimRight(s.config.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("agent request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("agent HTTP %d: %.300s", resp.StatusCode, respBody)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil || len(envelope.Choices) == 0 {
		return "", fmt.Errorf("agent response invalid")
	}
	return strings.TrimSpace(envelope.Choices[0].Message.Content), nil
}

func (s *Service) ChatStockStream(ctx context.Context, symbol, question, ctxText string, history []store.AgentChatMessage, emit func(string) error) error {
	if !s.Enabled() {
		return fmt.Errorf("AI 推荐未配置")
	}
	messages := []map[string]string{{"role": "system", "content": "你是 go-stock 的 AI 行情助理，仅基于用户提供的本地数据库字段回答。回答用简洁中文，不要编造数据。"}}
	for _, message := range history {
		if (message.Role == "user" || message.Role == "assistant") && strings.TrimSpace(message.Content) != "" {
			messages = append(messages, map[string]string{"role": message.Role, "content": message.Content})
		}
	}
	messages = append(messages, map[string]string{"role": "user", "content": "本轮携带的本地数据库上下文如下：\n" + ctxText + "\n\n用户问题：\n" + question})
	reqBody := map[string]interface{}{
		"model":       s.config.Model,
		"temperature": 0.2,
		"max_tokens":  600,
		"stream":      true,
		"messages":    messages,
	}
	body, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("agent stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("agent HTTP %d: %.300s", resp.StatusCode, respBody)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("agent stream response invalid: %w", err)
		}
		if chunk.Error != nil {
			return fmt.Errorf("agent: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if err := emit(choice.Delta.Content); err != nil {
					return err
				}
			}
		}
	}
	return scanner.Err()
}

func (s *Service) RunDaily(ctx context.Context) error {
	return s.runDailyAt(ctx, time.Now().In(shanghai()))
}

func (s *Service) runDailyAt(ctx context.Context, now time.Time) error {
	if !s.Enabled() {
		return fmt.Errorf("AI 推荐未配置")
	}
	if !isRecommendationTradingDay(now) {
		return fmt.Errorf("非交易日不生成 AI 推荐")
	}
	if !s.running.CompareAndSwap(false, true) {
		return fmt.Errorf("AI 推荐任务正在执行")
	}
	s.SetRecommendationLastError(nil)
	defer s.running.Store(false)
	analysisDate, err := s.st.LatestKlineDate(ctx)
	if err != nil {
		return err
	}
	// 盘前 08:00 运行：分析基准是最近一个已收盘交易日（周一早晨为上周五）。
	// 只要求日 K 不早于上一交易日，避免数据长期停滞时基于陈旧行情推荐。
	if analysisDate == "" || analysisDate < previousTradingDay(now).Format("2006-01-02") {
		return fmt.Errorf("最近收盘日 K 尚未就绪: latest=%s", analysisDate)
	}
	// 候选风险上限由最近一次复盘的 market_phase 自动决定（up 放宽 / down 收紧），
	// 因此 guidance 必须先于候选池读取；读取失败时回退到基准风险上限。
	guidance, guidanceErr := s.st.LatestReviewGuidanceForRecommendation(ctx, analysisDate)
	if guidanceErr != nil {
		slog.Warn("读取每日复盘优化指令失败，本次按基础规则推荐", "err", guidanceErr)
		guidance = store.LatestReviewGuidance{}
	}
	maxRisk := store.RecommendationMaxRiskScore(guidance.MarketPhase)
	// 候选池优先复用当日热点漏斗 final 报告（08:00 先于推荐运行）：漏斗已完成
	// “数据筛选→AI 产业链分析→数据回验”，其卡点概念成分股即为热点候选；
	// 漏斗缺失/过期或过滤后不足时回退到独立的题材热度候选池。
	candidates, candidateSource, err := s.candidatesReusingHotspot(ctx, analysisDate, maxRisk)
	if err != nil {
		return err
	}
	if len(candidates) < store.RecommendationCandidateMin {
		return fmt.Errorf("趋势/风险过滤后可分析候选不足: got=%d min=%d maxRisk=%.0f source=%s", len(candidates), store.RecommendationCandidateMin, maxRisk, candidateSource)
	}
	slog.Info("AI 推荐候选池就绪", "source", candidateSource, "count", len(candidates))
	// 影子基线先于 AI 请求落库：AI 失败当天也保留确定性对照样本。
	if err := s.st.SaveRecommendationShadow(ctx, analysisDate, candidates); err != nil {
		slog.Warn("保存推荐影子基线失败", "err", err)
	}
	payload, _ := json.Marshal(candidates)
	prompt := s.config.Prompt
	if prompt == "" {
		prompt = "你是严格受限的股票趋势评审器。候选股、热点题材、趋势评分和最近60个交易日OHLCV均来自本地数据库。评审目标是在趋势跟踪口径下选择结构最完整、可持续性最强的候选，不设固定持有天数，也不得承诺收益。"
	}
	if guidance.ReviewDate != "" {
		guidanceJSON, _ := json.Marshal(guidance)
		prompt += "\n以下是最近一次收盘复盘生成的结构化优化指令与风控参数：" + string(guidanceJSON) +
			"。这些内容只能影响输入候选内的相对排序和风险偏好：directives 用于调整候选优先级，risk_controls 的 position_mode 与 avoid_conditions 用于压低匹配相应特征候选的优先级；均不得覆盖候选范围、数量、字段格式及风险硬约束。market_phase=up 时兼顾趋势强度与风险；range 时提高确认要求；down 时优先低风险、低过热和回撤控制。"
	}
	prompt += fmt.Sprintf(" 只能从用户提供的%d只候选中选择，必须恰好选3只并按趋势持续性与可建仓性排序，代码不得重复；候选覆盖多个板块时3只不得全部来自同一板块；sector 必须逐字使用候选的 industry 字段，reason 不超过80字。排名第一者将在当日盘中寻找合适建仓区间，建仓后由 AI 综合大盘、板块、个股持续寻找退出机会；请优先选择趋势结构完整、仍有合理建仓空间且可持续性强的候选；近5日涨幅过大的候选注意高开回落与趋势透支风险。", len(candidates))
	request := map[string]interface{}{
		"model":           s.config.Model,
		"temperature":     0.2,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": prompt + ` 返回严格JSON：{"recommendations":[{"symbol":"SH600000","probability":72.5,"reason":"不超过80字","sector":"候选industry字段原值"}]}`},
			{"role": "user", "content": fmt.Sprintf("以下是唯一允许评审的%d只候选股。每只均包含热点题材、确定性趋势评分及最近60个交易日OHLCV；请只返回其中3只。\n", len(candidates)) + string(payload)},
		},
	}
	body, _ := json.Marshal(request)
	url := strings.TrimRight(s.config.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	// 推荐主请求携带 10 只股 ×60 根日 K 的大 payload，慢模型下 90 秒偏紧；
	// 使用与调度预算（5 分钟）匹配的独立超时，s.client 仍供轻量对话使用。
	client := &http.Client{Timeout: 4 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("AI recommendation request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("AI recommendation HTTP %d: %.300s", resp.StatusCode, respBody)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil || len(envelope.Choices) == 0 {
		return fmt.Errorf("AI recommendation response invalid")
	}
	var result struct {
		Recommendations []model.StockRecommendation `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &result); err != nil {
		return fmt.Errorf("AI recommendation JSON: %w", err)
	}
	if len(result.Recommendations) != 3 {
		return fmt.Errorf("AI recommendation count=%d", len(result.Recommendations))
	}
	allowed := make(map[string]store.RecommendationCandidate, len(candidates))
	for _, candidate := range candidates {
		allowed[candidate.Symbol] = candidate
	}
	seen := make(map[string]bool, len(result.Recommendations))
	for i := range result.Recommendations {
		item := &result.Recommendations[i]
		if seen[item.Symbol] {
			return fmt.Errorf("AI returned duplicate symbol: %s", item.Symbol)
		}
		seen[item.Symbol] = true
		candidate, ok := allowed[item.Symbol]
		if !ok {
			return fmt.Errorf("AI returned unknown symbol: %s", item.Symbol)
		}
		if item.Probability < 0 || item.Probability > 100 {
			return fmt.Errorf("AI returned invalid probability for %s", item.Symbol)
		}
		item.Rank, item.Code, item.Name = i+1, candidate.Code, candidate.Name
		risk := candidate.RiskScore
		item.RiskScore = &risk
		item.Reason = strings.TrimSpace(item.Reason)
		if item.Reason == "" || utf8.RuneCountInString(item.Reason) > 80 {
			return fmt.Errorf("AI returned invalid reason for %s", item.Symbol)
		}
		if item.Sector != candidate.Industry {
			return fmt.Errorf("AI returned invalid sector for %s", item.Symbol)
		}
	}
	// 板块分散硬约束：单一题材熄火会拖累趋势组合，候选池覆盖 ≥2 个板块时
	// 不允许 3 只全部同板块；候选池本身只有一个板块时无法满足，放行。
	candidateSectors := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		candidateSectors[candidate.Industry] = true
	}
	if len(candidateSectors) >= 2 {
		pickSectors := make(map[string]bool, 3)
		for _, item := range result.Recommendations {
			pickSectors[item.Sector] = true
		}
		if len(pickSectors) < 2 {
			return fmt.Errorf("AI returned 3 picks from single sector: %s", result.Recommendations[0].Sector)
		}
	}
	if err := s.st.ReplaceRecommendations(ctx, analysisDate, s.config.Model, result.Recommendations); err != nil {
		return err
	}
	// 推荐落库成功后，从 3 只中确定性选出最适合建仓的一只并自动加入自选
	// （自选上限 10 只，AddWatchlist 内部自动淘汰最旧条目保持数据同步）。
	// 该步骤失败只告警，不影响推荐主结果。
	s.autoWatchBestEntryPick(ctx, now, analysisDate, result.Recommendations)
	return nil
}

// selectBestEntryPick 从当日 3 只推荐中确定性选出最适合建仓的一只：
// AI 概率最高者优先；概率持平取风险分更低者；再持平按 AI 排名兜底。
func selectBestEntryPick(items []model.StockRecommendation) *model.StockRecommendation {
	var best *model.StockRecommendation
	for i := range items {
		item := &items[i]
		if best == nil {
			best = item
			continue
		}
		if item.Probability > best.Probability {
			best = item
			continue
		}
		if item.Probability == best.Probability && item.RiskScore != nil && best.RiskScore != nil && *item.RiskScore < *best.RiskScore {
			best = item
		}
	}
	return best
}

// autoWatchBestEntryPick 把最佳建仓候选加入自选并记录为当日 daily_pick。
// now 是推荐运行日（交易日盘前），自选与建仓记录都挂在当天。
func (s *Service) autoWatchBestEntryPick(ctx context.Context, now time.Time, analysisDate string, items []model.StockRecommendation) {
	best := selectBestEntryPick(items)
	if best == nil {
		return
	}
	if err := s.st.AddLifecycleWatchlist(ctx, best.Symbol); err != nil {
		slog.Warn("最佳建仓股加入自选失败", "symbol", best.Symbol, "err", err)
		return
	}
	tradeDate := now.Format("2006-01-02")
	reason := fmt.Sprintf("当日推荐中最适合建仓（概率 %.1f）：%s", best.Probability, best.Reason)
	if err := s.st.OpenPosition(ctx, best.Symbol, tradeDate, analysisDate); err != nil {
		slog.Warn("建立趋势持仓生命周期失败", "symbol", best.Symbol, "err", err)
		return
	}
	if err := s.st.SaveEntryAdvice(ctx, store.EntryAdviceInput{
		TradeDate: tradeDate, Symbol: best.Symbol, Source: store.EntrySourceDailyPick,
		Stage: store.EntryStageEntry, Action: store.EntryActionPick, Reason: reason,
		Urgency: store.EntryUrgencyNormal, Model: s.config.Model,
	}); err != nil {
		slog.Warn("记录当日最佳建仓股失败", "symbol", best.Symbol, "err", err)
		return
	}
	slog.Info("最佳建仓股已自动加入自选", "symbol", best.Symbol, "date", tradeDate)
}

// candidatesReusingHotspot 优先复用当日热点漏斗 final 报告生成候选池；
// 返回值第二项标识候选来源（hotspot_funnel / sector_heat），用于日志与排错。
func (s *Service) candidatesReusingHotspot(ctx context.Context, analysisDate string, maxRisk float64) ([]store.RecommendationCandidate, string, error) {
	concepts, err := s.hotspotConceptsForDate(ctx, analysisDate)
	if err != nil {
		slog.Warn("读取热点漏斗报告失败，回退题材热度候选池", "err", err)
	} else if len(concepts) > 0 {
		candidates, err := s.st.RecommendationCandidatesFromHotspot(ctx, maxRisk, concepts)
		if err != nil {
			return nil, "", err
		}
		if len(candidates) >= store.RecommendationCandidateMin {
			return candidates, "hotspot_funnel", nil
		}
		slog.Warn("热点漏斗候选过滤后不足，回退题材热度候选池", "got", len(candidates), "min", store.RecommendationCandidateMin)
	}
	candidates, err := s.st.RecommendationCandidates(ctx, maxRisk)
	if err != nil {
		return nil, "", err
	}
	return candidates, "sector_heat", nil
}

// hotspotConceptsForDate 解析最近一次热点漏斗 final 报告；仅当报告基准日与本次
// 推荐分析日一致时才复用（跨日的旧漏斗结论不代表当日热点）。
func (s *Service) hotspotConceptsForDate(ctx context.Context, analysisDate string) ([]store.RecommendationHotspotConcept, error) {
	raw, err := s.st.LatestHotspotReport(ctx)
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	var report HotspotFinalReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("解析热点漏斗报告失败: %w", err)
	}
	if report.ReportDate != analysisDate {
		return nil, nil
	}
	concepts := make([]store.RecommendationHotspotConcept, 0, len(report.Concepts))
	for _, concept := range report.Concepts {
		concepts = append(concepts, store.RecommendationHotspotConcept{
			SectorCode: concept.SectorCode,
			SectorName: concept.SectorName,
			Confidence: concept.Confidence,
		})
	}
	return concepts, nil
}

// StartScheduler 启动四条独立调度：
//   - 交易日 08:00 盘前运行热点漏斗（基于前一交易日收盘数据，供开盘决策）；
//   - 交易日 08:10 盘前生成 AI 趋势推荐（候选池直接复用当日热点漏斗结论，
//     并自动把最适合建仓的一只加入自选）；
//   - 交易日盘中 10:00 至 14:52 按 30 分钟档执行全部活跃标的分析：
//     待建仓标的寻找建仓区间，已建仓标的持续寻找退出机会；
//   - 交易日 17:00 复盘当天指数、板块与盘前推荐表现，生成次日优化指令。
//
// 各条调度独立超时，互不挤占。
func (s *Service) StartScheduler(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	go func() {
		for {
			now := time.Now().In(shanghai())
			next := nextRecommendationRun(now)

			select {
			case <-time.After(time.Until(next)):
				runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				if err := s.runDailyAt(runCtx, next); err != nil {
					s.SetRecommendationLastError(err)
					slog.Warn("AI 推荐生成失败", "err", err)
				}
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		for {
			now := time.Now().In(shanghai())
			next := nextHotspotRun(now)

			select {
			case <-time.After(time.Until(next)):
				runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
				if err := s.RunHotspot(runCtx); err != nil {
					slog.Warn("AI 热点漏斗生成失败", "err", err)
				} else {
					slog.Info("AI 热点漏斗盘前分析完成")
				}
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		for {
			now := time.Now().In(shanghai())
			next := nextReviewRun(now)

			select {
			case <-time.After(time.Until(next)):
				runCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
				if err := s.runScheduledDailyReview(runCtx, next); err != nil {
					slog.Warn("AI 每日复盘失败", "err", err)
				} else {
					slog.Info("AI 每日复盘完成", "date", next.Format("2006-01-02"))
				}
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()
	// 交易时段按 30 分钟节奏分析全部活跃标的，建仓后切换为退出机会分析。
	go func() {
		for {
			now := time.Now().In(shanghai())
			next := nextEntryRun(now)

			select {
			case <-time.After(time.Until(next)):
				runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
				if err := s.RunEntryAnalysis(runCtx, next); err != nil {
					slog.Info("盘中趋势分析跳过或未产生动作", "err", err)
				} else {
					slog.Info("盘中趋势分析完成", "at", next.Format("15:04"))
				}
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func shanghai() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

func rankRecommendations(candidates []store.RecommendationCandidate) []model.StockRecommendation {
	type scored struct {
		candidate store.RecommendationCandidate
		score     float64
	}
	scores := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		klines := candidate.Klines
		if len(klines) < 2 || klines[0].Close <= 0 {
			continue
		}
		scores = append(scores, scored{candidate, klines[len(klines)-1].Close / klines[0].Close})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].score == scores[j].score {
			return scores[i].candidate.Symbol < scores[j].candidate.Symbol
		}
		return scores[i].score > scores[j].score
	})
	out := make([]model.StockRecommendation, 0, 3)
	for i := 0; i < 3 && i < len(scores); i++ {
		c := scores[i].candidate
		out = append(out, model.StockRecommendation{Rank: i + 1, Symbol: c.Symbol, Code: c.Code, Name: c.Name, Sector: c.Industry, Probability: 50 + scores[i].score})
	}
	return out
}

func isRecommendationTradingDay(now time.Time) bool {
	return now.Weekday() != time.Saturday && now.Weekday() != time.Sunday
}

// nextRecommendationRun 计算下一次盘前趋势推荐运行时间：交易日 08:10 Asia/Shanghai。
// 盘前生成基于前一收盘数据，最早次日开盘建仓，收益追踪从推荐日之后的交易日开始。
func nextRecommendationRun(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 8, 10, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	for !isRecommendationTradingDay(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// previousTradingDay 返回给定时刻的上一交易日（跳过周末，不含当天）。
func previousTradingDay(now time.Time) time.Time {
	day := now.AddDate(0, 0, -1)
	for !isRecommendationTradingDay(day) {
		day = day.AddDate(0, 0, -1)
	}
	return day
}

// nextHotspotRun 计算下一次盘前热点漏斗运行时间：交易日 08:00 Asia/Shanghai。
func nextHotspotRun(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	for !isRecommendationTradingDay(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
