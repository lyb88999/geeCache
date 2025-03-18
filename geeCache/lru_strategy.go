package geeCache

import (
	"github.com/lyb88999/geeCache/geeCache/lru"
)

// LRUStrategy 是 lru.Cache 的适配器，实现了 CacheStrategy 接口
type LRUStrategy struct {
	cache *lru.Cache
}

// NewLRUCache 创建一个新的基于LRU策略的缓存
func NewLRUCache(maxBytes int64, onEvicted func(key string, value ByteView)) CacheStrategy {
	return &LRUStrategy{
		cache: lru.New(maxBytes, func(key string, value lru.Value) {
			if onEvicted != nil {
				onEvicted(key, value.(ByteView))
			}
		}),
	}
}

// Add 添加或更新缓存项
func (l *LRUStrategy) Add(key string, value ByteView) {
	l.cache.Add(key, value)
}

// Get 获取缓存项
func (l *LRUStrategy) Get(key string) (value ByteView, ok bool) {
	if v, ok := l.cache.Get(key); ok {
		return v.(ByteView), ok
	}
	return
}

// RemoveOldest 移除最久未使用的缓存项
func (l *LRUStrategy) RemoveOldest() {
	l.cache.RemoveOldest()
}

// Len 返回缓存中的项数
func (l *LRUStrategy) Len() int {
	return l.cache.Len()
}

// 确保 LRUStrategy 实现了 CacheStrategy 接口
var _ CacheStrategy = (*LRUStrategy)(nil)
