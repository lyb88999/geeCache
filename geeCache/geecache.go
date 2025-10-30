package geeCache

import (
	bytes2 "bytes"
	"fmt"
	"github.com/lyb88999/geeCache/singleFlight"
	"go.uber.org/zap"
	"sync"
)

type Getter interface {
	Get(key string) ([]byte, error)
}

// GetterFunc 接口型函数
type GetterFunc func(key string) ([]byte, error)

func (f GetterFunc) Get(key string) ([]byte, error) {
	return f(key)
}

// Group 一个Group可以认为是一个缓存的命名空间
type Group struct {
	name      string              // 每个Group拥有一个唯一的名称name
	getter    Getter              // 缓存未命中时获取数据的回调
	mainCache cache               // 实现的并发缓存
	peers     PeerPicker          // 分布式节点选择
	loader    *singleFlight.Group // 防止缓存击穿
	logger    *zap.Logger         // 日志记录器
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

	// 命中缓存
	if v, ok := g.mainCache.get(key); ok {
		g.logger.Debug("cache hit", zap.String("key", key))
		return v, nil
	}

	// 未命中缓存
	g.logger.Debug("cache miss", zap.String("key", key))
	return g.load(key)
}

func (g *Group) load(key string) (value ByteView, err error) {
	viewi, err := g.loader.Do(key, func() (interface{}, error) {
		if g.peers != nil {
			if peer, ok := g.peers.PickPeer(key); ok {
				value, err := g.getFromPeer(peer, key)
				if err == nil {
					g.logger.Debug("loaded from peer", zap.String("key", key))
					return value, nil
				}
				g.logger.Warn("failed to get from peer",
					zap.String("key", key),
					zap.Error(err))
			}
		}
		return g.getLocally(key)
	})

	if err == nil {
		return viewi.(ByteView), err
	}
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

	return value, nil
}

func (g *Group) populateCache(key string, value ByteView) {
	g.mainCache.add(key, value)
}
