package lru

import (
	"github.com/stretchr/testify/require"
	"testing"
)

type String string

func (d String) Len() int {
	return len(d)
}

func TestGet(t *testing.T) {
	lru := New(int64(0), nil)
	lru.Add("key1", String("1234"))
	v, ok := lru.Get("key1")
	str, okS := v.(String)
	require.Equal(t, ok, true)
	require.Equal(t, okS, true)
	require.Equal(t, str, String("1234"))
	_, ok = lru.Get("k2")
	require.Equal(t, ok, false)
}

func TestRemoveOldest(t *testing.T) {
	k1, k2, k3 := "key1", "key2", "k3"
	v1, v2, v3 := "value1", "value2", "v3"
	cap := len(k1 + k2 + v1 + v2)
	lru := New(int64(cap), nil)
	lru.Add(k1, String(v1))
	lru.Add(k2, String(v2))
	lru.Add(k3, String(v3))
	_, ok := lru.Get(k1)
	require.Equal(t, ok, false)
	require.Equal(t, lru.Len(), 2)
}

func TestOnEvicted(t *testing.T) {
	keys := make([]string, 0)
	callback := func(key string, value Value) {
		keys = append(keys, key)
	}
	lru := New(int64(10), callback)
	lru.Add("key1", String("123456"))
	lru.Add("k2", String("k2"))
	lru.Add("k3", String("k3"))
	lru.Add("k4", String("k4"))
	expect := []string{"key1", "k2"}
	require.Equal(t, expect, keys)
}
