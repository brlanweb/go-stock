// Package cache 进程内 TTL 缓存（低内存：无第三方依赖，定期清理过期项）。
package cache

import (
	"sync"
	"time"
)

type entry struct {
	value    interface{}
	expireAt time.Time
}

// TTLCache 并发安全的 TTL 缓存。
type TTLCache struct {
	mu    sync.RWMutex
	items map[string]entry
	stop  chan struct{}
}

// New 创建缓存并启动后台清理（每分钟）。
func New() *TTLCache {
	c := &TTLCache{
		items: make(map[string]entry),
		stop:  make(chan struct{}),
	}
	go c.janitor()
	return c
}

// Get 获取未过期的值。
func (c *TTLCache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.items[key]
	if !ok || time.Now().After(e.expireAt) {
		return nil, false
	}
	return e.value, true
}

// Set 写入带 TTL 的值。
func (c *TTLCache) Set(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = entry{value: value, expireAt: time.Now().Add(ttl)}
}

// Close 停止后台清理。
func (c *TTLCache) Close() { close(c.stop) }

func (c *TTLCache) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			now := time.Now()
			c.mu.Lock()
			for k, e := range c.items {
				if now.After(e.expireAt) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		case <-c.stop:
			return
		}
	}
}
