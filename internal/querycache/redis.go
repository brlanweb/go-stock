package querycache

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache 是可选的 Redis 查询缓存。缓存故障只会导致回源 MySQL。
type Cache struct {
	client *redis.Client
	ttl    time.Duration
}

func New(addr, password string, db, ttlSeconds int) *Cache {
	if addr == "" {
		return nil
	}
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
		PoolSize:     4,
	})
	cache := &Cache{client: client, ttl: time.Duration(ttlSeconds) * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis 查询缓存暂不可用，回退 MySQL", "addr", addr, "err", err)
	} else {
		slog.Info("Redis 查询缓存就绪", "addr", addr, "ttl", cache.ttl)
	}
	return cache
}

func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool) {
	value, err := c.GetStrict(ctx, key)
	if err == nil {
		return value, true
	}
	if !errors.Is(err, redis.Nil) && !errors.Is(err, ErrUnavailable) {
		slog.Debug("Redis 读取失败，回退 MySQL", "key", key, "err", err)
	}
	return nil, false
}

// ErrUnavailable 表示 Redis 未配置或客户端不可用。
var ErrUnavailable = errors.New("Redis 不可用")

// GetStrict 读取缓存并向调用方返回 Redis 错误。
func (c *Cache) GetStrict(ctx context.Context, key string) ([]byte, error) {
	if c == nil || c.client == nil {
		return nil, ErrUnavailable
	}
	return c.client.Get(ctx, key).Bytes()
}

func (c *Cache) Set(ctx context.Context, key string, value []byte) {
	if c == nil || c.client == nil {
		return
	}
	if err := c.client.Set(ctx, key, value, c.ttl).Err(); err != nil {
		slog.Debug("Redis 写入失败", "key", key, "err", err)
	}
}

func (c *Cache) SetUntil(ctx context.Context, key string, value []byte, expiresAt time.Time) {
	if err := c.SetUntilStrict(ctx, key, value, expiresAt); err != nil {
		slog.Debug("Redis 定时缓存写入失败", "key", key, "err", err)
	}
}

// SetUntilStrict 原子写入单个缓存值，并向调用方返回 Redis 错误。
func (c *Cache) SetUntilStrict(ctx context.Context, key string, value []byte, expiresAt time.Time) error {
	if c == nil || c.client == nil {
		return ErrUnavailable
	}
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return errors.New("缓存过期时间必须晚于当前时间")
	}
	return c.client.Set(ctx, key, value, ttl).Err()
}

// DeleteStrict 删除缓存值，并向调用方返回 Redis 错误。
func (c *Cache) DeleteStrict(ctx context.Context, key string) error {
	if c == nil || c.client == nil {
		return ErrUnavailable
	}
	return c.client.Del(ctx, key).Err()
}

func (c *Cache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}
