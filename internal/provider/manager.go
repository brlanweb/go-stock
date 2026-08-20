package provider

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// quoteSource 行情源 + 熔断器组合。
type quoteSource struct {
	provider QuoteProvider
	breaker  *CircuitBreaker
}

// Manager 多源降级管理器：东财(全字段) -> 腾讯 -> 新浪。
// 参考 daily_stock_analysis DataFetcherManager 的策略切换设计。
type Manager struct {
	em      *Eastmoney
	tencent *Tencent
	sina    *Sina
	sources []quoteSource

	indexMu       sync.RWMutex
	lastIndices   []model.IndexQuote
	lastIndicesAt time.Time

	globalMu     sync.RWMutex
	lastGlobal   []model.GlobalQuote
	lastGlobalAt time.Time
}

// NewManager 构建默认降级链。
func NewManager(emQPS float64) *Manager {
	em := NewEastmoney(emQPS)
	tc := NewTencent()
	sn := NewSina()
	return &Manager{
		em:      em,
		tencent: tc,
		sina:    sn,
		sources: []quoteSource{
			{em, em.Breaker()},
			{tc, tc.Breaker()},
			{sn, sn.Breaker()},
		},
	}
}

// Eastmoney 暴露东财源（K线/列表/分时仅东财实现）。
func (m *Manager) Eastmoney() *Eastmoney { return m.em }

// Quote 单只实时行情，按降级链尝试。
func (m *Manager) Quote(ctx context.Context, symbol string) (*model.Quote, error) {
	var lastErr error
	fallbackFrom := ""
	for _, src := range m.sources {
		if !src.breaker.Allow() {
			continue
		}
		q, err := src.provider.Quote(ctx, symbol)
		if err != nil {
			src.breaker.Failure(src.provider.Name())
			lastErr = err
			if fallbackFrom == "" {
				fallbackFrom = src.provider.Name()
			}
			slog.Debug("行情源失败，降级", "source", src.provider.Name(), "symbol", symbol, "err", err)
			continue
		}
		src.breaker.Success()
		if fallbackFrom != "" && fallbackFrom != src.provider.Name() {
			q.FallbackFrom = fallbackFrom
		}
		return q, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("所有行情源均熔断中")
	}
	return nil, fmt.Errorf("获取行情失败 %s: %w", symbol, lastErr)
}

// BatchQuotes 批量实时行情，按降级链尝试。
func (m *Manager) BatchQuotes(ctx context.Context, symbols []string) ([]*model.Quote, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	var lastErr error
	for _, src := range m.sources {
		if !src.breaker.Allow() {
			continue
		}
		quotes, err := src.provider.BatchQuotes(ctx, symbols)
		if err != nil || len(quotes) == 0 {
			src.breaker.Failure(src.provider.Name())
			if err == nil {
				err = fmt.Errorf("空结果")
			}
			lastErr = err
			slog.Debug("批量行情源失败，降级", "source", src.provider.Name(), "err", err)
			continue
		}
		src.breaker.Success()
		return quotes, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("所有行情源均熔断中")
	}
	return nil, fmt.Errorf("批量获取行情失败: %w", lastErr)
}

// 指数 symbol 常量（东财 secid 特殊：上证指数 1.000001，深证成指 0.399001，创业板指 0.399006，科创50 1.000688，沪深300 1.000300，北证50 0.899050）。
var IndexSecIDs = []struct {
	Symbol string
	Name   string
	SecID  string
}{
	{"SH000001", "上证指数", "1.000001"},
	{"SZ399001", "深证成指", "0.399001"},
	{"SZ399006", "创业板指", "0.399006"},
	{"SH000688", "科创50", "1.000688"},
	{"SH000300", "沪深300", "1.000300"},
	{"BJ899050", "北证50", "0.899050"},
}

// Indices 大盘指数。东财优先；临时 EOF/限流时重试后降级腾讯，并返回最近成功缓存。
func (m *Manager) Indices(ctx context.Context) ([]model.IndexQuote, error) {
	var lastErr error
	if m.em.Breaker().Allow() {
		for attempt := 0; attempt < 2; attempt++ {
			if attempt > 0 {
				select {
				case <-time.After(250 * time.Millisecond):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			quotes, err := m.em.batchQuotesBySecids(ctx, indexSecIDs())
			if err != nil {
				lastErr = err
				continue
			}
			out := indexQuotesFromQuotes(quotes)
			if len(out) > 0 {
				m.em.Breaker().Success()
				m.rememberIndices(out)
				return out, nil
			}
			lastErr = fmt.Errorf("东方财富指数返回为空")
		}
		m.em.Breaker().Failure(m.em.Name())
	}

	for _, src := range []quoteSource{{m.tencent, m.tencent.Breaker()}, {m.sina, m.sina.Breaker()}} {
		if !src.breaker.Allow() {
			continue
		}
		quotes, err := src.provider.BatchQuotes(ctx, indexSymbols())
		if err != nil {
			src.breaker.Failure(src.provider.Name())
			lastErr = err
			continue
		}
		out := indexQuotesFromQuotes(quotes)
		if len(out) == 0 {
			src.breaker.Failure(src.provider.Name())
			lastErr = fmt.Errorf("%s 指数返回为空", src.provider.Name())
			continue
		}
		src.breaker.Success()
		slog.Warn("指数行情已降级", "source", src.provider.Name(), "fallback_from", "eastmoney", "count", len(out))
		m.rememberIndices(out)
		return out, nil
	}

	if cached, cachedAt := m.cachedIndices(); len(cached) > 0 {
		slog.Warn("指数行情源不可用，返回最近成功缓存", "age", time.Since(cachedAt).Round(time.Second), "err", lastErr)
		return cached, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("所有指数行情源均熔断中")
	}
	return nil, fmt.Errorf("获取大盘指数失败: %w", lastErr)
}

func indexSecIDs() string {
	secids := ""
	for i, idx := range IndexSecIDs {
		if i > 0 {
			secids += ","
		}
		secids += idx.SecID
	}
	return secids
}

func indexSymbols() []string {
	symbols := make([]string, 0, len(IndexSecIDs))
	for _, idx := range IndexSecIDs {
		symbols = append(symbols, idx.Symbol)
	}
	return symbols
}

func indexQuotesFromQuotes(quotes []*model.Quote) []model.IndexQuote {
	metaBySymbol := make(map[string]struct{ Symbol, Name string }, len(IndexSecIDs))
	for _, idx := range IndexSecIDs {
		metaBySymbol[idx.Symbol] = struct{ Symbol, Name string }{idx.Symbol, idx.Name}
	}
	out := make([]model.IndexQuote, 0, len(quotes))
	for _, q := range quotes {
		meta, ok := metaBySymbol[q.Symbol]
		if !ok {
			continue
		}
		out = append(out, model.IndexQuote{
			Symbol:    meta.Symbol,
			Name:      meta.Name,
			Price:     q.Price,
			ChangePct: q.ChangePct,
			Amount:    q.Amount,
			Volume:    q.Volume,
		})
	}
	return out
}

// GlobalQuotes 隔夜外盘风险因子行情：东财批量 + 金龙指数 + 新浪 VIX。
// 东财失败时返回最近成功缓存（保守可用），VIX 缺失只损失单一因子不阻断。
func (m *Manager) GlobalQuotes(ctx context.Context) ([]model.GlobalQuote, error) {
	var out []model.GlobalQuote
	var lastErr error
	if m.em.Breaker().Allow() {
		quotes, err := m.em.GlobalQuotes(ctx)
		if err != nil {
			m.em.Breaker().Failure(m.em.Name())
			lastErr = err
		} else {
			m.em.Breaker().Success()
			out = quotes
		}
	}
	if m.sina.Breaker().Allow() {
		vix, err := m.sina.GlobalVIX(ctx)
		if err != nil {
			m.sina.Breaker().Failure(m.sina.Name())
			slog.Warn("VIX 行情获取失败，本轮缺失该因子", "err", err)
		} else {
			m.sina.Breaker().Success()
			out = append(out, *vix)
		}
	}
	if len(out) > 0 {
		m.rememberGlobal(out)
		return out, nil
	}
	if cached, cachedAt := m.cachedGlobal(); len(cached) > 0 {
		slog.Warn("外盘行情源不可用，返回最近成功缓存", "age", time.Since(cachedAt).Round(time.Second), "err", lastErr)
		return cached, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("外盘行情源均熔断中")
	}
	return nil, fmt.Errorf("获取外盘行情失败: %w", lastErr)
}

func (m *Manager) rememberGlobal(quotes []model.GlobalQuote) {
	m.globalMu.Lock()
	defer m.globalMu.Unlock()
	m.lastGlobal = append([]model.GlobalQuote(nil), quotes...)
	m.lastGlobalAt = time.Now()
}

func (m *Manager) cachedGlobal() ([]model.GlobalQuote, time.Time) {
	m.globalMu.RLock()
	defer m.globalMu.RUnlock()
	return append([]model.GlobalQuote(nil), m.lastGlobal...), m.lastGlobalAt
}

func (m *Manager) rememberIndices(indices []model.IndexQuote) {
	m.indexMu.Lock()
	defer m.indexMu.Unlock()
	m.lastIndices = append([]model.IndexQuote(nil), indices...)
	m.lastIndicesAt = time.Now()
}

func (m *Manager) cachedIndices() ([]model.IndexQuote, time.Time) {
	m.indexMu.RLock()
	defer m.indexMu.RUnlock()
	return append([]model.IndexQuote(nil), m.lastIndices...), m.lastIndicesAt
}
