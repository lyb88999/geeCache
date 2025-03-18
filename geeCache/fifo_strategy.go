package geeCache

import "container/list"

// FIFOStrategy 实现了先进先出(FIFO)缓存策略
type FIFOStrategy struct {
	maxBytes  int64                            // 允许使用的最大内存
	nBytes    int64                            // 当前已使用的内存
	queue     *list.List                       // 队列，使用双向链表实现
	cache     map[string]*list.Element         // 缓存映射表
	onEvicted func(key string, value ByteView) // 某条记录被移除时的回调函数
}

// fifoEntry 表示 FIFO 缓存中的一个条目
type fifoEntry struct {
	key   string
	value ByteView
}

// NewFIFOCache 创建一个新的FIFO缓存
func NewFIFOCache(maxBytes int64, onEvicted func(key string, value ByteView)) CacheStrategy {
	return &FIFOStrategy{
		maxBytes:  maxBytes,
		queue:     list.New(),
		cache:     make(map[string]*list.Element),
		onEvicted: onEvicted,
	}
}

// Len 返回当前缓存中的项数
func (c *FIFOStrategy) Len() int {
	return c.queue.Len()
}

// Get 查找缓存项（FIFO策略中，Get操作不会改变项的位置）
func (c *FIFOStrategy) Get(key string) (value ByteView, ok bool) {
	if elem, ok := c.cache[key]; ok {
		return elem.Value.(*fifoEntry).value, true
	}
	return
}

// RemoveOldest 删除最早添加的缓存项（队列头部）
func (c *FIFOStrategy) RemoveOldest() {
	elem := c.queue.Front() // 在FIFO中，最早添加的是队列头部
	if elem != nil {
		c.queue.Remove(elem)
		entry := elem.Value.(*fifoEntry)
		delete(c.cache, entry.key)
		c.nBytes -= int64(len(entry.key)) + int64(entry.value.Len())
		if c.onEvicted != nil {
			c.onEvicted(entry.key, entry.value)
		}
	}
}

// Add 添加或更新缓存项
// 在FIFO中：
// 1. 如果键已存在，更新其值但不改变其在队列中的位置
// 2. 如果键不存在，添加到队列尾部
func (c *FIFOStrategy) Add(key string, value ByteView) {
	if elem, ok := c.cache[key]; ok {
		// 键已存在，更新值但不改变位置
		entry := elem.Value.(*fifoEntry)
		c.nBytes += int64(value.Len()) - int64(entry.value.Len())
		entry.value = value
	} else {
		// 键不存在，添加到队列尾部
		elem := c.queue.PushBack(&fifoEntry{
			key:   key,
			value: value,
		})
		c.cache[key] = elem
		c.nBytes += int64(len(key)) + int64(value.Len())
	}
	// 如果超出内存限制，移除最旧的项
	for c.maxBytes != 0 && c.maxBytes < c.nBytes {
		c.RemoveOldest()
	}
}

// 确保FIFOStrategy实现了CacheStrategy接口
var _ CacheStrategy = (*FIFOStrategy)(nil)
