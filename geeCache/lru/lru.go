package lru

import "container/list"

// 这里用双向链表作为队列
// Front作为队尾 Back作为队首

type Cache struct {
	maxBytes int64      // 允许使用的最大内存
	nBytes   int64      // 当前已使用的内存
	ll       *list.List // Go语言标准库实现的双向链表
	cache    map[string]*list.Element
	// 某条记录被移除时的回调函数
	OnEvicted func(key string, value Value)
}

// 双向链表节点的数据类型
type entry struct {
	key   string
	value Value
}

type Value interface {
	Len() int
}

func (c *Cache) Len() int {
	return c.ll.Len()
}

func New(maxBytes int64, onEvicted func(key string, value Value)) *Cache {
	return &Cache{
		maxBytes:  maxBytes,
		ll:        list.New(),
		cache:     make(map[string]*list.Element),
		OnEvicted: onEvicted,
	}
}

// Get 查找:
// Step1: 从字典中找到对应的双向链表的节点
// Step2: 将该节点移动到队尾
func (c *Cache) Get(key string) (value Value, ok bool) {
	if elem, ok := c.cache[key]; ok {
		c.ll.MoveToFront(elem)
		kv := elem.Value.(*entry)
		return kv.value, true
	}
	return
}

// RemoveOldest 删除:
// Step1: 移除队首(list.Back)节点
// Step2: 从字典中删除该节点的映射关系
// Step3: 更新当前所用内存
// Step4: 如果当前回调函数不为nil, 则调用回调函数
func (c *Cache) RemoveOldest() {
	elem := c.ll.Back()
	if elem != nil {
		c.ll.Remove(elem)
		kv := elem.Value.(*entry)
		delete(c.cache, kv.key)
		c.nBytes -= int64(len(kv.key)) + int64(kv.value.Len())
		if c.OnEvicted != nil {
			c.OnEvicted(kv.key, kv.value)
		}
	}
}

// Add 新增:
// Step1.1: 如果key存在, 将节点移动到队尾
// Step1.2: 更新当前所用内存
// Step1.3: 更新cache中key对应的value
// Step2.1: 如果key不存在, 则新增节点到队尾
// Step2.2: 更新当前所用内存
// Step2.3: 新增cache中的kv对
// Step3: 如果当前内存大于最大内存的话, 移除最少访问的节点
func (c *Cache) Add(key string, value Value) {
	if elem, ok := c.cache[key]; ok {
		c.ll.MoveToFront(elem)
		kv := elem.Value.(*entry)
		c.nBytes += int64(value.Len()) - int64(kv.value.Len())
		kv.value = value
	} else {
		elem = c.ll.PushFront(&entry{
			key:   key,
			value: value,
		})
		c.nBytes += int64(len(key)) + int64(value.Len())
		c.cache[key] = elem
	}
	for c.maxBytes != 0 && c.maxBytes < c.nBytes {
		c.RemoveOldest()
	}
}
