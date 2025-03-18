package geeCache

// Value 接口已经在 ByteView 中定义，这里我们直接引用它

// CacheStrategy 缓存策略接口
type CacheStrategy interface {
	// Add 添加或更新一个缓存项
	Add(key string, value ByteView)

	// Get 获取一个缓存项，如果存在则返回缓存值和true，否则返回空值和false
	Get(key string) (value ByteView, ok bool)

	// RemoveOldest 从缓存中移除"最旧"的项
	// 不同的策略有不同的"最旧"定义：
	// - LRU: 最近最少使用
	// - LFU: 最不经常使用
	// - FIFO: 先进先出
	RemoveOldest()

	// Len 返回当前缓存中的项数
	Len() int
}

// CacheStrategyFactory 工厂函数类型，用于创建不同的缓存策略
type CacheStrategyFactory func(maxBytes int64, onEvicted func(key string, value ByteView)) CacheStrategy
