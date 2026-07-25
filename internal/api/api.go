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
	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
	"github.com/hoax/go-stock/internal/store"
	gsync "github.com/hoax/go-stock/internal/sync"
)

// Server API 依赖集合。
type Server struct {
	St       *store.Store
	Svc      *provider.Service
	Engine   *gsync.Engine
	Analysis *analysis.Service
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
	mux.HandleFunc("GET /api/v1/market/heatmap", s.handleMarketHeatmap)
	mux.HandleFunc("GET /api/v1/recommendations", s.handleRecommendations)
	mux.HandleFunc("GET /api/v1/recommendations/history", s.handleRecommendationHistory)
	mux.HandleFunc("POST /api/v1/recommendations/run", s.handleRecommendationsRun)

	mux.HandleFunc("GET /api/v1/watchlist", s.handleWatchlistGet)
	mux.HandleFunc("POST /api/v1/watchlist/{code}", s.handleWatchlistAdd)
	mux.HandleFunc("DELETE /api/v1/watchlist/{code}", s.handleWatchlistDel)

	mux.HandleFunc("GET /api/v1/sync/status", s.handleSyncStatus)
	mux.HandleFunc("POST /api/v1/sync/backfill", s.handleSyncBackfill)
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
	quote, err := s.St.LatestQuote(ctx, symbol)
	if err != nil {
		writeErr(w, http.StatusNotFound, "本地尚无该证券行情快照；请等待下一次定时采集或执行同步")
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
	quotes, err := s.St.LatestQuotes(ctx, symbols)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	writeErr(w, http.StatusNotImplemented, "本地尚未采集分钟级分时快照；请查看日K数据")
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

func (s *Server) handleMarketHeatmap(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	q := r.URL.Query()
	groups, notice, err := s.St.MarketHeatmap(ctx, q.Get("market"), q.Get("group_by"), q.Get("metric"), q.Get("period"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"market":   q.Get("market"),
		"group_by": q.Get("group_by"),
		"metric":   q.Get("metric"),
		"period":   q.Get("period"),
		"notice":   notice,
		"groups":   groups,
	})
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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := s.Analysis.RunDaily(ctx); err != nil {
			slog.Error("AI 推荐执行失败", "err", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "analysis started"})
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
	writeErr(w, http.StatusGone, "全市场历史回填已停用；请使用 POST /api/v1/sync/stock/{code}?mode=full 显式同步单只股票")
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
