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
	"github.com/hoax/go-stock/internal/config"
	"github.com/hoax/go-stock/internal/mcpserver"
	"github.com/hoax/go-stock/internal/provider"
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

	// Provider + 缓存服务
	mgr := provider.NewManager(cfg.BackfillQPS)
	svc := provider.NewService(mgr, cfg.QuoteTTLSeconds)
	defer svc.Close()

	// 同步引擎
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	engine := gsync.NewEngine(st, mgr, cfg.BackfillWorkers)
	engine.StartDailyScheduler(rootCtx)
	analysisService := analysis.New(st, analysis.Config{BaseURL: cfg.AIBaseURL, APIKey: cfg.AIAPIKey, Model: cfg.AIModel, Prompt: cfg.AIPrompt})
	analysisService.StartScheduler(rootCtx)
	if err := engine.StartBackfill(rootCtx); err != nil {
		slog.Warn("启动历史缺失检查失败", "err", err)
	}

	// 路由
	mux := http.NewServeMux()
	apiServer := &api.Server{St: st, Svc: svc, Engine: engine, Analysis: analysisService}
	apiServer.Register(mux)

	// MCP Streamable HTTP（/mcp）
	mcpHandler := mcpserver.NewHandler(mcpserver.Deps{St: st, Svc: svc, Eng: engine}, cfg.MCPToken)
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)

	// 前端静态资源（embed）
	mux.Handle("/", web.Handler())

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
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
