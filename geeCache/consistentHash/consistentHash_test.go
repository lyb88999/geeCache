package consistentHash

import (
	"strconv"
	"testing"
)

func TestHashing(t *testing.T) {
	hash := New(3, func(key []byte) uint32 {
		i, _ := strconv.Atoi(string(key))
		return uint32(i)
	})
	hash.Add("6", "4", "2")
	testCases := map[string]string{
		"2":  "2",
		"11": "2",
		"23": "4",
		"27": "2",
	}
	for k, v := range testCases {
		if hash.Get(k) != v {
			t.Errorf("Asking for %s, should have yielded %s, got %s", k, v, hash.Get(k))
		}
	}
	hash.Add("8")
	testCases["27"] = "8"
	for k, v := range testCases {
		if hash.Get(k) != v {
			t.Errorf("Asking for %s, should have yielded %s, got %s", k, v, hash.Get(k))
		}
	}
}

// TestRemove 测试Remove方法
func TestRemove(t *testing.T) {
	hash := New(3, func(key []byte) uint32 {
		i, _ := strconv.Atoi(string(key))
		return uint32(i)
	})

	// 添加节点
	hash.Add("6", "4", "2")

	// 测试初始状态
	testCases := map[string]string{
		"2":  "2",
		"11": "2",
		"23": "4",
		"27": "2",
	}
	for k, v := range testCases {
		if hash.Get(k) != v {
			t.Errorf("Before remove: Asking for %s, should have yielded %s, got %s", k, v, hash.Get(k))
		}
	}

	// 移除节点 "6"
	hash.Remove("6")

	// 验证节点6已被移除
	testCases = map[string]string{
		"2":  "2",
		"11": "2",
		"23": "4",
		"27": "2",
	}
	for k, v := range testCases {
		if hash.Get(k) != v {
			t.Errorf("After removing 6: Asking for %s, should have yielded %s, got %s", k, v, hash.Get(k))
		}
	}

	// 移除节点 "4"
	hash.Remove("4")

	// 现在只剩下节点2
	testCases = map[string]string{
		"2":  "2",
		"11": "2",
		"23": "2",
		"27": "2",
	}
	for k, v := range testCases {
		if hash.Get(k) != v {
			t.Errorf("After removing 4: Asking for %s, should have yielded %s, got %s", k, v, hash.Get(k))
		}
	}

	// 移除最后一个节点
	hash.Remove("2")

	// 应该返回空字符串
	if hash.Get("2") != "" {
		t.Errorf("After removing all nodes, should return empty string, got %s", hash.Get("2"))
	}
}

// TestRemoveNonExistent 测试移除不存在的节点
func TestRemoveNonExistent(t *testing.T) {
	hash := New(3, func(key []byte) uint32 {
		i, _ := strconv.Atoi(string(key))
		return uint32(i)
	})

	hash.Add("6", "4", "2")

	// 移除不存在的节点应该不影响现有节点
	hash.Remove("999")

	testCases := map[string]string{
		"2":  "2",
		"11": "2",
		"23": "4",
		"27": "2",
	}
	for k, v := range testCases {
		if hash.Get(k) != v {
			t.Errorf("After removing non-existent node: Asking for %s, should have yielded %s, got %s", k, v, hash.Get(k))
		}
	}
}
