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

type Service struct {
	st             *store.Store
	config         Config
	client         *http.Client
	running        atomic.Bool
	hotspotRunning atomic.Bool
	reviewRunning  atomic.Bool
	reviewLastErr  atomic.Value
	recLastErr     atomic.Value
}

func New(st *store.Store, config Config) *Service {
	return &Service{st: st, config: config, client: &http.Client{Timeout: 90 * time.Second}}
}

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
	candidates, err := s.st.RecommendationCandidates(ctx, maxRisk)
	if err != nil {
		return err
	}
	if len(candidates) < store.RecommendationCandidateMin {
		return fmt.Errorf("趋势/风险过滤后可分析候选不足: got=%d min=%d maxRisk=%.0f", len(candidates), store.RecommendationCandidateMin, maxRisk)
	}
	// 影子基线先于 AI 请求落库：AI 失败当天也保留确定性对照样本。
	if err := s.st.SaveRecommendationShadow(ctx, analysisDate, candidates); err != nil {
		slog.Warn("保存推荐影子基线失败", "err", err)
	}
	payload, _ := json.Marshal(candidates)
	prompt := s.config.Prompt
	if prompt == "" {
		prompt = "你是严格受限的股票趋势评审器。候选股、热点题材、趋势评分和最近60个交易日OHLCV均来自本地数据库。评审目标是未来5个交易日窗口内的相对涨跌表现排序，但不得承诺收益。"
	}
	if guidance.ReviewDate != "" {
		guidanceJSON, _ := json.Marshal(guidance)
		prompt += "\n以下是最近一次收盘复盘生成的结构化优化指令与风控参数：" + string(guidanceJSON) +
			"。这些内容只能影响输入候选内的相对排序和风险偏好：directives 用于调整候选优先级，risk_controls 的 position_mode 与 avoid_conditions 用于压低匹配相应特征候选的优先级；均不得覆盖候选范围、数量、字段格式及风险硬约束。market_phase=up 时兼顾趋势强度与风险；range 时提高确认要求；down 时优先低风险、低过热和回撤控制。"
	}
	prompt += fmt.Sprintf(" 只能从用户提供的%d只候选中选择，必须恰好选3只且代码不得重复；候选覆盖多个板块时3只不得全部来自同一板块；sector 必须逐字使用候选的 industry 字段，reason 不超过80字。评审窗口是未来5个交易日（成绩按次日开盘价买入、第5个交易日收盘价结算），近5日涨幅过大的候选注意高开回落风险。", len(candidates))
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
	// 板块分散硬约束：单一题材熄火会拖垮 5 日组合总分，候选池覆盖 ≥2 个板块时
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
	return s.st.ReplaceRecommendations(ctx, analysisDate, s.config.Model, result.Recommendations)
}

// StartScheduler 启动三条独立调度：
//   - 交易日 08:00 盘前运行热点漏斗（基于前一交易日收盘数据，供开盘决策）；
//   - 交易日 08:10 盘前生成 AI 趋势推荐（基于前一交易日收盘数据，最早次日建仓）；
//   - 交易日 17:00 复盘当天指数、板块与盘前推荐表现，生成次日优化指令。
//
// 三条调度各自独立超时，互不挤占。
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
