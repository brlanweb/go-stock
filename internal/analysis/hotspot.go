package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/store"
)

type HotspotAIRelation struct {
	FromCode string `json:"from_code"`
	ToCode   string `json:"to_code"`
	Type     string `json:"type"`
	Reason   string `json:"reason"`
}

type HotspotAIChokepoint struct {
	SectorCode   string  `json:"sector_code"`
	Status       string  `json:"status"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
	Invalidation string  `json:"invalidation"`
}

type HotspotAIMainline struct {
	Name         string                `json:"name"`
	Thesis       string                `json:"thesis"`
	ConceptCodes []string              `json:"concept_codes"`
	Relations    []HotspotAIRelation   `json:"relations"`
	Chokepoints  []HotspotAIChokepoint `json:"chokepoints"`
}

type hotspotAIResult struct {
	Mainlines []HotspotAIMainline `json:"mainlines"`
}

type HotspotConceptResult struct {
	SectorCode   string                  `json:"sector_code"`
	SectorName   string                  `json:"sector_name"`
	Status       string                  `json:"status"`
	Confidence   float64                 `json:"confidence"`
	Reason       string                  `json:"reason"`
	Invalidation string                  `json:"invalidation"`
	Stats        store.HotspotSectorStat `json:"stats"`
	Stocks       []store.HotspotStock    `json:"stocks"`
}

type HotspotFinalReport struct {
	ReportDate  string                    `json:"report_date"`
	Model       string                    `json:"model"`
	GeneratedAt time.Time                 `json:"generated_at"`
	Screened    []store.HotspotSectorStat `json:"screened"`
	Relations   []store.HotspotRelation   `json:"data_relations"`
	Mainlines   []HotspotAIMainline       `json:"mainlines"`
	Concepts    []HotspotConceptResult    `json:"concepts"`
}

func (s *Service) HotspotRunning() bool { return s.hotspotRunning.Load() }

// RunHotspot 执行“数据筛选 -> 关系候选 -> AI 产业链分析 -> 数据回验”的完整漏斗。
func (s *Service) RunHotspot(ctx context.Context) error {
	if !s.Enabled() {
		return fmt.Errorf("AI 热点分析未配置")
	}
	if !s.hotspotRunning.CompareAndSwap(false, true) {
		return fmt.Errorf("热点漏斗任务正在执行")
	}
	defer s.hotspotRunning.Store(false)

	tradeDate, err := s.st.LatestKlineDate(ctx)
	if err != nil || tradeDate == "" {
		return fmt.Errorf("热点分析缺少日K数据: %w", err)
	}
	if err := s.st.RecomputeHotspotStats(ctx, tradeDate); err != nil {
		return err
	}
	if err := s.st.RebuildSectorOverlaps(ctx); err != nil {
		return err
	}
	screened, err := s.st.HotspotCandidates(ctx, 30)
	if err != nil {
		return err
	}
	if len(screened) < 5 {
		return fmt.Errorf("热点候选概念不足: %d", len(screened))
	}
	if err := s.st.SaveHotspotReport(ctx, tradeDate, "l1_screen", "", screened); err != nil {
		return err
	}
	codes := make([]string, 0, len(screened))
	for _, item := range screened {
		codes = append(codes, item.SectorCode)
	}
	relations, err := s.st.HotspotRelations(ctx, codes, 0.08)
	if err != nil {
		return err
	}
	if err := s.st.SaveHotspotReport(ctx, tradeDate, "l2_relation", "", relations); err != nil {
		return err
	}

	universeCodes := append([]string{}, codes...)
	seen := make(map[string]bool, len(codes)+len(relations)*2)
	for _, code := range codes {
		seen[code] = true
	}
	for _, relation := range relations {
		for _, code := range []string{relation.FromCode, relation.ToCode} {
			if !seen[code] {
				seen[code] = true
				universeCodes = append(universeCodes, code)
			}
		}
	}
	stats, err := s.st.HotspotStatsByCodes(ctx, universeCodes)
	if err != nil {
		return err
	}
	// 邻接概念没有足够行情统计时不交给模型，避免无法回验的推断进入结果。
	allowed := make(map[string]store.HotspotSectorStat, len(stats))
	for code, stat := range stats {
		if stat.StockCount >= 5 && stat.StockCount <= 150 {
			allowed[code] = stat
		}
	}
	aiInput := map[string]interface{}{
		"trade_date":                    tradeDate,
		"concepts":                      allowed,
		"constituent_overlap_relations": relations,
	}
	result, err := s.analyzeHotspotWithAI(ctx, aiInput)
	if err != nil {
		return err
	}
	if err := validateHotspotAIResult(&result, allowed); err != nil {
		return err
	}
	if err := s.st.SaveHotspotReport(ctx, tradeDate, "l3_ai", s.config.Model, result); err != nil {
		return err
	}

	final := HotspotFinalReport{
		ReportDate:  tradeDate,
		Model:       s.config.Model,
		GeneratedAt: time.Now().In(shanghai()),
		Screened:    screened,
		Relations:   relations,
		Mainlines:   result.Mainlines,
		Concepts:    []HotspotConceptResult{},
	}
	conceptSeen := map[string]bool{}
	for _, mainline := range result.Mainlines {
		for _, pick := range mainline.Chokepoints {
			if conceptSeen[pick.SectorCode] {
				continue
			}
			stat := allowed[pick.SectorCode]
			stocks, err := s.st.HotspotStocks(ctx, pick.SectorCode, 10)
			if err != nil {
				return err
			}
			if len(stocks) == 0 {
				continue
			}
			conceptSeen[pick.SectorCode] = true
			final.Concepts = append(final.Concepts, HotspotConceptResult{
				SectorCode: pick.SectorCode, SectorName: stat.SectorName,
				Status: pick.Status, Confidence: pick.Confidence, Reason: pick.Reason,
				Invalidation: pick.Invalidation, Stats: stat, Stocks: stocks,
			})
		}
	}
	if len(final.Concepts) == 0 {
		return fmt.Errorf("AI 结果经本地数据回验后无有效概念")
	}
	sort.SliceStable(final.Concepts, func(i, j int) bool {
		return final.Concepts[i].Confidence > final.Concepts[j].Confidence
	})
	return s.st.SaveHotspotReport(ctx, tradeDate, "final", s.config.Model, final)
}

func (s *Service) analyzeHotspotWithAI(ctx context.Context, input interface{}) (hotspotAIResult, error) {
	payload, _ := json.Marshal(input)
	systemPrompt := strings.TrimSpace(s.config.HotspotPrompt)
	if systemPrompt == "" {
		systemPrompt = `你是A股产业链热点分析器。输入概念和统计全部来自本地数据库。你的任务不是按涨幅复述排名，而是识别正在扩散的主线，并从上游材料、核心器件、基础设施、下游应用中找出具有技术/资源/产能不可替代性，或需求增速/渗透率/国产替代高成长性，且尚未完全定价的卡点概念。
硬约束：所有 concept_codes、sector_code、关系端点必须逐字使用输入 concepts 的代码；不得虚构概念、股票、数值。可以使用已有产业知识判断链条、壁垒和成长来源，但实时市场强弱与量价结论只能来自输入。constituent_overlap 只表示共现，不等于产业因果；relations 必须给出明确的产业链关系。卡点 reason 必须说明不可替代性或高成长性的依据，仅有短期涨幅不得入选。只保留2至4条主线，每条1至3个有效卡点。status 只能是 accelerating、latent、overheated；confidence 为0到100；reason和invalidation各不超过100字。`
	}
	request := map[string]interface{}{
		"model":           s.config.Model,
		"temperature":     0.15,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt + ` 返回严格JSON：{"mainlines":[{"name":"主线名","thesis":"主线逻辑","concept_codes":["BK0001"],"relations":[{"from_code":"BK0001","to_code":"BK0002","type":"上游材料/核心器件/基础设施/下游应用/共振","reason":"关系依据"}],"chokepoints":[{"sector_code":"BK0002","status":"latent","confidence":80,"reason":"入选原因","invalidation":"证伪条件"}]}]}`},
			{"role": "user", "content": "请分析以下唯一允许使用的本地候选集：\n" + string(payload)},
		},
	}
	body, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.config.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return hotspotAIResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	// 热点产业链分析输入大、推理时间长，使用独立长超时客户端，
	// 不复用 90s 的通用客户端；整体截止时间仍受调用方 ctx 约束。
	hotspotClient := &http.Client{Timeout: 8 * time.Minute}
	resp, err := hotspotClient.Do(req)
	if err != nil {
		return hotspotAIResult{}, fmt.Errorf("AI hotspot request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return hotspotAIResult{}, fmt.Errorf("AI hotspot HTTP %d: %.300s", resp.StatusCode, respBody)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil || len(envelope.Choices) == 0 {
		return hotspotAIResult{}, fmt.Errorf("AI hotspot response invalid")
	}
	var result hotspotAIResult
	if err := json.Unmarshal([]byte(envelope.Choices[0].Message.Content), &result); err != nil {
		return result, fmt.Errorf("AI hotspot JSON: %w", err)
	}
	return result, nil
}

func validateHotspotAIResult(result *hotspotAIResult, allowed map[string]store.HotspotSectorStat) error {
	if len(result.Mainlines) < 1 || len(result.Mainlines) > 4 {
		return fmt.Errorf("AI 主线数量无效: %d", len(result.Mainlines))
	}
	validStatus := map[string]bool{"accelerating": true, "latent": true, "overheated": true}
	validRelationType := map[string]bool{"上游材料": true, "核心器件": true, "基础设施": true, "下游应用": true, "共振": true}
	for i := range result.Mainlines {
		mainline := &result.Mainlines[i]
		mainline.Name, mainline.Thesis = strings.TrimSpace(mainline.Name), strings.TrimSpace(mainline.Thesis)
		if mainline.Name == "" || len(mainline.Chokepoints) == 0 || len(mainline.Chokepoints) > 3 {
			return fmt.Errorf("AI 主线内容无效: %q", mainline.Name)
		}
		mainlineCodes := make(map[string]bool, len(mainline.ConceptCodes))
		for _, code := range mainline.ConceptCodes {
			if _, ok := allowed[code]; !ok {
				return fmt.Errorf("AI 返回未知概念: %s", code)
			}
			mainlineCodes[code] = true
		}
		for _, relation := range mainline.Relations {
			if _, ok := allowed[relation.FromCode]; !ok {
				return fmt.Errorf("AI 返回未知关系端点: %s", relation.FromCode)
			}
			if _, ok := allowed[relation.ToCode]; !ok {
				return fmt.Errorf("AI 返回未知关系端点: %s", relation.ToCode)
			}
			if !mainlineCodes[relation.FromCode] || !mainlineCodes[relation.ToCode] || relation.FromCode == relation.ToCode {
				return fmt.Errorf("AI 关系未落在当前主线: %s -> %s", relation.FromCode, relation.ToCode)
			}
			if !validRelationType[relation.Type] || strings.TrimSpace(relation.Reason) == "" {
				return fmt.Errorf("AI 关系类型或依据无效: %s -> %s", relation.FromCode, relation.ToCode)
			}
		}
		for j := range mainline.Chokepoints {
			pick := &mainline.Chokepoints[j]
			if _, ok := allowed[pick.SectorCode]; !ok {
				return fmt.Errorf("AI 返回未知卡点概念: %s", pick.SectorCode)
			}
			if !mainlineCodes[pick.SectorCode] {
				return fmt.Errorf("AI 卡点不属于当前主线: %s", pick.SectorCode)
			}
			if !validStatus[pick.Status] || pick.Confidence < 0 || pick.Confidence > 100 || strings.TrimSpace(pick.Reason) == "" || strings.TrimSpace(pick.Invalidation) == "" {
				return fmt.Errorf("AI 卡点概念字段无效: %s", pick.SectorCode)
			}
		}
	}
	return nil
}
