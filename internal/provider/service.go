package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/cache"
	"github.com/hoax/go-stock/internal/model"
)

// Service 行情服务：Manager 之上加 TTL 缓存（交易时段短 TTL，收盘后长 TTL）。
type Service struct {
	mgr      *Manager
	cache    *cache.TTLCache
	shortTTL time.Duration // 交易时段
	longTTL  time.Duration // 非交易时段
}

// NewService 构建行情服务。
func NewService(mgr *Manager, quoteTTLSeconds int) *Service {
	return &Service{
		mgr:      mgr,
		cache:    cache.New(),
		shortTTL: time.Duration(quoteTTLSeconds) * time.Second,
		longTTL:  60 * time.Second,
	}
}

// Manager 暴露底层管理器。
func (s *Service) Manager() *Manager { return s.mgr }

// Close 释放缓存。
func (s *Service) Close() { s.cache.Close() }

// ttl 根据 A股交易时段决定缓存时长。
func (s *Service) ttl() time.Duration {
	if IsTradingHours(time.Now()) {
		return s.shortTTL
	}
	return s.longTTL
}

// IsTradingHours 判断是否 A股连续竞价时段（工作日 9:15-11:30 / 13:00-15:05，粗粒度，不含节假日精判）。
func IsTradingHours(t time.Time) bool {
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return false
	}
	hm := t.Hour()*100 + t.Minute()
	return (hm >= 915 && hm <= 1135) || (hm >= 1300 && hm <= 1505)
}

// Quote 带缓存的单只行情。
func (s *Service) Quote(ctx context.Context, input string) (*model.Quote, error) {
	symbol := model.NormalizeSymbol(input)
	if symbol == "" {
		return nil, fmt.Errorf("无法识别的代码: %s", input)
	}
	key := "q:" + symbol
	if v, ok := s.cache.Get(key); ok {
		return v.(*model.Quote), nil
	}
	q, err := s.mgr.Quote(ctx, symbol)
	if err != nil {
		return nil, err
	}
	s.cache.Set(key, q, s.ttl())
	return q, nil
}

// BatchQuotes 带缓存的批量行情（整批缓存键 = 排序后 symbol 串哈希，简单起见直接串联）。
func (s *Service) BatchQuotes(ctx context.Context, inputs []string) ([]*model.Quote, error) {
	symbols := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if sym := model.NormalizeSymbol(in); sym != "" {
			symbols = append(symbols, sym)
		}
	}
	if len(symbols) == 0 {
		return nil, fmt.Errorf("无有效代码")
	}
	key := "bq:" + strings.Join(symbols, ",")
	if v, ok := s.cache.Get(key); ok {
		return v.([]*model.Quote), nil
	}
	quotes, err := s.mgr.BatchQuotes(ctx, symbols)
	if err != nil {
		return nil, err
	}
	s.cache.Set(key, quotes, s.ttl())
	return quotes, nil
}

// Indices 带缓存的大盘指数。
func (s *Service) Indices(ctx context.Context) ([]model.IndexQuote, error) {
	key := "indices"
	if v, ok := s.cache.Get(key); ok {
		return v.([]model.IndexQuote), nil
	}
	idx, err := s.mgr.Indices(ctx)
	if err != nil {
		return nil, err
	}
	s.cache.Set(key, idx, s.ttl())
	return idx, nil
}

// MinuteKlines 获取当日一分钟蜡烛。持久缓存由 API 层 Redis 负责，此处不写内存缓存。
func (s *Service) MinuteKlines(ctx context.Context, input string) ([]model.MinuteKline, error) {
	symbol := model.NormalizeSymbol(input)
	if symbol == "" {
		return nil, fmt.Errorf("无法识别的代码: %s", input)
	}
	return s.mgr.Eastmoney().MinuteKlines(ctx, symbol)
}

// Timeshare 带缓存的分时（TTL 同行情）。
func (s *Service) Timeshare(ctx context.Context, input string) ([]model.TimesharePoint, error) {
	symbol := model.NormalizeSymbol(input)
	if symbol == "" {
		return nil, fmt.Errorf("无法识别的代码: %s", input)
	}
	key := "ts:" + symbol
	if v, ok := s.cache.Get(key); ok {
		return v.([]model.TimesharePoint), nil
	}
	points, err := s.mgr.Eastmoney().Timeshare(ctx, symbol)
	if err != nil {
		return nil, err
	}
	s.cache.Set(key, points, s.ttl())
	return points, nil
}
