// Package mcpserver MCP Streamable HTTP 端点（/mcp），供 LobeHub 等 MCP 客户端调用。
// Package mcpserver MCP Streamable HTTP 端点（/mcp），供 LobeHub 等 MCP 客户端调用。
package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
	"github.com/hoax/go-stock/internal/store"
	gsync "github.com/hoax/go-stock/internal/sync"
)

// Deps MCP 工具依赖。
type Deps struct {
	St  *store.Store
	Svc *provider.Service
	Eng *gsync.Engine
}

// NewHandler 构建挂载于 /mcp 的 http.Handler（无状态模式，便于 LobeHub 直连）。
// token 非空时启用 Bearer 鉴权。
func NewHandler(d Deps, token string) http.Handler {
	s := server.NewMCPServer("go-stock", "1.1.0",
		server.WithToolCapabilities(false),
		server.WithInstructions("A股本地数据分析服务：报价、指数和自选股均读取最近一次定时入库快照；K线、行业云图、搜索、每日指标和同步状态均查询 MySQL。响应中的 fetched_at 表示快照采集时间。symbol 支持 600519 / SH600519 / 000001.SZ。sync_stock_history 是唯一显式写操作，仅在用户要求同步单只证券时调用。"),
	)
	registerTools(s, d)

	httpServer := server.NewStreamableHTTPServer(s, server.WithStateLess(true))
	if token == "" {
		return httpServer
	}
	return authMiddleware(httpServer, token)
}

// authMiddleware Bearer Token 鉴权。
func authMiddleware(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// jsonResult 序列化为 MCP 文本结果。
func jsonResult(v interface{}) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

func registerTools(s *server.MCPServer, d Deps) {
	// 1. get_realtime_quote
	s.AddTool(mcp.NewTool("get_realtime_quote",
		mcp.WithDescription("获取单只A股/ETF最近一次本地定时行情快照（含采集时间；不访问外部行情源）"),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("股票代码，如 600519 / SH600519 / 000001.SZ")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		symbol := model.NormalizeSymbol(req.GetString("symbol", ""))
		if symbol == "" {
			return mcp.NewToolResultError("无法识别的代码"), nil
		}
		q, err := d.St.LatestQuote(ctx, symbol)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(q)
	})

	// 2. get_batch_quotes
	s.AddTool(mcp.NewTool("get_batch_quotes",
		mcp.WithDescription("批量获取A股/ETF最近一次本地行情快照（最多100只，不访问外部行情源）"),
		mcp.WithString("symbols", mcp.Required(), mcp.Description("逗号分隔的代码列表，如 600519,000001,510300")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		codes := strings.Split(req.GetString("symbols", ""), ",")
		if len(codes) > 100 {
			return mcp.NewToolResultError("单次最多100只"), nil
		}
		symbols := make([]string, 0, len(codes))
		for _, code := range codes {
			if symbol := model.NormalizeSymbol(code); symbol != "" {
				symbols = append(symbols, symbol)
			}
		}
		quotes, err := d.St.LatestQuotes(ctx, symbols)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(quotes)
	})

	// 3. get_kline
	s.AddTool(mcp.NewTool("get_kline",
		mcp.WithDescription("获取历史K线（库内数据：日/周/月线，支持前复权qfq与不复权none）"),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("股票代码")),
		mcp.WithString("period", mcp.Description("day/week/month，默认day")),
		mcp.WithString("adjust", mcp.Description("qfq前复权/none不复权，默认qfq")),
		mcp.WithNumber("limit", mcp.Description("返回根数，默认250，最大5000")),
		mcp.WithString("start", mcp.Description("开始日期 YYYY-MM-DD，可选")),
		mcp.WithString("end", mcp.Description("结束日期 YYYY-MM-DD，可选")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		symbol := model.NormalizeSymbol(req.GetString("symbol", ""))
		if symbol == "" {
			return mcp.NewToolResultError("无法识别的代码"), nil
		}
		period := req.GetString("period", "day")
		adjust := req.GetString("adjust", "qfq")
		limit := int(req.GetFloat("limit", 250))
		klines, err := d.St.QueryKlines(ctx, symbol, period, adjust, req.GetString("start", ""), req.GetString("end", ""), limit)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(klines)
	})

	// 4. search_stock
	s.AddTool(mcp.NewTool("search_stock",
		mcp.WithDescription("按代码或名称搜索A股/ETF"),
		mcp.WithString("keyword", mcp.Required(), mcp.Description("代码前缀或名称关键词，如 茅台 / 600 / 白酒")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		results, err := d.St.SearchSecurities(ctx, req.GetString("keyword", ""), 20)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(results)
	})

	// 5. get_market_indices
	s.AddTool(mcp.NewTool("get_market_indices",
		mcp.WithDescription("获取最近一次定时入库的大盘指数快照（不访问外部行情源）"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		idx, err := d.St.LatestIndices(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(idx)
	})

	// 6. get_watchlist
	s.AddTool(mcp.NewTool("get_watchlist",
		mcp.WithDescription("获取自选股列表及其最近一次本地行情快照"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		symbols, err := d.St.WatchlistSymbols(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(symbols) == 0 {
			return mcp.NewToolResultText("[]"), nil
		}
		quotes, err := d.St.LatestQuotes(ctx, symbols)
		if err != nil {
			return jsonResult(symbols)
		}
		return jsonResult(quotes)
	})

	// 7. get_daily_indicator
	s.AddTool(mcp.NewTool("get_daily_indicator",
		mcp.WithDescription("获取每日指标历史快照（PE/PB/总市值/流通市值/换手率/量比，按日留痕）"),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("股票代码")),
		mcp.WithNumber("limit", mcp.Description("返回天数，默认250")),
		mcp.WithString("start", mcp.Description("开始日期 YYYY-MM-DD，可选")),
		mcp.WithString("end", mcp.Description("结束日期 YYYY-MM-DD，可选")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		symbol := model.NormalizeSymbol(req.GetString("symbol", ""))
		if symbol == "" {
			return mcp.NewToolResultError("无法识别的代码"), nil
		}
		list, err := d.St.QueryDailyIndicators(ctx, symbol, req.GetString("start", ""), req.GetString("end", ""), int(req.GetFloat("limit", 250)))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(list)
	})

	// 8. get_market_heatmap
	s.AddTool(mcp.NewTool("get_market_heatmap",
		mcp.WithDescription("获取A股行业市场云图；支持A股/创业板/科创板/北交所、涨跌幅或PE TTM、今日/三日/五日。概念和主力资金未同步时会返回明确提示。"),
		mcp.WithString("market", mcp.Description("all/gem/star/bse，默认all")),
		mcp.WithString("group_by", mcp.Description("industry/concept，默认industry")),
		mcp.WithString("metric", mcp.Description("change_pct/pe_ttm/main_net_inflow，默认change_pct")),
		mcp.WithString("period", mcp.Description("1d/3d/5d，默认1d")),
		mcp.WithNumber("limit", mcp.Description("按热度返回的证券总数，默认100，最大5000")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		groups, notice, err := d.St.MarketHeatmap(ctx, req.GetString("market", "all"), req.GetString("group_by", "industry"), req.GetString("metric", "change_pct"), req.GetString("period", "1d"), int(req.GetFloat("limit", 100)))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{"groups": groups, "notice": notice})
	})

	// 9. sync_stock_history
	s.AddTool(mcp.NewTool("sync_stock_history",
		mcp.WithDescription("按用户明确指令异步同步单只股票；latest只拉当天，missing补缺失日期，full拉取上市以来历史。"),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("股票代码")),
		mcp.WithString("mode", mcp.Description("latest/missing/full，默认latest")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mode := req.GetString("mode", "latest")
		if mode != "latest" && mode != "missing" && mode != "full" {
			return mcp.NewToolResultError("mode 仅支持 latest、missing 或 full"), nil
		}
		if err := d.Eng.SyncStock(context.Background(), req.GetString("symbol", ""), mode); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]string{"status": "stock sync started", "mode": mode})
	})

	// 10. get_sync_status
	s.AddTool(mcp.NewTool("get_sync_status",
		mcp.WithDescription("查询历史数据回填进度与库内最新数据日期"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		status, err := d.St.SyncStatus(ctx, gsync.TaskBackfill)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]interface{}{
			"backfill":         status,
			"backfill_running": d.Eng.IsRunning(),
		})
	})
}
