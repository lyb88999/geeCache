package geeCache

import (
	"sync"
)

type cache struct {
	mu         sync.Mutex
	strategy   CacheStrategy // 替换原来的 lru 对象
	cacheBytes int64
	factory    CacheStrategyFactory // 工厂函数，用于创建缓存策略
}

func (c *cache) add(key string, value ByteView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 延迟初始化
	if c.strategy == nil {
		c.strategy = c.factory(c.cacheBytes, nil)
	}
	c.strategy.Add(key, value)
}

func (c *cache) get(key string) (value ByteView, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.strategy == nil {
		return
	}
	return c.strategy.Get(key)
}

// clear 清空缓存
func (c *cache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 重新创建策略实例，相当于清空
	if c.strategy != nil {
		c.strategy = c.factory(c.cacheBytes, nil)
	}
}

// len 返回缓存项数
func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.strategy == nil {
		return 0
	}
	return c.strategy.Len()
}
