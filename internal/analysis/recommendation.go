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
	// AutoEntryEnabled 为 false 时，盘前只生成推荐供观察，不建立持仓生命周期。
	// 默认关闭，详见 config.Config.AutoEntryEnabled 的说明。
	AutoEntryEnabled bool
}

type marketQuoteProvider interface {
	BatchQuotes(context.Context, []string) ([]*model.Quote, error)
}

// globalQuoteProvider 隔夜外盘行情源（风险感知板块）。provider.Manager 实现。
type globalQuoteProvider interface {
	GlobalQuotes(context.Context) ([]model.GlobalQuote, error)
}

type Service struct {
	st             *store.Store
	marketProvider marketQuoteProvider
	globalProvider globalQuoteProvider
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
func (s *Service) SetMarketProvider(provider marketQuoteProvider) {
	s.marketProvider = provider
	// 行情源同时实现外盘接口（provider.Manager）时，风险感知层自动启用。
	if gp, ok := provider.(globalQuoteProvider); ok {
		s.globalProvider = gp
	}
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
	// 最近复盘提供市场阶段与策略优化指令。个股 risk_score 已在 store 层完成
	// 硬否决（>= RecommendationProductionMaxRisk 剔除），此处不再重复过滤。
	guidance, guidanceErr := s.st.LatestReviewGuidanceForRecommendation(ctx, analysisDate)
	if guidanceErr != nil {
		slog.Warn("读取每日复盘优化指令失败，本次按基础规则推荐", "err", guidanceErr)
		guidance = store.LatestReviewGuidance{}
	}
	// 指数风向门（含全球风险门）：red 表示系统性风险高，此时推荐数量收紧到
	// 最多 1 只且允许交白卷（见下方 maxPicks），yellow 收紧到 2 只。
	// 2026-09 复盘结论：此前 red/yellow 只注入提示词、不改变行为，导致
	// 2026-09-02 这类全市场 76% 下跌日仍被强制推出 3 只。
	gate, gateErr := s.st.MarketDirectionGate(ctx, analysisDate)
	if gateErr != nil {
		slog.Warn("计算指数风向门失败，按黄灯保守处理", "err", gateErr)
		gate = store.MarketGate{TradeDate: analysisDate, Level: store.MarketGateYellow, Reason: "指数风向数据不可用，保守降档"}
	}
	slog.Info("指数风向门判定", "level", gate.Level, "reason", gate.Reason)
	// 全球风险门：风险感知板块的盘前判定。用隔夜外盘（A50 夜盘/金龙/美股/
	// VIX/离岸人民币）提前识别千股跌停类系统性风险——境内指数门基于 T-1 收盘
	// 是滞后确认，外盘门在开盘前预警。融合取最严档位：只能收紧，不能放宽。
	globalGate := s.RunGlobalGate(ctx, now.Format("2006-01-02"))
	if merged := store.StricterGateLevel(gate.Level, globalGate.Level); merged != gate.Level {
		gate.Reason = mergedGateReason(gate, globalGate)
		gate.Level = merged
		slog.Info("全球风险门收紧风向档位", "level", gate.Level, "global_score", globalGate.Score)
	}
	// maxPicks 按风向档位收紧当日推荐上限。AI 可以在上限内自由选择更少，
	// 包括一只都不选（空仓）——市场没有值得参与的机会时交白卷是正确输出。
	maxPicks := recommendationMaxPicks(gate.Level)
	if gate.Level == store.MarketGateRed {
		slog.Warn("风险感知红灯，当日推荐上限收紧", "reason", gate.Reason, "max_picks", maxPicks)
	}
	// 候选池优先复用当日热点漏斗 final 报告（08:00 先于推荐运行）：漏斗已完成
	// “数据筛选→AI 产业链分析→数据回验”，其卡点概念成分股即为热点候选；
	// 漏斗缺失/过期或过滤后不足时回退到独立的题材热度候选池。
	candidates, candidateSource, err := s.candidatesReusingHotspot(ctx, analysisDate)
	if err != nil {
		return err
	}
	if len(candidates) < store.RecommendationCandidateMin {
		return fmt.Errorf("趋势与建仓空间过滤后可分析候选不足: got=%d min=%d source=%s", len(candidates), store.RecommendationCandidateMin, candidateSource)
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
	if gate.Level == store.MarketGateYellow {
		prompt += "\n当前指数风向为黄灯（" + gate.Reason + "）：市场环境偏弱，本次最多只能选 2 只，且必须在推荐理由中客观说明市场环境。若候选中没有结构足够扎实的标的，返回空列表比凑数更好。"
	}
	if gate.Level == store.MarketGateRed {
		prompt += "\n当前指数风向为红灯（" + gate.Reason + "）：市场系统性风险高，本次最多只能选 1 只，且只有在该标的趋势结构显著强于大盘、建仓空间充足时才可给出。默认应当返回空列表（不推荐任何标的），这是红灯环境下的正确输出，不是失败。"
	}
	if guidance.ReviewDate != "" {
		guidanceJSON, _ := json.Marshal(guidance)
		prompt += "\n以下是最近一次收盘复盘生成的结构化优化指令：" + string(guidanceJSON) +
			"。directives 用于调整趋势、板块强度、龙头地位与建仓空间的判断口径；market_phase 作为市场背景写入理由。高风险候选已在候选池构建阶段按确定性风险分完成剔除，你收到的候选均已通过风险闸门。"
	}
	prompt += fmt.Sprintf(` 只能从用户提供的%d只候选中选择，最多选 %d 只并按建仓机会排序，代码不得重复。选举顺序必须是：
1. 先比较 sector_heat、题材持续性和板块趋势，锁定当前最强且具备持续性的板块；
2. 再在强板块内优先选择 leader_rank 靠前、成交活跃、趋势结构完整的龙头；
3. 最后确认龙头仍有合理建仓空间，避免昨日接近涨停、短线严重过热等既有禁入形态。

宁缺毋滥：没有达到标准的候选一律不要选。返回少于 %d 只、甚至返回空列表 {"recommendations":[]}，都是完全合法且被鼓励的输出。不要为了凑满数量而降低标准——凑数推荐造成的亏损远大于错过机会的代价。只有当你能明确写出"强板块+龙头地位+建仓机会"三项依据时，才可以给出该标的。

候选覆盖多个强板块且你选择 2 只以上时应兼顾分散，不得全部来自同一板块；sector 必须逐字使用候选的 industry 字段，reason 不超过80字。
probability 字段是 0-100 的相对机会分，用于表达候选之间的相对强弱排序，不是胜率、不是统计概率，也不得解读为收益预期。`, len(candidates), maxPicks, maxPicks)
	request := map[string]interface{}{
		"model":           s.config.Model,
		"temperature":     0.2,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": prompt + ` 返回严格JSON：{"recommendations":[{"symbol":"SH600000","probability":72.5,"reason":"不超过80字","sector":"候选industry字段原值"}]}`},
			{"role": "user", "content": fmt.Sprintf("以下是唯一允许评审的%d只候选股。每只均包含热点题材、确定性趋势评分及最近60个交易日OHLCV；请返回其中最多%d只，达不到标准时返回空列表。\n", len(candidates), maxPicks) + string(payload)},
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
	// 允许 0..maxPicks 只：空列表是「今日无值得参与的机会」的合法结论，
	// 超出上限才是违规输出。
	if len(result.Recommendations) > maxPicks {
		return fmt.Errorf("AI recommendation count=%d exceeds max=%d (gate=%s)", len(result.Recommendations), maxPicks, gate.Level)
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
	// 不允许多只推荐全部同板块；候选池本身只有一个板块时无法满足，放行。
	// 仅在推荐数 ≥2 时校验：0 只（空仓）与 1 只不存在分散问题。
	candidateSectors := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		candidateSectors[candidate.Industry] = true
	}
	if len(result.Recommendations) >= 2 && len(candidateSectors) >= 2 {
		pickSectors := make(map[string]bool, len(result.Recommendations))
		for _, item := range result.Recommendations {
			pickSectors[item.Sector] = true
		}
		if len(pickSectors) < 2 {
			return fmt.Errorf("AI returned %d picks from single sector: %s", len(result.Recommendations), result.Recommendations[0].Sector)
		}
	}
	if err := s.st.ReplaceRecommendations(ctx, analysisDate, s.config.Model, result.Recommendations); err != nil {
		return err
	}
	// 运行留痕必须落库：它是「最近推荐日」的权威来源，空仓日没有推荐行，
	// 缺失留痕会让前端回退展示上一个交易日的旧推荐。
	if err := s.st.SaveRecommendationRun(ctx, store.RecommendationRun{
		AnalysisDate:   analysisDate,
		GateLevel:      gate.Level,
		GateReason:     gate.Reason,
		MaxPicks:       maxPicks,
		PickCount:      len(result.Recommendations),
		CandidateCount: len(candidates),
		ModelName:      s.config.Model,
	}); err != nil {
		return fmt.Errorf("保存推荐运行留痕失败: %w", err)
	}
	if len(result.Recommendations) == 0 {
		// 空仓日：明确留痕，便于复盘统计「主动不参与」的天数与后续市场表现。
		slog.Info("AI 判定当日无值得参与的机会，返回空推荐（空仓）", "date", analysisDate, "gate", gate.Level, "candidates", len(candidates))
		return nil
	}
	// AI 只负责生成推荐。加入自选、建仓和平仓都必须由用户主动操作，
	// 风险门和历史配置不再拥有改变持仓或自选的权限。
	slog.Info("AI 推荐已生成，仅供用户选择加入自选", "date", analysisDate, "picks", len(result.Recommendations), "max_picks", maxPicks, "gate", gate.Level)
	return nil
}

// recommendationMaxPicks 按指数风向档位给出当日推荐数量上限。
// 这是「空仓权」的核心：市场越弱允许推的越少，AI 始终可以在上限内选更少
// 甚至不选。绿灯 3 只 / 黄灯 2 只 / 红灯 1 只，任何档位都允许 0 只。
func recommendationMaxPicks(level string) int {
	switch level {
	case store.MarketGateRed:
		return 1
	case store.MarketGateYellow:
		return 2
	default:
		return 3
	}
}

// AutoEntryEnabled 固定返回 false：AI 永不自动建仓。
func (s *Service) AutoEntryEnabled() bool { return false }

// DailyEntryPickCount 是每个推荐日纳入生命周期的唯一最强标的数量。
const DailyEntryPickCount = 1

// selectEntryPicks 按 AI 概率降序、AI 排名升序做确定性兜底；RiskScore
// 只展示，不参与排序。同时保证板块分散。
func selectEntryPicks(items []model.StockRecommendation, limit int) []model.StockRecommendation {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	ranked := make([]model.StockRecommendation, len(items))
	copy(ranked, items)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Probability != b.Probability {
			return a.Probability > b.Probability
		}
		return a.Rank < b.Rank
	})

	picks := make([]model.StockRecommendation, 0, limit)
	usedSectors := make(map[string]bool, limit)
	// 第一轮：每个板块最多取一只，优先保证分散。
	for _, item := range ranked {
		if len(picks) == limit {
			break
		}
		if usedSectors[item.Sector] {
			continue
		}
		usedSectors[item.Sector] = true
		picks = append(picks, item)
	}
	// 第二轮：板块数不足时用剩余高分候选补齐。
	if len(picks) < limit {
		chosen := make(map[string]bool, len(picks))
		for _, p := range picks {
			chosen[p.Symbol] = true
		}
		for _, item := range ranked {
			if len(picks) == limit {
				break
			}
			if !chosen[item.Symbol] {
				picks = append(picks, item)
			}
		}
	}
	return picks
}

// selectBestEntryPick 返回最适合建仓的单只（保留给需要唯一首选的场景）。
func selectBestEntryPick(items []model.StockRecommendation) *model.StockRecommendation {
	picks := selectEntryPicks(items, 1)
	if len(picks) == 0 {
		return nil
	}
	return &picks[0]
}

// autoWatchBestEntryPick 把当日最佳建仓候选加入自选并记录为 daily_pick。
// now 是推荐运行日（交易日盘前），自选与建仓记录都挂在当天。
// 自选容量不足时逐只降级跳过，不影响已成功入池的标的。
func (s *Service) autoWatchBestEntryPick(ctx context.Context, now time.Time, analysisDate string, items []model.StockRecommendation) {
	picks := selectEntryPicks(items, DailyEntryPickCount)
	if len(picks) == 0 {
		return
	}
	tradeDate := now.Format("2006-01-02")
	opened := 0
	for _, best := range picks {
		if err := s.st.AddLifecycleWatchlist(ctx, best.Symbol); err != nil {
			slog.Warn("建仓候选加入自选失败", "symbol", best.Symbol, "err", err)
			continue
		}
		reason := fmt.Sprintf("当日推荐建仓候选（概率 %.1f）：%s", best.Probability, best.Reason)
		if err := s.st.OpenPosition(ctx, best.Symbol, tradeDate, analysisDate); err != nil {
			slog.Warn("建立趋势持仓生命周期失败", "symbol", best.Symbol, "err", err)
			continue
		}
		if err := s.st.SaveEntryAdvice(ctx, store.EntryAdviceInput{
			TradeDate: tradeDate, Symbol: best.Symbol, Source: store.EntrySourceDailyPick,
			Stage: store.EntryStageEntry, Action: store.EntryActionPick, Reason: reason,
			Urgency: store.EntryUrgencyNormal, Model: s.config.Model,
		}); err != nil {
			slog.Warn("记录当日建仓候选失败", "symbol", best.Symbol, "err", err)
			continue
		}
		opened++
	}
	slog.Info("建仓候选已加入自选", "count", opened, "date", tradeDate)
}

// candidatesReusingHotspot 优先复用当日热点漏斗 final 报告生成候选池；
// 返回值第二项标识候选来源（hotspot_funnel / sector_heat），用于日志与排错。
func (s *Service) candidatesReusingHotspot(ctx context.Context, analysisDate string) ([]store.RecommendationCandidate, string, error) {
	concepts, err := s.hotspotConceptsForDate(ctx, analysisDate)
	if err != nil {
		slog.Warn("读取热点漏斗报告失败，回退题材热度候选池", "err", err)
	} else if len(concepts) > 0 {
		candidates, err := s.st.RecommendationCandidatesFromHotspot(ctx, concepts)
		if err != nil {
			return nil, "", err
		}
		if len(candidates) >= store.RecommendationCandidateMin {
			return candidates, "hotspot_funnel", nil
		}
		slog.Warn("热点漏斗候选过滤后不足，回退题材热度候选池", "got", len(candidates), "min", store.RecommendationCandidateMin)
	}
	candidates, err := s.st.RecommendationCandidates(ctx)
	if err != nil {
		return nil, "", err
	}
	// 泛概念熔断：回退池按题材人气排序，一旦规模闸门与名称黑名单都没拦住新的
	// 统计标签（例如交易所新增概念），选出的就是没有产业逻辑的「伪题材」股。
	// 此时宁可当日不推荐，也不能把这类标的送进 AI 评审——2026-08-11~14 的
	// 连续亏损正是由「融资融券」类回退候选造成的。
	if blocked := store.GenericConceptNames(candidates); len(blocked) > 0 {
		return nil, "", fmt.Errorf("回退候选池命中泛概念，放弃当日推荐: %s", strings.Join(blocked, "/"))
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
	// 风险感知（全球风险门）独立于 AI 配置：交易日 08:05 盘前采集隔夜外盘
	// 并落库判定，先于 08:10 推荐链路，供其直接复用；盘中风控亦读取该结论。
	go func() {
		for {
			now := time.Now().In(shanghai())
			next := nextGlobalGateRun(now)
			select {
			case <-time.After(time.Until(next)):
				runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				gate := s.RunGlobalGate(runCtx, next.Format("2006-01-02"))
				slog.Info("盘前风险感知完成", "level", gate.Level, "score", gate.Score)
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()
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
