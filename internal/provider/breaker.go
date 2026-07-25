package provider

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// CircuitBreaker 数据源熔断器（参考 daily_stock_analysis realtime_types.CircuitBreaker）。
// 连续失败 N 次进入冷却期，冷却期内直接跳过该源。
type CircuitBreaker struct {
	mu           sync.Mutex
	failCount    int
	threshold    int           // 连续失败阈值
	cooldown     time.Duration // 冷却时长
	openUntil    time.Time     // 熔断截止时间
	totalTripped int
}

// NewCircuitBreaker threshold 次连续失败后熔断 cooldown。
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{threshold: threshold, cooldown: cooldown}
}

// Allow 当前是否允许请求。
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return time.Now().After(cb.openUntil)
}

// Success 记录成功，重置失败计数。
func (cb *CircuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failCount = 0
}

// Failure 记录失败，达到阈值则熔断。
func (cb *CircuitBreaker) Failure(source string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failCount++
	if cb.failCount >= cb.threshold {
		cb.openUntil = time.Now().Add(cb.cooldown)
		cb.failCount = 0
		cb.totalTripped++
		slog.Warn("数据源熔断", "source", source, "cooldown", cb.cooldown, "tripped_total", cb.totalTripped)
	}
}

// RateGate 限流 + 随机抖动（防上游封禁）。
type RateGate struct {
	limiter *rate.Limiter
	jitter  time.Duration // 最大随机附加延迟
}

// NewRateGate qps 上限与最大抖动。
func NewRateGate(qps float64, jitter time.Duration) *RateGate {
	return &RateGate{
		limiter: rate.NewLimiter(rate.Limit(qps), 1),
		jitter:  jitter,
	}
}

// Wait 阻塞至允许发起请求（含随机抖动）。
func (g *RateGate) Wait(ctx context.Context) error {
	if err := g.limiter.Wait(ctx); err != nil {
		return err
	}
	if g.jitter > 0 {
		select {
		case <-time.After(time.Duration(rand.Int63n(int64(g.jitter)))):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
