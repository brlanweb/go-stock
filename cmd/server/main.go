// go-stock 主入口：REST API + MCP + 后台同步任务，单进程单二进制。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hoax/go-stock/internal/analysis"
	"github.com/hoax/go-stock/internal/api"
	"github.com/hoax/go-stock/internal/auth"
	"github.com/hoax/go-stock/internal/config"
	"github.com/hoax/go-stock/internal/mcpserver"
	"github.com/hoax/go-stock/internal/provider"
	"github.com/hoax/go-stock/internal/querycache"
	"github.com/hoax/go-stock/internal/realtime"
	"github.com/hoax/go-stock/internal/store"
	gsync "github.com/hoax/go-stock/internal/sync"
	"github.com/hoax/go-stock/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	initLogger(cfg.LogLevel)

	st, err := store.Open(cfg.DSN())
	if err != nil {
		slog.Error("数据库初始化失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	slog.Info("数据库就绪", "host", cfg.DBHost, "db", cfg.DBName)
	queryCache := querycache.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB, cfg.RedisTTLSeconds)
	defer queryCache.Close()

	// Provider + 缓存服务
	mgr := provider.NewManager(cfg.BackfillQPS)
	svc := provider.NewService(mgr, cfg.QuoteTTLSeconds)
	defer svc.Close()

	// 同步引擎
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	engine := gsync.NewEngine(st, mgr, cfg.BackfillWorkers, cfg.BackfillQPS, cfg.SyncSectors, cfg.PythonCommand, cfg.PythonKlineScript)
	engine.SetBaseContext(rootCtx)
	engine.StartDailyScheduler(rootCtx)
	watchlistSyncer := realtime.NewWatchlistSyncer(st, mgr, queryCache, time.Duration(cfg.WatchlistSyncSeconds)*time.Second)
	watchlistSyncer.Start(rootCtx)
	analysisService := analysis.New(st, analysis.Config{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIAPIKey, Model: cfg.AIModel, Prompt: cfg.AIPrompt, HotspotPrompt: cfg.AIHotspotPrompt})
	analysisService.StartScheduler(rootCtx)
	if os.Getenv("GOSTOCK_AUTO_BACKFILL") != "false" {
		if err := engine.StartBackfill(rootCtx); err != nil {
			slog.Warn("启动历史缺失检查失败", "err", err)
		}
	} else {
		slog.Info("启动自动历史回填已禁用")
	}

	// 路由
	mux := http.NewServeMux()
	apiServer := &api.Server{St: st, Svc: svc, Engine: engine, Analysis: analysisService, Cache: queryCache, Watchlist: watchlistSyncer}
	apiServer.Register(mux)

	// 可选页面访问密码
	guard := auth.New(cfg.AccessPassword)
	guard.RegisterRoutes(mux)
	if guard.Enabled() {
		slog.Info("页面访问密码已启用")
	}

	// MCP Streamable HTTP（/mcp）
	mcpHandler := mcpserver.NewHandler(mcpserver.Deps{St: st, Svc: svc, Eng: engine}, cfg.MCPToken)
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)

	// 前端静态资源（embed）
	mux.Handle("/", web.Handler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           guard.Wrap(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("HTTP 服务启动", "addr", cfg.Addr, "mcp", "/mcp", "api", "/api/v1")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务异常退出", "err", err)
			os.Exit(1)
		}
	}()

	// 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("收到退出信号，正在关闭…")
	rootCancel()
	engine.StopBackfill()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	slog.Info("已退出")
}

func initLogger(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv})))
}
