package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hoax/go-stock/internal/store"
)

type ReviewSectorAssessment struct {
	SectorCode string `json:"sector_code"`
	SectorName string `json:"sector_name"`
	Strength   string `json:"strength"`
	Outlook    string `json:"outlook"`
	Risk       string `json:"risk"`
}

type ReviewPickAssessment struct {
	RecommendationDate string `json:"recommendation_date"`
	Symbol             string `json:"symbol"`
	Name               string `json:"name"`
	Verdict            string `json:"verdict"`
	Performance        string `json:"performance"`
	Attribution        string `json:"attribution"`
	NextAction         string `json:"next_action"`
}

// ReviewHotspotAssessment 逐项回验盘前热点概念在复盘日的实际表现。
type ReviewHotspotAssessment struct {
	SectorCode string `json:"sector_code"`
	Verdict    string `json:"verdict"`
	Assessment string `json:"assessment"`
}

// ReviewDirectiveAssessment 回验上一次复盘指令在本交易日的实际效果。
type ReviewDirectiveAssessment struct {
	Action  string `json:"action"`
	Verdict string `json:"verdict"`
	Comment string `json:"comment"`
}

type ReviewRiskControls struct {
	PositionMode      string   `json:"position_mode"`
	MaxPositionPct    float64  `json:"max_position_pct"`
	MaxSingleStockPct float64  `json:"max_single_stock_pct"`
	StopLossPct       float64  `json:"stop_loss_pct"`
	AvoidConditions   []string `json:"avoid_conditions"`
}

// DailyReviewReport 是展示、历史记录和次日推荐优化共用的结构化复盘结果。
type DailyReviewReport struct {
	ReviewDate            string                          `json:"review_date"`
	GeneratedAt           time.Time                       `json:"generated_at"`
	Model                 string                          `json:"model"`
	MarketPhase           string                          `json:"market_phase"`
	Confidence            float64                         `json:"confidence"`
	MarketSummary         string                          `json:"market_summary"`
	IndexReview           string                          `json:"index_review"`
	BreadthReview         string                          `json:"breadth_review"`
	SectorAssessments     []ReviewSectorAssessment        `json:"sector_assessments"`
	HotspotReviews        []ReviewHotspotAssessment       `json:"hotspot_reviews"`
	RecommendationReviews []ReviewPickAssessment          `json:"recommendation_reviews"`
	PrevDirectiveReviews  []ReviewDirectiveAssessment     `json:"previous_directive_reviews"`
	WhatWorked            []string                        `json:"what_worked"`
	WhatFailed            []string                        `json:"what_failed"`
	Directives            []store.RecommendationDirective `json:"directives"`
	RiskControls          ReviewRiskControls              `json:"risk_controls"`
	Facts                 store.DailyReviewFacts          `json:"facts"`
}

func (s *Service) ReviewRunning() bool { return s.reviewRunning.Load() }

func (s *Service) ReviewLastError() string {
	if value := s.reviewLastErr.Load(); value != nil {
		return value.(string)
	}
	return ""
}

func (s *Service) SetReviewLastError(err error) {
	if err == nil {
		s.reviewLastErr.Store("")
		return
	}
	s.reviewLastErr.Store(err.Error())
}

// RunDailyReview 手动基于库内最近一个收盘交易日执行复盘。
func (s *Service) RunDailyReview(ctx context.Context) error {
	return s.runDailyReview(ctx, time.Now().In(shanghai()), false)
}

func (s *Service) runScheduledDailyReview(ctx context.Context, now time.Time) error {
	return s.runDailyReview(ctx, now, true)
}

func (s *Service) runDailyReview(ctx context.Context, now time.Time, requireToday bool) error {
	if !s.Enabled() {
		return fmt.Errorf("AI 每日复盘未配置")
	}
	if requireToday && !isRecommendationTradingDay(now) {
		return fmt.Errorf("非交易日不执行每日复盘")
	}
	if !s.reviewRunning.CompareAndSwap(false, true) {
		return fmt.Errorf("每日复盘任务正在执行")
	}
	s.SetReviewLastError(nil)
	defer s.reviewRunning.Store(false)

	tradeDate, err := s.st.LatestKlineDate(ctx)
	if err != nil || tradeDate == "" {
		return fmt.Errorf("每日复盘缺少收盘日K: %w", err)
	}
	if requireToday && tradeDate != now.Format("2006-01-02") {
		return fmt.Errorf("当天收盘数据尚未就绪: latest=%s", tradeDate)
	}
	// 16:00 收盘任务通常已经计算板块统计；手动复盘时重算一次确保底稿完整。
	if err := s.st.RecomputeHotspotStats(ctx, tradeDate); err != nil {
		return fmt.Errorf("复盘板块统计: %w", err)
	}
	facts, err := s.st.DailyReviewFacts(ctx, tradeDate)
	if err != nil {
		return err
	}
	if facts.Breadth.StockCount == 0 || len(facts.Indices) == 0 {
		return fmt.Errorf("复盘事实不完整: stocks=%d indices=%d", facts.Breadth.StockCount, len(facts.Indices))
	}
	if err := s.st.SaveDailyReview(ctx, tradeDate, "facts", "", "", facts); err != nil {
		return err
	}

	report, err := s.analyzeDailyReviewWithAI(ctx, facts)
	if err != nil {
		return err
	}
	report.ReviewDate = tradeDate
	report.GeneratedAt = time.Now().In(shanghai())
	report.Model = s.config.Model
	report.Facts = facts
	if err := validateDailyReview(&report, facts); err != nil {
		return err
	}
	if err := s.st.SaveDailyReview(ctx, tradeDate, "review", report.MarketPhase, s.config.Model, report); err != nil {
		return err
	}

	// 复盘闭环的执行端：先用确定性事实生成并保存统一考核快照，再验收已过冻结期的
	// 参数调整，最后才处理本次 AI 提案。AI 只能提出 stop_loss 目标；是否调整、调整
	// 幅度、样本门槛、冻结期和回滚全部由数据库约束与确定性代码决定。
	scorecard, scoreErr := s.st.StrategyScorecardForWindow(ctx, 60, "all")
	if scoreErr != nil {
		slog.Warn("生成每日策略考核失败", "date", tradeDate, "error", scoreErr)
		return nil // 复盘报告已成功落库，辅助优化失败不得伪装成复盘失败
	}
	if err := s.st.SaveStrategyScorecard(ctx, tradeDate, scorecard); err != nil {
		slog.Warn("保存每日策略考核失败", "date", tradeDate, "error", err)
	}
	if err := s.st.EvaluateStrategyParamChanges(ctx, tradeDate, scorecard); err != nil {
		slog.Warn("验收策略参数调整失败", "date", tradeDate, "error", err)
	}
	if report.RiskControls.StopLossPct > 0 {
		rationale := fmt.Sprintf("复盘%s阶段建议止损%.1f%%；由样本门槛、单步幅度与冻结回滚约束执行", report.MarketPhase, report.RiskControls.StopLossPct)
		applied, skipped, err := s.st.ApplyStrategyParamProposal(ctx, "stop_loss_pct", report.RiskControls.StopLossPct, scorecard, tradeDate, "daily_review", rationale)
		switch {
		case err != nil:
			slog.Warn("应用策略参数提案失败", "date", tradeDate, "param", "stop_loss_pct", "error", err)
		case applied:
			slog.Info("复盘参数提案已生效", "date", tradeDate, "param", "stop_loss_pct",
				"proposed", report.RiskControls.StopLossPct, "trade_samples", scorecard.Overall.Samples,
				"selection_samples", scorecard.Stages.Selection.Samples)
		default:
			// 此前这里是静默丢弃，导致复盘连续多日建议调整却无人察觉、
			// 参数长期停留在默认值。改为显式留痕，便于监控闭环是否在运转。
			slog.Warn("复盘参数提案未生效", "date", tradeDate, "param", "stop_loss_pct",
				"proposed", report.RiskControls.StopLossPct, "reason", skipped,
				"trade_samples", scorecard.Overall.Samples,
				"selection_samples", scorecard.Stages.Selection.Samples)
		}
	}
	return nil
}

func (s *Service) analyzeDailyReviewWithAI(ctx context.Context, facts store.DailyReviewFacts) (DailyReviewReport, error) {
	payload, _ := json.Marshal(facts)
	systemPrompt := strings.TrimSpace(s.config.ReviewPrompt)
	if systemPrompt == "" {
		systemPrompt = `你是A股每日收盘复盘器。所有指数、市场宽度、板块、盘前热点预测和推荐表现都来自本地数据库。只按输入事实归因，不得虚构新闻、资金流、政策或盘中走势。目标是在上升阶段提升趋势收益，在下降阶段优先限制回撤，但不得承诺收益。
输入中的 market_stance（take_profit=落袋、hold=扛单、accumulate=扫货）是本地按等权大盘历史数据确定性推演的操作姿态，不是你的输出项：不得改写或反驳，但 market_summary 与 directives 应与其口径一致。
market_phase 只能是 up、range、down。up 要求指数和市场宽度大体共振；down 要求指数和市场宽度明显转弱；证据冲突时使用 range。推荐复盘必须按 date+symbol 逐条覆盖 latest_recommendations 中全部记录，区分选股贡献与大盘系统性影响。
归因基准优先使用 excess_vs_market_pct（相对全市场个股收益中位数的超额），它衡量「同期随机买一只股票」的机会成本，与本策略选中小盘题材龙头的口径一致；excess_change_pct 以沪深300为基准，仅作辅助参考，两者结论冲突时以 excess_vs_market_pct 为准。个股收益口径为分析日后首个交易日开盘买入 → 窗口末日收盘，与用户实际可成交价一致。
当日 latest_recommendations 为空时，说明系统按风险闸门主动空仓（不是故障）：此时应回顾当日市场实际表现，判断该次空仓是否正确，并在 directives 中沉淀经验。
hotspot_checks 非空时必须在 hotspot_reviews 中按 sector_code 逐条回验盘前热点预测；previous_review.directives 非空时必须逐条回验上次指令的实际效果，action 逐字引用原文。directives 针对强板块识别、板块龙头、趋势结构和建仓空间提出可执行优化。候选池已在盘前按确定性风险分（>=75 剔除）与追高规则（近5日涨幅>15% 剔除）完成硬过滤，directives 不需要重复要求这些过滤，但可以就阈值是否合适提出证据支撑的调整建议。`
	}
	request := map[string]interface{}{
		"model":           s.config.Model,
		"temperature":     0.1,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt + ` 返回严格JSON：{"market_phase":"up|range|down","confidence":0,"market_summary":"...","index_review":"...","breadth_review":"...","sector_assessments":[{"sector_code":"输入代码","sector_name":"输入名称","strength":"strong|neutral|weak","outlook":"...","risk":"..."}],"hotspot_reviews":[{"sector_code":"输入代码","verdict":"hit|miss|mixed","assessment":"..."}],"recommendation_reviews":[{"recommendation_date":"输入date","symbol":"输入代码","name":"输入名称","verdict":"hit|miss|watching","performance":"...","attribution":"...","next_action":"..."}],"previous_directive_reviews":[{"action":"上次指令原文","verdict":"effective|ineffective|unclear","comment":"..."}],"what_worked":["..."],"what_failed":["..."],"directives":[{"action":"...","rationale":"..."}],"risk_controls":{"position_mode":"aggressive|balanced|defensive","max_position_pct":0,"max_single_stock_pct":0,"stop_loss_pct":0,"avoid_conditions":["..."]}}`},
			{"role": "user", "content": "请严格基于以下本地收盘事实完成复盘：\n" + string(payload)},
		},
	}
	body, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return DailyReviewReport{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	client := &http.Client{Timeout: 8 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return DailyReviewReport{}, fmt.Errorf("AI daily review request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DailyReviewReport{}, fmt.Errorf("AI daily review HTTP %d: %.300s", resp.StatusCode, respBody)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil || len(envelope.Choices) == 0 {
		return DailyReviewReport{}, fmt.Errorf("AI daily review response invalid")
	}
	var report DailyReviewReport
	if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &report); err != nil {
		return report, fmt.Errorf("AI daily review JSON: %w", err)
	}
	return report, nil
}

func validateDailyReview(report *DailyReviewReport, facts store.DailyReviewFacts) error {
	validPhase := map[string]bool{"up": true, "range": true, "down": true}
	if !validPhase[report.MarketPhase] || report.Confidence < 0 || report.Confidence > 100 {
		return fmt.Errorf("AI 复盘市场阶段或置信度无效")
	}
	if strings.TrimSpace(report.MarketSummary) == "" || strings.TrimSpace(report.IndexReview) == "" || strings.TrimSpace(report.BreadthReview) == "" {
		return fmt.Errorf("AI 复盘市场结论不完整")
	}
	allowedSectors := make(map[string]string, len(facts.StrongSectors)+len(facts.WeakSectors))
	for _, item := range append(append([]store.ReviewSectorFact{}, facts.StrongSectors...), facts.WeakSectors...) {
		allowedSectors[item.SectorCode] = item.SectorName
	}
	if len(allowedSectors) > 0 && (len(report.SectorAssessments) < 2 || len(report.SectorAssessments) > len(allowedSectors)) {
		return fmt.Errorf("AI 板块复盘数量无效: got=%d max=%d", len(report.SectorAssessments), len(allowedSectors))
	}
	seenSectors := make(map[string]bool, len(report.SectorAssessments))
	for _, item := range report.SectorAssessments {
		if name, ok := allowedSectors[item.SectorCode]; !ok || name != item.SectorName || seenSectors[item.SectorCode] {
			return fmt.Errorf("AI 复盘返回未知或重复板块: %s", item.SectorCode)
		}
		seenSectors[item.SectorCode] = true
		if item.Strength != "strong" && item.Strength != "neutral" && item.Strength != "weak" {
			return fmt.Errorf("AI 复盘板块强度无效: %s", item.SectorCode)
		}
	}
	// 同一股票可能出现在多个推荐日，唯一键为“推荐日|代码”。
	allowedPicks := make(map[string]string, len(facts.LatestRecommendations))
	for _, item := range facts.LatestRecommendations {
		allowedPicks[item.Date+"|"+item.Symbol] = item.Name
	}
	if len(report.RecommendationReviews) != len(allowedPicks) {
		return fmt.Errorf("AI 推荐复盘数量无效: got=%d want=%d", len(report.RecommendationReviews), len(allowedPicks))
	}
	seen := make(map[string]bool, len(allowedPicks))
	for _, item := range report.RecommendationReviews {
		key := item.RecommendationDate + "|" + item.Symbol
		if name, ok := allowedPicks[key]; !ok || name != item.Name || seen[key] {
			return fmt.Errorf("AI 推荐复盘返回未知或重复股票: %s %s", item.RecommendationDate, item.Symbol)
		}
		seen[key] = true
		if item.Verdict != "hit" && item.Verdict != "miss" && item.Verdict != "watching" {
			return fmt.Errorf("AI 推荐复盘结论无效: %s", item.Symbol)
		}
	}
	allowedHotspots := make(map[string]bool, len(facts.HotspotChecks))
	for _, item := range facts.HotspotChecks {
		allowedHotspots[item.SectorCode] = true
	}
	if len(report.HotspotReviews) != len(allowedHotspots) {
		return fmt.Errorf("AI 热点回验数量无效: got=%d want=%d", len(report.HotspotReviews), len(allowedHotspots))
	}
	seenHotspots := make(map[string]bool, len(allowedHotspots))
	for _, item := range report.HotspotReviews {
		if !allowedHotspots[item.SectorCode] || seenHotspots[item.SectorCode] {
			return fmt.Errorf("AI 热点回验返回未知或重复板块: %s", item.SectorCode)
		}
		seenHotspots[item.SectorCode] = true
		if item.Verdict != "hit" && item.Verdict != "miss" && item.Verdict != "mixed" {
			return fmt.Errorf("AI 热点回验结论无效: %s", item.SectorCode)
		}
		if strings.TrimSpace(item.Assessment) == "" || utf8.RuneCountInString(item.Assessment) > 120 {
			return fmt.Errorf("AI 热点回验说明无效: %s", item.SectorCode)
		}
	}
	// 上次指令逐条回验：action 必须逐字对应，不得增删。
	allowedDirectives := make(map[string]bool, len(facts.PreviousReview.Directives))
	for _, item := range facts.PreviousReview.Directives {
		allowedDirectives[item.Action] = true
	}
	if len(report.PrevDirectiveReviews) != len(allowedDirectives) {
		return fmt.Errorf("AI 指令回验数量无效: got=%d want=%d", len(report.PrevDirectiveReviews), len(allowedDirectives))
	}
	seenDirectives := make(map[string]bool, len(allowedDirectives))
	for _, item := range report.PrevDirectiveReviews {
		if !allowedDirectives[item.Action] || seenDirectives[item.Action] {
			return fmt.Errorf("AI 指令回验返回未知或重复指令")
		}
		seenDirectives[item.Action] = true
		if item.Verdict != "effective" && item.Verdict != "ineffective" && item.Verdict != "unclear" {
			return fmt.Errorf("AI 指令回验结论无效")
		}
		if strings.TrimSpace(item.Comment) == "" || utf8.RuneCountInString(item.Comment) > 120 {
			return fmt.Errorf("AI 指令回验说明无效")
		}
	}
	if len(report.Directives) < 1 || len(report.Directives) > 5 {
		return fmt.Errorf("AI 优化指令数量无效: %d", len(report.Directives))
	}
	seenNewDirectives := make(map[string]bool, len(report.Directives))
	for _, item := range report.Directives {
		action := strings.TrimSpace(item.Action)
		if action == "" || strings.TrimSpace(item.Rationale) == "" || utf8.RuneCountInString(action) > 120 || utf8.RuneCountInString(item.Rationale) > 120 {
			return fmt.Errorf("AI 优化指令内容无效")
		}
		if seenNewDirectives[action] {
			return fmt.Errorf("AI 优化指令重复")
		}
		seenNewDirectives[action] = true
	}
	if report.RiskControls.MaxPositionPct < 0 || report.RiskControls.MaxPositionPct > 100 ||
		report.RiskControls.MaxSingleStockPct < 0 || report.RiskControls.MaxSingleStockPct > 100 ||
		report.RiskControls.StopLossPct < 0 || report.RiskControls.StopLossPct > 30 {
		return fmt.Errorf("AI 风控参数无效")
	}
	validMode := map[string]bool{"aggressive": true, "balanced": true, "defensive": true}
	if !validMode[report.RiskControls.PositionMode] {
		return fmt.Errorf("AI 仓位模式无效")
	}
	return nil
}

// nextReviewRun 计算下一次交易日 17:00（Asia/Shanghai）复盘时间。
func nextReviewRun(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 17, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	for !isRecommendationTradingDay(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
