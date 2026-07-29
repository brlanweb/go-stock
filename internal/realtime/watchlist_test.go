package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hoax/go-stock/internal/model"
	"github.com/redis/go-redis/v9"
)

type fakeSymbolStore struct {
	symbols []string
	err     error
}

func (s *fakeSymbolStore) WatchlistSymbols(context.Context) ([]string, error) {
	return append([]string(nil), s.symbols...), s.err
}

type fakeQuoteProvider struct {
	quotes []*model.Quote
	err    error
	calls  int
}

func (p *fakeQuoteProvider) BatchQuotes(context.Context, []string) ([]*model.Quote, error) {
	p.calls++
	return p.quotes, p.err
}

type fakeBatchCache struct {
	value       []byte
	getErr      error
	setErr      error
	deleteErr   error
	setCalls    int
	deleteCalls int
}

func (c *fakeBatchCache) GetStrict(context.Context, string) ([]byte, error) {
	if c.getErr != nil {
		return nil, c.getErr
	}
	if c.value == nil {
		return nil, redis.Nil
	}
	return append([]byte(nil), c.value...), nil
}

func (c *fakeBatchCache) SetUntilStrict(_ context.Context, _ string, value []byte, _ time.Time) error {
	c.setCalls++
	if c.setErr != nil {
		return c.setErr
	}
	c.value = append([]byte(nil), value...)
	return nil
}

func (c *fakeBatchCache) DeleteStrict(context.Context, string) error {
	c.deleteCalls++
	c.value = nil
	return c.deleteErr
}

func TestWatchlistSyncPublishesCompleteBatch(t *testing.T) {
	now := tradingTime()
	store := &fakeSymbolStore{symbols: []string{"SH600519", "SZ000001"}}
	quotes := []*model.Quote{quoteAt("SZ000001", now), quoteAt("SH600519", now)}
	upstream := &fakeQuoteProvider{quotes: quotes}
	cache := &fakeBatchCache{}
	syncer := NewWatchlistSyncer(store, upstream, cache, 5*time.Second)
	syncer.now = func() time.Time { return now }

	syncer.syncOnce(context.Background())

	if cache.setCalls != 1 || cache.deleteCalls != 0 {
		t.Fatalf("set=%d delete=%d, want set once without delete", cache.setCalls, cache.deleteCalls)
	}
	var batch WatchlistBatch
	if err := json.Unmarshal(cache.value, &batch); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(batch.Quotes) != 2 || batch.Quotes[0].Symbol != "SH600519" || batch.Quotes[1].Symbol != "SZ000001" {
		t.Fatalf("quotes were not published in watchlist order: %#v", batch.Quotes)
	}
	response := syncer.Response(context.Background())
	if response.Status != "live" || len(response.Quotes) != 2 || len(response.Symbols) != 2 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestWatchlistSyncRejectsIncompleteBatch(t *testing.T) {
	now := tradingTime()
	cache := &fakeBatchCache{value: []byte("stale")}
	syncer := NewWatchlistSyncer(
		&fakeSymbolStore{symbols: []string{"SH600519", "SZ000001"}},
		&fakeQuoteProvider{quotes: []*model.Quote{quoteAt("SH600519", now)}},
		cache,
		5*time.Second,
	)
	syncer.now = func() time.Time { return now }

	syncer.syncOnce(context.Background())

	if cache.setCalls != 0 || cache.deleteCalls != 1 || cache.value != nil {
		t.Fatalf("incomplete batch must invalidate cache: %#v", cache)
	}
	if response := syncer.Response(context.Background()); response.Status != "unavailable" || len(response.Quotes) != 0 {
		t.Fatalf("unexpected response after incomplete batch: %#v", response)
	}
}

func TestWatchlistSyncInvalidatesOnUpstreamOrRedisFailure(t *testing.T) {
	now := tradingTime()
	tests := []struct {
		name     string
		provider *fakeQuoteProvider
		cache    *fakeBatchCache
	}{
		{
			name:     "upstream failure",
			provider: &fakeQuoteProvider{err: errors.New("upstream unavailable")},
			cache:    &fakeBatchCache{value: []byte("stale")},
		},
		{
			name:     "redis write failure",
			provider: &fakeQuoteProvider{quotes: []*model.Quote{quoteAt("SH600519", now)}},
			cache:    &fakeBatchCache{value: []byte("stale"), setErr: errors.New("redis unavailable")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := NewWatchlistSyncer(&fakeSymbolStore{symbols: []string{"SH600519"}}, tt.provider, tt.cache, 5*time.Second)
			syncer.now = func() time.Time { return now }
			syncer.syncOnce(context.Background())
			if tt.cache.deleteCalls != 1 || tt.cache.value != nil {
				t.Fatalf("failed round must invalidate published batch: %#v", tt.cache)
			}
		})
	}
}

func TestWatchlistResponseRejectsChangedSymbolSet(t *testing.T) {
	now := tradingTime()
	store := &fakeSymbolStore{symbols: []string{"SH600519"}}
	cache := &fakeBatchCache{}
	syncer := NewWatchlistSyncer(store, &fakeQuoteProvider{quotes: []*model.Quote{quoteAt("SH600519", now)}}, cache, 5*time.Second)
	syncer.now = func() time.Time { return now }
	syncer.syncOnce(context.Background())

	store.symbols = []string{"SH600519", "SZ000001"}
	response := syncer.Response(context.Background())
	if response.Status != "unavailable" || len(response.Quotes) != 0 || len(response.Symbols) != 2 {
		t.Fatalf("changed watchlist must not receive previous batch: %#v", response)
	}
}

func TestWatchlistClosedDoesNotCallProviderOrExposeQuotes(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	store := &fakeSymbolStore{symbols: []string{"SH600519"}}
	upstream := &fakeQuoteProvider{quotes: []*model.Quote{quoteAt("SH600519", now)}}
	cache := &fakeBatchCache{value: []byte("stale")}
	syncer := NewWatchlistSyncer(store, upstream, cache, 5*time.Second)
	syncer.now = func() time.Time { return now }

	syncer.syncOnce(context.Background())
	response := syncer.Response(context.Background())

	if upstream.calls != 0 || cache.deleteCalls != 1 {
		t.Fatalf("closed market provider calls=%d delete=%d", upstream.calls, cache.deleteCalls)
	}
	if response.Status != "closed" || len(response.Quotes) != 0 || len(response.Symbols) != 1 {
		t.Fatalf("unexpected closed response: %#v", response)
	}
}

func tradingTime() time.Time {
	return time.Date(2026, 7, 29, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
}

func quoteAt(symbol string, now time.Time) *model.Quote {
	price := 100.0
	return &model.Quote{Symbol: symbol, Price: &price, FetchedAt: now}
}
