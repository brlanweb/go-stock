// Package api REST 端点（标准库 net/http 1.22+ method 路由，零额外依赖）。
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/analysis"
	"github.com/hoax/go-stock/internal/backtest"
	"github.com/hoax/go-stock/internal/indicator"
	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
	"github.com/hoax/go-stock/internal/querycache"
	"github.com/hoax/go-stock/internal/store"
	gsync "github.com/hoax/go-stock/internal/sync"
)

// Server API 依赖集合。
type Server struct {
	St       *store.Store
	Svc      *provider.Service
	Engine   *gsync.Engine
	Analysis *analysis.Service
	Cache    *querycache.Cache
}

// Register 注册全部 REST 路由。
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/quote/{code}", s.handleQuote)
	mux.HandleFunc("GET /api/v1/quotes", s.handleQuotes)
	mux.HandleFunc("GET /api/v1/kline/{code}", s.handleKline)
	mux.HandleFunc("GET /api/v1/timeshare/{code}", s.handleTimeshare)
	mux.HandleFunc("GET /api/v1/search", s.handleSearch)
	mux.HandleFunc("GET /api/v1/indices", s.handleIndices)
	mux.HandleFunc("GET /api/v1/security/{code}", s.handleSecurity)
	mux.HandleFunc("GET /api/v1/indicator/{code}", s.handleIndicator)
	mux.HandleFunc("GET /api/v1/indicators", s.handleIndicators)
	mux.HandleFunc("GET /api/v1/indicators/{id}", s.handleIndicatorDefinition)
	mux.HandleFunc("PUT /api/v1/indicators/{id}", s.handleIndicatorUpdate)
	mux.HandleFunc("POST /api/v1/indicators/{id}/reset", s.handleIndicatorReset)
	mux.HandleFunc("POST /api/v1/backtest", s.handleBacktest)
	mux.HandleFunc("GET /api/v1/backtest/history/{code}", s.handleBacktestHistory)
	mux.HandleFunc("GET /api/v1/market/heatmap", s.handleMarketHeatmap)
	mux.HandleFunc("GET /api/v1/sectors", s.handleSectors)
	mux.HandleFunc("GET /api/v1/sectors/{code}/constituents", s.handleSectorConstituents)
	mux.HandleFunc("GET /api/v1/stock/{code}/detail", s.handleStockDetail)
	mux.HandleFunc("POST /api/v1/agent/chat", s.handleAgentChat)
	mux.HandleFunc("POST /api/v1/agent/chat/stream", s.handleAgentChatStream)
	mux.HandleFunc("GET /api/v1/agent/chat/history/{code}", s.handleAgentChatHistory)
	mux.HandleFunc("DELETE /api/v1/agent/chat/history/{code}", s.handleAgentChatClear)
	mux.HandleFunc("GET /api/v1/recommendations", s.handleRecommendations)
	mux.HandleFunc("GET /api/v1/recommendations/history", s.handleRecommendationHistory)
	mux.HandleFunc("POST /api/v1/recommendations/run", s.handleRecommendationsRun)

	mux.HandleFunc("GET /api/v1/watchlist", s.handleWatchlistGet)
	mux.HandleFunc("POST /api/v1/watchlist/{code}", s.handleWatchlistAdd)
	mux.HandleFunc("DELETE /api/v1/watchlist/{code}", s.handleWatchlistDel)

	mux.HandleFunc("GET /api/v1/sync/status", s.handleSyncStatus)
	mux.HandleFunc("POST /api/v1/sync/backfill", s.handleSyncBackfill)
	mux.HandleFunc("POST /api/v1/sync/backfill/retry-failed", s.handleSyncBackfillRetryFailed)
	mux.HandleFunc("POST /api/v1/sync/backfill/stop", s.handleSyncBackfillStop)
	mux.HandleFunc("POST /api/v1/sync/daily", s.handleSyncDaily)
	mux.HandleFunc("POST /api/v1/sync/stock/{code}", s.handleSyncStock)
}

// ---- 工具函数 ----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func reqCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 30*time.Second)
}

func (s *Server) cachedJSON(w http.ResponseWriter, r *http.Request, key string, load func(context.Context) (interface{}, error)) {
	if data, ok := s.Cache.Get(r.Context(), key); ok {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Cache", "HIT")
		_, _ = w.Write(data)
		return
	}
	value, err := load(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.Cache.Set(r.Context(), key, data)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Cache", "MISS")
	_, _ = w.Write(data)
}

// ---- Handlers ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

func (s *Server) handleQuote(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "无法识别的代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	quote, err := s.Svc.Quote(ctx, symbol)
	if err != nil {
		quote, err = s.St.LatestQuote(ctx, symbol)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "实时行情和本地快照均不可用")
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

func (s *Server) handleQuotes(w http.ResponseWriter, r *http.Request) {
	codes := strings.Split(r.URL.Query().Get("codes"), ",")
	if len(codes) == 0 || codes[0] == "" {
		writeErr(w, http.StatusBadRequest, "缺少 codes 参数（逗号分隔）")
		return
	}
	if len(codes) > 100 {
		writeErr(w, http.StatusBadRequest, "单次最多 100 只")
		return
	}
	symbols := make([]string, 0, len(codes))
	for _, code := range codes {
		if symbol := model.NormalizeSymbol(code); symbol != "" {
			symbols = append(symbols, symbol)
		}
	}
	if len(symbols) == 0 {
		writeErr(w, http.StatusBadRequest, "没有有效代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	quotes, err := s.Svc.BatchQuotes(ctx, symbols)
	if err != nil {
		quotes, err = s.St.LatestQuotes(ctx, symbols)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "实时行情和本地快照均不可用")
		return
	}
	writeJSON(w, http.StatusOK, quotes)
}

func (s *Server) handleKline(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, 400, "无法识别的代码")
		return
	}
	q := r.URL.Query()
	period := q.Get("period")
	if period == "" {
		period = "day"
	}
	adjust := q.Get("adjust")
	if adjust == "" {
		adjust = "qfq"
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	ctx, cancel := reqCtx(r)
	defer cancel()
	klines, err := s.St.QueryKlines(ctx, symbol, period, adjust, q.Get("start"), q.Get("end"), limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, klines)
}

func (s *Server) handleTimeshare(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "无法识别的代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	points, err := s.Svc.Timeshare(ctx, symbol)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "实时分时行情获取失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	keyword := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	results, err := s.St.SearchSecurities(ctx, keyword, limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, results)
}

func (s *Server) handleIndices(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	indices, err := s.St.LatestIndices(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(indices) == 0 {
		writeErr(w, http.StatusNotFound, "本地尚无指数快照；请等待下一次定时采集")
		return
	}
	writeJSON(w, http.StatusOK, indices)
}

func (s *Server) handleSecurity(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, 400, "无法识别的代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	sec, err := s.St.GetSecurity(ctx, symbol)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if sec == nil {
		writeErr(w, 404, "未找到该证券")
		return
	}
	writeJSON(w, 200, sec)
}

func (s *Server) handleIndicator(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, 400, "无法识别的代码")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	ctx, cancel := reqCtx(r)
	defer cancel()
	list, err := s.St.QueryDailyIndicators(ctx, symbol, q.Get("start"), q.Get("end"), limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) handleIndicators(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	items, err := s.St.ListIndicators(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleIndicatorDefinition(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	item, err := s.St.GetIndicator(ctx, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeErr(w, http.StatusNotFound, "指标不存在")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleIndicatorUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool           `json:"enabled"`
		Params  map[string]any `json:"params"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	if err := s.St.UpdateIndicator(ctx, r.PathValue("id"), body.Enabled, body.Params); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	item, _ := s.St.GetIndicator(ctx, r.PathValue("id"))
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleIndicatorReset(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	if err := s.St.ResetIndicator(ctx, r.PathValue("id")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	item, _ := s.St.GetIndicator(ctx, r.PathValue("id"))
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleBacktest(w http.ResponseWriter, r *http.Request) {
	var req backtest.Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	req.Symbol = model.NormalizeSymbol(req.Symbol)
	if req.Symbol == "" || req.IndicatorID == "" {
		writeErr(w, http.StatusBadRequest, "缺少有效 symbol 或 indicator_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	definition, err := s.St.GetIndicator(ctx, req.IndicatorID)
	if err != nil || definition == nil {
		writeErr(w, http.StatusNotFound, "指标不存在")
		return
	}
	if !definition.Enabled {
		writeErr(w, http.StatusConflict, "指标已停用")
		return
	}
	if definition.Kind != "strategy" || definition.Capability != indicator.Executable {
		writeErr(w, http.StatusUnprocessableEntity, "该条目不支持纯K线确定性回测")
		return
	}
	if len(req.Params) == 0 {
		req.Params = definition.CurrentParams
	}
	klines, err := s.St.QueryKlines(ctx, req.Symbol, req.Period, "qfq", req.Start, req.End, 1500)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	result, err := backtest.Run(req, klines)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.St.SaveBacktest(ctx, result); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBacktestHistory(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "无法识别的代码")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ctx, cancel := reqCtx(r)
	defer cancel()
	items, err := s.St.BacktestHistory(ctx, symbol, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleMarketHeatmap(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	market, groupBy, metric, period := q.Get("market"), q.Get("group_by"), q.Get("metric"), q.Get("period")
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	key := "heatmap:v13:" + market + ":" + groupBy + ":" + metric + ":" + period + ":" + strconv.Itoa(limit)
	s.cachedJSON(w, r, key, func(ctx context.Context) (interface{}, error) {
		groups, notice, err := s.St.MarketHeatmap(ctx, market, groupBy, metric, period, limit)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"market": market, "group_by": groupBy, "metric": metric,
			"period": period, "limit": limit, "notice": notice, "groups": groups,
		}, nil
	})
}

func (s *Server) handleSectors(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("group_by")
	if groupBy != "concept" {
		groupBy = "industry"
	}
	s.cachedJSON(w, r, "sectors:v1:"+groupBy, func(ctx context.Context) (interface{}, error) {
		list, err := s.St.ListSectors(ctx, groupBy)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"sector_type": groupBy, "sectors": list}, nil
	})
}

func (s *Server) handleSectorConstituents(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	key := "sector-constituents:v1:" + code + ":" + strconv.Itoa(limit)
	s.cachedJSON(w, r, key, func(ctx context.Context) (interface{}, error) {
		items, err := s.St.ListSectorConstituents(ctx, code, limit)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"sector_code": code, "constituents": items}, nil
	})
}

func (s *Server) handleStockDetail(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, 400, "无法识别的代码")
		return
	}
	s.cachedJSON(w, r, "stock-detail:v1:"+symbol, func(ctx context.Context) (interface{}, error) {
		return s.St.DetailForSymbol(ctx, symbol)
	})
}

func (s *Server) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol   string `json:"symbol"`
		Question string `json:"question"`
		Context  string `json:"context"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	if s.Analysis == nil || !s.Analysis.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "AI 推荐未配置（GOSTOCK_AI_BASE_URL/API_KEY/MODEL）")
		return
	}
	if body.Question == "" {
		writeErr(w, http.StatusBadRequest, "缺少 question")
		return
	}
	reply, err := s.Analysis.ChatStock(r.Context(), body.Symbol, body.Question, body.Context)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

func validAgentHistoryDays(days int) bool {
	return days == 0 || days == 10 || days == 30 || days == 60
}

func (s *Server) handleAgentChatStream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol       string `json:"symbol"`
		Question     string `json:"question"`
		IncludeStock *bool  `json:"include_stock"`
		HistoryDays  int    `json:"history_days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败")
		return
	}
	if s.Analysis == nil || !s.Analysis.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "AI 推荐未配置（GOSTOCK_AI_BASE_URL/API_KEY/MODEL）")
		return
	}
	if strings.TrimSpace(body.Question) == "" {
		writeErr(w, http.StatusBadRequest, "缺少 question")
		return
	}
	symbol := model.NormalizeSymbol(body.Symbol)
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "无法识别的代码")
		return
	}
	if !validAgentHistoryDays(body.HistoryDays) {
		writeErr(w, http.StatusBadRequest, "history_days 仅支持 0、10、30 或 60")
		return
	}
	includeStock := body.IncludeStock == nil || *body.IncludeStock
	ctxText, err := s.agentContext(r.Context(), symbol, includeStock, body.HistoryDays)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	history, err := s.St.AgentChatMessages(r.Context(), symbol)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.St.AppendAgentChatMessage(r.Context(), symbol, "user", body.Question); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "当前服务不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	emit := func(event string, value interface{}) error {
		payload, _ := json.Marshal(value)
		if _, err := w.Write([]byte("event: " + event + "\ndata: " + string(payload) + "\n\n")); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	var assistantReply strings.Builder
	if err := s.Analysis.ChatStockStream(r.Context(), symbol, body.Question, ctxText, history, func(delta string) error {
		assistantReply.WriteString(delta)
		return emit("delta", map[string]string{"text": delta})
	}); err != nil {
		_ = emit("error", map[string]string{"error": err.Error()})
		return
	}
	if err := s.St.AppendAgentChatMessage(context.Background(), symbol, "assistant", assistantReply.String()); err != nil {
		slog.Error("保存 Agent 回复失败", "symbol", symbol, "err", err)
	}
	_ = emit("done", map[string]bool{"done": true})
}

func (s *Server) agentContext(ctx context.Context, symbol string, includeStock bool, historyDays int) (string, error) {
	detail, err := s.St.DetailForSymbol(ctx, symbol)
	if err != nil {
		return "", err
	}
	payload := make(map[string]interface{}, 2)
	if includeStock {
		payload["stock"] = map[string]interface{}{
			"symbol": detail.Symbol, "code": detail.Code, "name": detail.Name,
			"industry": detail.Industry, "concepts": detail.Concepts,
			"list_date": detail.ListDate, "latest_snapshot": detail.Quote,
		}
	}
	if historyDays > 0 {
		klines := detail.Klines60
		if len(klines) > historyDays {
			klines = klines[len(klines)-historyDays:]
		}
		payload["daily_klines_qfq"] = klines
	}
	if len(payload) == 0 {
		return "用户未选择携带本地数据上下文。", nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Server) handleAgentChatHistory(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "无法识别的代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	messages, err := s.St.AgentChatMessages(ctx, symbol)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleAgentChatClear(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, http.StatusBadRequest, "无法识别的代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	if err := s.St.ClearAgentChatMessages(ctx, symbol); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"cleared": symbol})
}

func (s *Server) handleRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	items, err := s.St.RecommendationsByDate(ctx, r.URL.Query().Get("date"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) handleRecommendationHistory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	dates, err := s.St.RecommendationHistory(ctx, limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, dates)
}

func (s *Server) handleRecommendationsRun(w http.ResponseWriter, r *http.Request) {
	if s.Analysis == nil || !s.Analysis.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "AI 推荐未配置")
		return
	}
	if s.Analysis.Running() {
		writeErr(w, http.StatusConflict, "AI 推荐任务正在执行")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.Analysis.RunDaily(ctx); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "analysis completed"})
}

func (s *Server) handleWatchlistGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	symbols, err := s.St.WatchlistSymbols(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(symbols) == 0 {
		writeJSON(w, 200, []interface{}{})
		return
	}
	quotes, err := s.St.LatestQuotes(ctx, symbols)
	if err != nil {
		slog.Warn("自选股本地快照查询失败", "err", err)
		writeJSON(w, 200, symbols)
		return
	}
	writeJSON(w, 200, quotes)
}

func (s *Server) handleWatchlistAdd(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, 400, "无法识别的代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	if err := s.St.AddWatchlist(ctx, symbol); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"added": symbol})
}

func (s *Server) handleWatchlistDel(w http.ResponseWriter, r *http.Request) {
	symbol := model.NormalizeSymbol(r.PathValue("code"))
	if symbol == "" {
		writeErr(w, 400, "无法识别的代码")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	if err := s.St.RemoveWatchlist(ctx, symbol); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"removed": symbol})
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	backfill, err := s.St.SyncStatus(ctx, gsync.TaskBackfill)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"backfill":         backfill,
		"backfill_running": s.Engine.IsRunning(),
	})
}

func (s *Server) handleSyncBackfill(w http.ResponseWriter, r *http.Request) {
	if s.Engine.IsRunning() {
		writeErr(w, http.StatusConflict, "已有同步任务运行中")
		return
	}
	if err := s.Engine.StartBackfill(s.Engine.BaseContext()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, 202, map[string]string{"status": "backfill started"})
}

func (s *Server) handleSyncBackfillRetryFailed(w http.ResponseWriter, r *http.Request) {
	if s.Engine.IsRunning() {
		writeErr(w, http.StatusConflict, "回填任务正在运行，请停止后再重试失败项")
		return
	}
	ctx, cancel := reqCtx(r)
	defer cancel()
	count, err := s.St.RequeueFailed(ctx, gsync.TaskBackfill)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "no failed checkpoints", "requeued": 0})
		return
	}
	if err := s.Engine.StartBackfill(s.Engine.BaseContext()); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"status": "failed checkpoints requeued", "requeued": count})
}

func (s *Server) handleSyncBackfillStop(w http.ResponseWriter, r *http.Request) {
	s.Engine.StopBackfill()
	writeJSON(w, 200, map[string]string{"status": "stopping"})
}

func (s *Server) handleSyncStock(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "latest"
	}
	if mode != "latest" && mode != "missing" && mode != "full" {
		writeErr(w, 400, "mode 仅支持 latest、missing 或 full")
		return
	}
	if err := s.Engine.SyncStock(context.Background(), r.PathValue("code"), mode); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, map[string]string{"status": "stock sync started", "mode": mode})
}

func (s *Server) handleSyncDaily(w http.ResponseWriter, r *http.Request) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.Engine.RunDailySync(ctx); err != nil {
			slog.Error("手动每日同步失败", "err", err)
		}
	}()
	writeJSON(w,
		202, map[string]string{"status": "daily sync started"})
}
