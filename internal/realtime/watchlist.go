package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/hoax/go-stock/internal/model"
	"github.com/hoax/go-stock/internal/provider"
	"github.com/redis/go-redis/v9"
)

const watchlistBatchKey = "realtime:watchlist:v1:current"

// SymbolStore 提供当前自选股代码集合，以及非交易时段可展示的本地收盘快照。
type SymbolStore interface {
	WatchlistSymbols(context.Context) ([]string, error)
	LatestQuotes(context.Context, []string) ([]*model.Quote, error)
}

// QuoteProvider 获取一整批实时行情。
type QuoteProvider interface {
	BatchQuotes(context.Context, []string) ([]*model.Quote, error)
}

// BatchCache 是实时批次所需的严格 Redis 操作。
type BatchCache interface {
	GetStrict(context.Context, string) ([]byte, error)
	SetUntilStrict(context.Context, string, []byte, time.Time) error
	DeleteStrict(context.Context, string) error
}

// WatchlistBatch 是一次完整且原子发布的自选股实时行情。
type WatchlistBatch struct {
	TradeDate string         `json:"trade_date"`
	SyncedAt  time.Time      `json:"synced_at"`
	Symbols   []string       `json:"symbols"`
	Quotes    []*model.Quote `json:"quotes"`
}

// WatchlistResponse 是自选股接口的明确状态响应。
type WatchlistResponse struct {
	Status   string         `json:"status"`
	SyncedAt *time.Time     `json:"synced_at,omitempty"`
	Symbols  []string       `json:"symbols"`
	Quotes   []*model.Quote `json:"quotes"`
}

// WatchlistSyncer 按固定周期发布完整自选股实时行情批次。
type WatchlistSyncer struct {
	store    SymbolStore
	provider QuoteProvider
	cache    BatchCache
	interval time.Duration
	now      func() time.Time
	stateMu  sync.RWMutex
	live     bool
}

func NewWatchlistSyncer(store SymbolStore, quoteProvider QuoteProvider, cache BatchCache, interval time.Duration) *WatchlistSyncer {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &WatchlistSyncer{store: store, provider: quoteProvider, cache: cache, interval: interval, now: time.Now}
}

// Start 启动同步循环，并立即执行首轮同步。
func (s *WatchlistSyncer) Start(ctx context.Context) {
	go func() {
		s.syncOnce(ctx)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.syncOnce(ctx)
			}
		}
	}()
}

func (s *WatchlistSyncer) syncOnce(ctx context.Context) {
	now := s.now()
	if !provider.IsTradingHours(now) {
		s.invalidate(ctx, "非交易时段")
		return
	}

	symbols, err := s.store.WatchlistSymbols(ctx)
	if err != nil {
		s.invalidate(ctx, "读取自选股失败", "err", err)
		return
	}
	symbols = normalizedSymbols(symbols)
	if len(symbols) == 0 {
		s.invalidate(ctx, "自选股为空")
		return
	}

	quotes, err := s.provider.BatchQuotes(ctx, symbols)
	if err != nil {
		s.invalidate(ctx, "实时行情整批同步失败", "err", err)
		return
	}
	ordered, err := completeQuotes(symbols, quotes, now)
	if err != nil {
		s.invalidate(ctx, "实时行情批次不完整", "err", err)
		return
	}

	batch := WatchlistBatch{
		TradeDate: marketDate(now),
		SyncedAt:  now,
		Symbols:   symbols,
		Quotes:    ordered,
	}
	data, err := json.Marshal(batch)
	if err != nil {
		s.invalidate(ctx, "实时行情批次编码失败", "err", err)
		return
	}
	expiresAt := now.Add(max(s.interval*3, 15*time.Second))
	if err := s.cache.SetUntilStrict(ctx, watchlistBatchKey, data, expiresAt); err != nil {
		s.invalidate(ctx, "实时行情批次写入 Redis 失败", "err", err)
		return
	}
	s.setLive(true)
	slog.Debug("自选股实时行情批次已发布", "count", len(ordered), "synced_at", now)
}

func (s *WatchlistSyncer) invalidate(ctx context.Context, message string, args ...any) {
	s.setLive(false)
	if err := s.cache.DeleteStrict(ctx, watchlistBatchKey); err != nil && !errors.Is(err, context.Canceled) {
		args = append(args, "delete_err", err)
	}
	slog.Debug(message, args...)
}

func (s *WatchlistSyncer) setLive(live bool) {
	s.stateMu.Lock()
	s.live = live
	s.stateMu.Unlock()
}

func (s *WatchlistSyncer) isLive() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.live
}

// Response 在交易时段只读取当前 Redis 批次；非交易时段只读取本地快照，
// 不触发任何上游行情请求。
func (s *WatchlistSyncer) Response(ctx context.Context) WatchlistResponse {
	now := s.now()
	symbols, err := s.store.WatchlistSymbols(ctx)
	if err != nil {
		return WatchlistResponse{Status: "unavailable", Symbols: []string{}, Quotes: []*model.Quote{}}
	}
	symbols = normalizedSymbols(symbols)
	if len(symbols) == 0 {
		return WatchlistResponse{Status: "empty", Symbols: symbols, Quotes: []*model.Quote{}}
	}
	if !provider.IsTradingHours(now) {
		quotes, err := s.store.LatestQuotes(ctx, symbols)
		if err != nil {
			slog.Debug("读取盘后自选股收盘快照失败", "err", err)
			return WatchlistResponse{Status: "unavailable", Symbols: symbols, Quotes: []*model.Quote{}}
		}
		ordered := orderStoredQuotes(symbols, quotes)
		response := WatchlistResponse{Status: "closed", Symbols: symbols, Quotes: ordered}
		if len(ordered) > 0 {
			syncedAt := ordered[0].FetchedAt
			for _, quote := range ordered[1:] {
				if quote.FetchedAt.After(syncedAt) {
					syncedAt = quote.FetchedAt
				}
			}
			response.SyncedAt = &syncedAt
		}
		return response
	}
	if !s.isLive() {
		return WatchlistResponse{Status: "unavailable", Symbols: symbols, Quotes: []*model.Quote{}}
	}
	data, err := s.cache.GetStrict(ctx, watchlistBatchKey)
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Debug("读取自选股实时批次失败", "err", err)
		}
		return WatchlistResponse{Status: "unavailable", Symbols: symbols, Quotes: []*model.Quote{}}
	}
	var batch WatchlistBatch
	if err := json.Unmarshal(data, &batch); err != nil || batch.TradeDate != marketDate(now) || !sameSymbols(batch.Symbols, symbols) {
		return WatchlistResponse{Status: "unavailable", Symbols: symbols, Quotes: []*model.Quote{}}
	}
	if _, err := completeQuotes(symbols, batch.Quotes, now); err != nil {
		return WatchlistResponse{Status: "unavailable", Symbols: symbols, Quotes: []*model.Quote{}}
	}
	return WatchlistResponse{Status: "live", SyncedAt: &batch.SyncedAt, Symbols: symbols, Quotes: batch.Quotes}
}

// orderStoredQuotes keeps the user configured watchlist order. A missing local
// snapshot is intentionally omitted instead of clearing every other holding.
func orderStoredQuotes(symbols []string, quotes []*model.Quote) []*model.Quote {
	bySymbol := make(map[string]*model.Quote, len(quotes))
	for _, quote := range quotes {
		if quote == nil || quote.Price == nil || quote.FetchedAt.IsZero() {
			continue
		}
		symbol := model.NormalizeSymbol(quote.Symbol)
		if symbol != "" {
			bySymbol[symbol] = quote
		}
	}
	ordered := make([]*model.Quote, 0, len(symbols))
	for _, symbol := range symbols {
		if quote, ok := bySymbol[symbol]; ok {
			ordered = append(ordered, quote)
		}
	}
	return ordered
}

func normalizedSymbols(inputs []string) []string {
	seen := make(map[string]struct{}, len(inputs))
	out := make([]string, 0, len(inputs))
	for _, input := range inputs {
		symbol := model.NormalizeSymbol(input)
		if symbol == "" {
			continue
		}
		if _, ok := seen[symbol]; ok {
			continue
		}
		seen[symbol] = struct{}{}
		out = append(out, symbol)
	}
	return out
}

func completeQuotes(symbols []string, quotes []*model.Quote, now time.Time) ([]*model.Quote, error) {
	if len(quotes) != len(symbols) {
		return nil, fmt.Errorf("期望 %d 只，实际 %d 只", len(symbols), len(quotes))
	}
	bySymbol := make(map[string]*model.Quote, len(quotes))
	for _, quote := range quotes {
		if quote == nil || quote.Price == nil || quote.FetchedAt.IsZero() {
			return nil, fmt.Errorf("存在无价格或无抓取时间的行情")
		}
		symbol := model.NormalizeSymbol(quote.Symbol)
		if symbol == "" {
			return nil, fmt.Errorf("存在无效行情代码")
		}
		if _, duplicate := bySymbol[symbol]; duplicate {
			return nil, fmt.Errorf("行情代码重复: %s", symbol)
		}
		if marketDate(quote.FetchedAt) != marketDate(now) {
			return nil, fmt.Errorf("行情抓取日期不是当日: %s", symbol)
		}
		if quote.ProviderTimestamp != "" {
			providerTime, err := time.Parse(time.RFC3339, quote.ProviderTimestamp)
			if err != nil || marketDate(providerTime) != marketDate(now) {
				return nil, fmt.Errorf("行情源日期不是当日: %s", symbol)
			}
		}
		bySymbol[symbol] = quote
	}
	ordered := make([]*model.Quote, 0, len(symbols))
	for _, symbol := range symbols {
		quote, ok := bySymbol[symbol]
		if !ok {
			return nil, fmt.Errorf("缺少行情: %s", symbol)
		}
		ordered = append(ordered, quote)
	}
	return ordered, nil
}

func sameSymbols(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func marketDate(t time.Time) string {
	return t.In(provider.MarketLocation()).Format("2006-01-02")
}
