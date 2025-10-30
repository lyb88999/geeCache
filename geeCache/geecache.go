package geeCache

import (
	bytes2 "bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lyb88999/geeCache/singleFlight"
	"go.uber.org/zap"
)

type Getter interface {
	Get(key string) ([]byte, error)
}

// GetterFunc 接口型函数
type GetterFunc func(key string) ([]byte, error)

func (f GetterFunc) Get(key string) ([]byte, error) {
	return f(key)
}

// Stats 缓存组统计信息
type Stats struct {
	Hits        int64     // 缓存命中次数
	Misses      int64     // 缓存未命中次数
	Gets        int64     // 总获取次数
	Loads       int64     // 从数据源加载次数
	LocalLoads  int64     // 从本地加载次数
	PeerLoads   int64     // 从远程节点加载次数
	PeerErrors  int64     // 远程节点错误次数
	LoadSuccess int64     // 加载成功次数
	LoadErrors  int64     // 加载错误次数
	CacheBytes  int64     // 当前缓存大小(字节)
	CacheItems  int64     // 当前缓存项数
	CreatedAt   time.Time // 创建时间
}

// HitRate 计算命中率
func (s *Stats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// Group 一个Group可以认为是一个缓存的命名空间
type Group struct {
	name      string              // 每个Group拥有一个唯一的名称name
	getter    Getter              // 缓存未命中时获取数据的回调
	mainCache cache               // 实现的并发缓存
	peers     PeerPicker          // 分布式节点选择
	loader    *singleFlight.Group // 防止缓存击穿
	logger    *zap.Logger         // 日志记录器

	// 统计信息（使用原子操作）
	stats Stats
	mu    sync.RWMutex // 保护 stats.CacheBytes 和 stats.CacheItems
}

var (
	mu     sync.RWMutex
	groups = make(map[string]*Group)
	// 默认使用LRU策略
	defaultStrategyFactory = NewLRUCache
)

// NewGroup 创建一个新的缓存命名空间，使用默认缓存策略
func NewGroup(name string, cacheBytes int64, getter Getter) *Group {
	return NewGroupWithStrategy(name, cacheBytes, getter, defaultStrategyFactory)
}

// NewGroupWithStrategy 创建一个使用指定缓存策略的缓存命名空间
func NewGroupWithStrategy(name string, cacheBytes int64, getter Getter, factory CacheStrategyFactory) *Group {
	if getter == nil {
		panic("getter is nil!")
	}

	logger := zap.L().With(
		zap.String("component", "cache_group"),
		zap.String("group", name))

	mu.Lock()
	defer mu.Unlock()
	g := &Group{
		name:   name,
		getter: getter,
		mainCache: cache{
			cacheBytes: cacheBytes,
			factory:    factory,
		},
		loader: &singleFlight.Group{},
		logger: logger,
		stats: Stats{
			CreatedAt: time.Now(),
		},
	}
	groups[name] = g

	logger.Info("created cache group",
		zap.Int64("max_bytes", cacheBytes))

	return g
}

// RegisterPeers 将实现了PeerPicker的HTTPPool注入到Group中
func (g *Group) RegisterPeers(peers PeerPicker) {
	if g.peers != nil {
		panic("RegisterPeerPicker called more than once")
	}
	g.peers = peers
	g.logger.Info("registered peer picker")
}

func GetGroup(name string) *Group {
	mu.RLock()
	defer mu.RUnlock()
	g := groups[name]
	return g
}

func (g *Group) Get(key string) (ByteView, error) {
	if key == "" {
		return ByteView{}, fmt.Errorf("key is required")
	}

	// 增加获取次数统计
	atomic.AddInt64(&g.stats.Gets, 1)

	// 命中缓存
	if v, ok := g.mainCache.get(key); ok {
		g.logger.Debug("cache hit", zap.String("key", key))
		atomic.AddInt64(&g.stats.Hits, 1)
		return v, nil
	}

	// 未命中缓存
	g.logger.Debug("cache miss", zap.String("key", key))
	atomic.AddInt64(&g.stats.Misses, 1)
	return g.load(key)
}

func (g *Group) load(key string) (value ByteView, err error) {
	atomic.AddInt64(&g.stats.Loads, 1)

	viewi, err := g.loader.Do(key, func() (interface{}, error) {
		if g.peers != nil {
			if peer, ok := g.peers.PickPeer(key); ok {
				value, err := g.getFromPeer(peer, key)
				if err == nil {
					g.logger.Debug("loaded from peer", zap.String("key", key))
					atomic.AddInt64(&g.stats.PeerLoads, 1)
					atomic.AddInt64(&g.stats.LoadSuccess, 1)
					return value, nil
				}
				g.logger.Warn("failed to get from peer",
					zap.String("key", key),
					zap.Error(err))
				atomic.AddInt64(&g.stats.PeerErrors, 1)
			}
		}
		return g.getLocally(key)
	})

	if err == nil {
		return viewi.(ByteView), err
	}
	atomic.AddInt64(&g.stats.LoadErrors, 1)
	return
}

// getFromPeer 使用实现了PeerGetter接口的httpGetter从远程节点获取缓存值
func (g *Group) getFromPeer(peer PeerGetter, key string) (ByteView, error) {
	bytes, err := peer.Get(g.name, key)
	if err != nil {
		return ByteView{}, err
	}
	return ByteView{b: bytes}, nil
}

func (g *Group) getLocally(key string) (value ByteView, err error) {
	g.logger.Debug("loading from local getter", zap.String("key", key))
	atomic.AddInt64(&g.stats.LocalLoads, 1)

	bytes, err := g.getter.Get(key)
	if err != nil {
		g.logger.Error("failed to load from getter",
			zap.String("key", key),
			zap.Error(err))
		return ByteView{}, err
	}

	value = ByteView{b: bytes2.Clone(bytes)}
	g.populateCache(key, value)

	g.logger.Debug("loaded from local getter",
		zap.String("key", key),
		zap.Int("size", len(bytes)))

	atomic.AddInt64(&g.stats.LoadSuccess, 1)
	return value, nil
}

func (g *Group) populateCache(key string, value ByteView) {
	g.mainCache.add(key, value)
	// 更新缓存统计
	g.updateCacheStats()
}

// GetStats 获取统计信息
func (g *Group) GetStats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := g.stats
	stats.Hits = atomic.LoadInt64(&g.stats.Hits)
	stats.Misses = atomic.LoadInt64(&g.stats.Misses)
	stats.Gets = atomic.LoadInt64(&g.stats.Gets)
	stats.Loads = atomic.LoadInt64(&g.stats.Loads)
	stats.LocalLoads = atomic.LoadInt64(&g.stats.LocalLoads)
	stats.PeerLoads = atomic.LoadInt64(&g.stats.PeerLoads)
	stats.PeerErrors = atomic.LoadInt64(&g.stats.PeerErrors)
	stats.LoadSuccess = atomic.LoadInt64(&g.stats.LoadSuccess)
	stats.LoadErrors = atomic.LoadInt64(&g.stats.LoadErrors)
	stats.CacheBytes = atomic.LoadInt64(&g.stats.CacheBytes)
	stats.CacheItems = atomic.LoadInt64(&g.stats.CacheItems)

	return stats
}

// updateCacheStats 更新缓存大小和项数统计
func (g *Group) updateCacheStats() {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 从 cache 获取实际的大小和项数
	items := int64(g.mainCache.strategy.Len())

	// 计算总字节数（需要遍历缓存）
	// 注意：这是一个近似值，实际实现中可以在 cache 层维护这个值
	atomic.StoreInt64(&g.stats.CacheItems, items)

	// CacheBytes 可以通过 mainCache.cacheBytes 获取
	// 但由于 cache 结构没有暴露 nBytes，这里先用配置的 maxBytes 作为参考
	// 实际项目中建议在 cache 结构中添加 getBytesUsed() 方法
}

// Clear 清空缓存
func (g *Group) Clear() {
	g.mainCache.clear()

	// 重置部分统计信息（保留历史统计）
	atomic.StoreInt64(&g.stats.CacheBytes, 0)
	atomic.StoreInt64(&g.stats.CacheItems, 0)

	g.logger.Info("cache cleared", zap.String("group", g.name))
}

// Name 返回组名
func (g *Group) Name() string {
	return g.name
}

// CacheBytes 返回缓存大小配置
func (g *Group) CacheBytes() int64 {
	return g.mainCache.cacheBytes
}
