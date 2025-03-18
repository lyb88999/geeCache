package geeCache

import (
	"container/list"
)

// LFUStrategy 实现了最不经常使用(LFU)缓存策略
type LFUStrategy struct {
	maxBytes  int64                            // 允许使用的最大内存
	nBytes    int64                            // 当前已使用的内存
	cache     map[string]*lfuEntry             // 缓存映射表，键到缓存项的映射
	freqList  *list.List                       // 频率列表，按频率升序排列
	onEvicted func(key string, value ByteView) // 某条记录被移除时的回调函数
}

// freqEntry 频率节点，包含一个频率值和该频率对应的所有缓存项
type freqEntry struct {
	freq  int                      // 频率值
	items map[string]*list.Element // 该频率下的所有缓存项
}

// lfuEntry LFU缓存项
type lfuEntry struct {
	key      string
	value    ByteView
	freq     int           // 当前频率
	freqNode *list.Element // 指向频率节点的指针
}

// NewLFUCache 创建一个新的LFU缓存
func NewLFUCache(maxBytes int64, onEvicted func(key string, value ByteView)) CacheStrategy {
	return &LFUStrategy{
		maxBytes:  maxBytes,
		cache:     make(map[string]*lfuEntry),
		freqList:  list.New(),
		onEvicted: onEvicted,
	}
}

// Len 返回当前缓存中的项数
func (c *LFUStrategy) Len() int {
	return len(c.cache)
}

// Get 查找缓存项，并增加其访问频率
func (c *LFUStrategy) Get(key string) (value ByteView, ok bool) {
	if entry, ok := c.cache[key]; ok {
		c.incrementFreq(entry)
		return entry.value, true
	}
	return ByteView{}, false
}

// incrementFreq 增加缓存项的访问频率
func (c *LFUStrategy) incrementFreq(entry *lfuEntry) {
	// 获取当前频率节点
	freqNode := entry.freqNode
	currentFreq := entry.freq
	newFreq := currentFreq + 1

	// 从当前频率节点的items映射中移除该项
	if freqNode != nil {
		fe := freqNode.Value.(*freqEntry)
		delete(fe.items, entry.key)

		// 如果当前频率节点没有其他项，移除它
		if len(fe.items) == 0 {
			c.freqList.Remove(freqNode)
		}
	}

	// 寻找或创建新频率节点
	var newFreqNode *list.Element
	// 如果freqNode为nil或者是最后一个节点，或者下一个节点的频率大于newFreq
	if freqNode == nil {
		// 从头开始查找
		for e := c.freqList.Front(); e != nil; e = e.Next() {
			if e.Value.(*freqEntry).freq == newFreq {
				newFreqNode = e
				break
			} else if e.Value.(*freqEntry).freq > newFreq {
				// 当找到比newFreq大的频率节点时，在之前插入新节点
				newFreqEntry := &freqEntry{
					freq:  newFreq,
					items: make(map[string]*list.Element),
				}
				newFreqNode = c.freqList.InsertBefore(newFreqEntry, e)
				break
			}
		}
	} else {
		// 从当前频率节点开始向后查找
		next := freqNode.Next()
		if next == nil || next.Value.(*freqEntry).freq > newFreq {
			newFreqEntry := &freqEntry{
				freq:  newFreq,
				items: make(map[string]*list.Element),
			}
			if next == nil {
				newFreqNode = c.freqList.PushBack(newFreqEntry)
			} else {
				newFreqNode = c.freqList.InsertBefore(newFreqEntry, next)
			}
		} else if next.Value.(*freqEntry).freq == newFreq {
			newFreqNode = next
		}
	}

	// 如果没有找到适合的频率节点，创建一个新的
	if newFreqNode == nil {
		newFreqEntry := &freqEntry{
			freq:  newFreq,
			items: make(map[string]*list.Element),
		}
		newFreqNode = c.freqList.PushBack(newFreqEntry)
	}

	// 更新缓存项的频率和频率节点
	entry.freq = newFreq
	entry.freqNode = newFreqNode
	newFreqNode.Value.(*freqEntry).items[entry.key] = nil
}

// RemoveOldest 删除最不经常使用的缓存项
func (c *LFUStrategy) RemoveOldest() {
	if c.freqList.Len() == 0 {
		return
	}

	// 获取最低频率节点
	minFreqNode := c.freqList.Front()
	if minFreqNode == nil {
		return
	}

	// 从最低频率节点中选择一个移除
	fe := minFreqNode.Value.(*freqEntry)
	var oldestKey string
	for k := range fe.items {
		oldestKey = k
		break
	}

	if oldestKey != "" {
		// 从缓存和频率节点中移除
		entry := c.cache[oldestKey]
		delete(c.cache, oldestKey)
		delete(fe.items, oldestKey)

		// 如果频率节点为空，移除它
		if len(fe.items) == 0 {
			c.freqList.Remove(minFreqNode)
		}

		// 更新已用内存
		c.nBytes -= int64(len(oldestKey)) + int64(entry.value.Len())

		// 调用回调函数
		if c.onEvicted != nil {
			c.onEvicted(oldestKey, entry.value)
		}
	}
}

// Add 添加或更新缓存项
func (c *LFUStrategy) Add(key string, value ByteView) {
	if entry, ok := c.cache[key]; ok {
		// 更新值
		c.nBytes = c.nBytes - int64(entry.value.Len()) + int64(value.Len())
		entry.value = value
		// 增加频率
		c.incrementFreq(entry)
	} else {
		// 新建缓存项
		entry := &lfuEntry{
			key:   key,
			value: value,
			freq:  0, // 初始频率为0
		}
		c.cache[key] = entry
		c.nBytes += int64(len(key)) + int64(value.Len())

		// 增加频率（初始为1）
		c.incrementFreq(entry)
	}

	// 如果超过内存限制，移除最不常用的项
	for c.maxBytes != 0 && c.nBytes > c.maxBytes {
		c.RemoveOldest()
	}
}

// 确保LFUStrategy实现了CacheStrategy接口
var _ CacheStrategy = (*LFUStrategy)(nil)
