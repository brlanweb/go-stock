package analysis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func (s *Service) ChatStockStream(ctx context.Context, symbol, question, ctxText string, emit func(string) error) error {
	if !s.Enabled() {
		return fmt.Errorf("AI 推荐未配置")
	}
	reqBody := map[string]interface{}{
		"model":       s.config.Model,
		"temperature": 0.2,
		"max_tokens":  600,
		"stream":      true,
		"messages": []map[string]string{
			{"role": "system", "content": "你是 go-stock 的 AI 行情助理，仅基于用户提供的本地数据库字段回答。回答用简洁中文，不要编造数据。"},
			{"role": "user", "content": "已携带本地数据库内容如下：\n" + ctxText + "\n\n用户问题：\n" + question},
		},
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
	if !s.Enabled() {
		return fmt.Errorf("AI 推荐未配置")
	}
	if !s.running.CompareAndSwap(false, true) {
		return fmt.Errorf("AI 推荐任务正在执行")
	}
	defer s.running.Store(false)
	candidates, err := s.st.RecommendationCandidates(ctx)
	if err != nil {
		return err
	}
	if len(candidates) < 3 {
		return fmt.Errorf("可分析候选股不足: %d", len(candidates))
	}
	payload, _ := json.Marshal(candidates)
	prompt := s.config.Prompt
	if prompt == "" {
		prompt = "基于候选股最近60个交易日OHLCV，评估未来10个交易日维持上涨趋势的概率。必须只从候选中选3只，避免保证收益。"
	}
	request := map[string]interface{}{
		"model":           s.config.Model,
		"temperature":     0.2,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": prompt + ` 返回严格JSON：{"recommendations":[{"symbol":"SH600000","probability":72.5,"reason":"不超过80字","sector":"银行"}]}`},
			{"role": "user", "content": string(payload)},
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
	resp, err := s.client.Do(req)
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
	if len(result.Recommendations) > 3 {
		result.Recommendations = result.Recommendations[:3]
	}
	if len(result.Recommendations) != 3 {
		return fmt.Errorf("AI recommendation count=%d", len(result.Recommendations))
	}
	allowed := make(map[string]store.RecommendationCandidate, len(candidates))
	for _, candidate := range candidates {
		allowed[candidate.Symbol] = candidate
	}
	for i := range result.Recommendations {
		item := &result.Recommendations[i]
		candidate, ok := allowed[item.Symbol]
		if !ok {
			return fmt.Errorf("AI returned unknown symbol: %s", item.Symbol)
		}
		if item.Probability < 0 {
			item.Probability = 0
		}
		if item.Probability > 100 {
			item.Probability = 100
		}
		item.Rank, item.Code, item.Name = i+1, candidate.Code, candidate.Name
		if item.Sector == "" {
			item.Sector = candidate.Industry
		}
	}
	return s.st.ReplaceRecommendations(ctx, time.Now().In(shanghai()).Format("2006-01-02"), s.config.Model, result.Recommendations)
}

func (s *Service) StartScheduler(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	go func() {
		for {
			now := time.Now().In(shanghai())
			next := time.Date(now.Year(), now.Month(), now.Day(), 5, 0, 0, 0, now.Location())
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			select {
			case <-time.After(time.Until(next)):
				runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				_ = s.RunDaily(runCtx)
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
