package consistentHash

import (
	"hash/crc32"
	"sort"
	"strconv"
)

type Hash func(data []byte) uint32

type Map struct {
	hash     Hash           // Hash函数
	replicas int            // 虚拟节点倍数
	keys     []int          // 哈希环
	hashMap  map[int]string // 虚拟节点与真实节点的映射表
}

func New(replicas int, fn Hash) *Map {
	m := &Map{
		hash:     fn,
		replicas: replicas,
		hashMap:  make(map[int]string),
	}
	if m.hash == nil {
		m.hash = crc32.ChecksumIEEE
	}
	return m
}

// Add 添加真实节点或机器的Add方法
func (m *Map) Add(keys ...string) {
	for _, key := range keys {
		// 对每一个真实节点创建m.replicas个虚拟节点，虚拟节点的名称是: strconv.Itoa(i) + key, 即通过添加编号的方式区分不同虚拟节点
		for i := 0; i < m.replicas; i++ {
			// 使用m.hash计算虚拟节点的hash值, 并添加到环上
			hash := int(m.hash([]byte(strconv.Itoa(i) + key)))
			m.keys = append(m.keys, hash)
			// 在hashMap中增加虚拟节点和真实节点的映射关系
			m.hashMap[hash] = key
		}
	}
	// 环上的hash值排序
	sort.Ints(m.keys)
}

func (m *Map) Remove(key string) {
	if key == "" {
		return
	}

	// 1. 收集需要删除的虚拟节点hash值
	toRemove := make(map[int]bool)
	for i := 0; i < m.replicas; i++ {
		hash := int(m.hash([]byte(strconv.Itoa(i) + key)))
		toRemove[hash] = true
		delete(m.hashMap, hash)
	}

	// 2. 从keys切片中移除这些值
	newKeys := make([]int, 0, len(m.keys))
	for _, k := range m.keys {
		if !toRemove[k] {
			newKeys = append(newKeys, k)
		}
	}
	m.keys = newKeys
}

// Get 选择节点的Get方法
func (m *Map) Get(key string) string {
	if len(m.keys) == 0 {
		return ""
	}
	// 计算key的hash值
	hash := int(m.hash([]byte(key)))
	// 顺时针找到第一个匹配的虚拟节点下标
	idx := sort.Search(len(m.keys), func(i int) bool {
		return m.keys[i] >= hash
	})
	// 从m.keys中获取对应的hash值
	// 如果idx == len(m.keys) 说明应该选择m.keys[0]
	// 通过hashMap映射得到真实的节点
	return m.hashMap[m.keys[idx%len(m.keys)]]
}
