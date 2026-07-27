package analysis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/store"
)

type Config struct {
	BaseURL string
	APIKey  string
	Model   string
	Prompt  string
}

type Service struct {
	st      *store.Store
	config  Config
	client  *http.Client
	running atomic.Bool
}

func New(st *store.Store, config Config) *Service {
	return &Service{st: st, config: config, client: &http.Client{Timeout: 90 * time.Second}}
}

func (s *Service) Enabled() bool {
	return s.config.BaseURL != "" && s.config.APIKey != "" && s.config.Model != ""
}

func (s *Service) Running() bool { return s.running.Load() }

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
	defer s.running.Store(false)

	analysisDate, err := s.st.LatestKlineDate(ctx)
	if err != nil {
		return err
	}
	if analysisDate == "" || analysisDate != now.Format("2006-01-02") {
		return fmt.Errorf("当日收盘日 K 尚未就绪: latest=%s", analysisDate)
	}
	candidates, err := s.st.RecommendationCandidates(ctx)
	if err != nil {
		return err
	}
	if len(candidates) != 10 {
		return fmt.Errorf("可分析候选股必须为 10 只: %d", len(candidates))
	}
	items := rankRecommendations(candidates)
	return s.st.ReplaceRecommendations(ctx, analysisDate, "deterministic-ohlcv-v1", items)
}

func rankRecommendations(candidates []store.RecommendationCandidate) []model.StockRecommendation {
	type scored struct {
		candidate store.RecommendationCandidate
		score     float64
		reason    string
	}
	scores := make([]scored, 0, len(candidates))
	for _, candidate := range candidates {
		klines := candidate.Klines
		closes := make([]float64, len(klines))
		positive := 0
		returns := make([]float64, 0, len(klines)-1)
		for i, kline := range klines {
			closes[i] = kline.Close
			if kline.Close > kline.Open {
				positive++
			}
			if i > 0 && closes[i-1] > 0 {
				returns = append(returns, closes[i]/closes[i-1]-1)
			}
		}
		ma20 := average(closes[len(closes)-20:])
		ma60 := average(closes)
		trend := percent(closes[len(closes)-1], ma20)*0.6 + percent(ma20, ma60)*0.4
		momentum10 := percent(closes[len(closes)-1], closes[len(closes)-11])
		momentum20 := percent(closes[len(closes)-1], closes[len(closes)-21])
		momentum := momentum10*0.6 + momentum20*0.4
		positiveRate := float64(positive) / float64(len(klines)) * 100
		volatility := standardDeviation(returns) * 100
		score := trend*0.35 + momentum*0.35 + (positiveRate-50)*0.20 - volatility*0.10
		scores = append(scores, scored{
			candidate: candidate,
			score:     score,
			reason:    fmt.Sprintf("20日趋势%.1f%%，10/20日动量%.1f%%/%.1f%%，阳线率%.0f%%", trend, momentum10, momentum20, positiveRate),
		})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].score == scores[j].score {
			return scores[i].candidate.Symbol < scores[j].candidate.Symbol
		}
		return scores[i].score > scores[j].score
	})
	out := make([]model.StockRecommendation, 0, 3)
	for i := 0; i < 3 && i < len(scores); i++ {
		probability := math.Max(0, math.Min(100, 50+scores[i].score))
		candidate := scores[i].candidate
		out = append(out, model.StockRecommendation{
			Rank: i + 1, Symbol: candidate.Symbol, Code: candidate.Code,
			Name: candidate.Name, Sector: candidate.Industry,
			Probability: math.Round(probability*100) / 100,
			Reason:      scores[i].reason,
		})
	}
	return out
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func percent(current, base float64) float64 {
	if base == 0 {
		return 0
	}
	return (current/base - 1) * 100
}

func standardDeviation(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	mean := average(values)
	var variance float64
	for _, value := range values {
		diff := value - mean
		variance += diff * diff
	}
	return math.Sqrt(variance / float64(len(values)))
}

func isRecommendationTradingDay(now time.Time) bool {
	return now.Weekday() != time.Saturday && now.Weekday() != time.Sunday
}

func nextRecommendationRun(now time.Time) time.Time {
	loc := now.Location()
	next := time.Date(now.Year(), now.Month(), now.Day(), 16, 30, 0, 0, loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	for !isRecommendationTradingDay(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

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
				_ = s.runDailyAt(runCtx, next)
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
