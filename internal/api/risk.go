package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/hoax/go-stock/internal/store"
)

// 风险感知板块 REST 端点。
//
// GET  /api/v1/risk/gate          当日风险总览：境内风向门 + 全球风险门 + 融合档位
// GET  /api/v1/risk/gate/history  全球风险门判定历史（预警命中率复盘用）
// POST /api/v1/risk/gate/run      手动触发一轮外盘采集与判定（同步执行，秒级）

// handleRiskGate 返回融合后的风险总览。境内门实时计算（分析基准 = 最近收盘
// 交易日），全球门读取当日落库结论；任一侧缺失不阻断另一侧展示。
func (s *Server) handleRiskGate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()

	var marketGate *store.MarketGate
	analysisDate, err := s.St.LatestKlineDate(ctx)
	if err == nil && analysisDate != "" {
		if gate, gateErr := s.St.MarketDirectionGate(ctx, analysisDate); gateErr == nil {
			marketGate = &gate
		}
	}
	globalGate, err := s.St.LatestGlobalRiskGate(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取全球风险门失败: "+err.Error())
		return
	}

	// 融合档位：两门取最严；任一缺失按黄灯保守参与融合（缺数据不等于安全）。
	marketLevel, globalLevel := store.MarketGateYellow, store.MarketGateYellow
	if marketGate != nil {
		marketLevel = marketGate.Level
	}
	if globalGate != nil {
		globalLevel = globalGate.Level
	}
	// auto_entry_enabled 让前端能区分「风险门放行」与「系统真的会建仓」：
	// 开关关闭时即使绿灯也只输出观察性推荐，必须显式告知，避免照单手动追买。
	autoEntry := false
	if s.Analysis != nil {
		autoEntry = s.Analysis.AutoEntryEnabled()
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"analysis_date":      analysisDate,
		"market_gate":        marketGate,
		"global_gate":        globalGate,
		"final_level":        store.StricterGateLevel(marketLevel, globalLevel),
		"auto_entry_enabled": autoEntry,
	})
}

// handleRiskGateHistory 全球风险门判定历史，按日期倒序。
func (s *Server) handleRiskGateHistory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	limit := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	history, err := s.St.GlobalRiskGateHistory(ctx, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": history})
}

// handleRiskGateRun 手动触发一轮风险感知（外盘采集 + 判定 + 落库）。
// 外盘接口为公开行情，单轮 2-3 个请求，同步执行即可。
func (s *Server) handleRiskGateRun(w http.ResponseWriter, r *http.Request) {
	if s.Analysis == nil {
		writeErr(w, http.StatusServiceUnavailable, "分析服务未初始化")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	today := time.Now().In(shanghaiLoc()).Format("2006-01-02")
	gate := s.Analysis.RunGlobalGate(ctx, today)
	writeJSON(w, http.StatusOK, gate)
}
