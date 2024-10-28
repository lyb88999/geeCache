package geeCache

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"log"
	"testing"
)

func TestGetter(t *testing.T) {
	var g Getter = GetterFunc(func(key string) ([]byte, error) {
		return []byte(key), nil
	})
	expect := []byte("key")
	v, err := g.Get("key")
	require.NoError(t, err)
	require.Equal(t, expect, v)
}

var db = map[string]string{
	"Tom":  "630",
	"Jack": "589",
	"Sam":  "567",
}

func TestGet(t *testing.T) {
	loadCounts := make(map[string]int, len(db))
	gee := NewGroup("scores", 2<<10, GetterFunc(
		func(key string) ([]byte, error) {
			log.Println("[SlowDB] Search key", key)
			if v, ok := db[key]; ok {
				//if _, ok := loadCounts[key]; !ok {
				//	loadCounts[key] = 0
				//}
				loadCounts[key] += 1
				return []byte(v), nil
			}
			return nil, fmt.Errorf("%s not exist", key)
		}))
	for k, v := range db {
		// 测试在缓存为空的情况下, 能够通过回调函数获取到源数据
		view, err := gee.Get(k)
		require.NoError(t, err)
		require.Equal(t, view.String(), v)
		// 测试在缓存已经存在情况下, 是否直接从缓存中获取
		// 使用loadCounts统计某个键调用回调函数的次数, 如果次数大于1, 表示多次调用了回调函数, 没有缓存
		view, err = gee.Get(k)
		require.NoError(t, err)
		require.Equal(t, view.String(), v)
		require.Equal(t, loadCounts[k], 1)
	}

	view, err := gee.Get("Unknown")
	require.Error(t, err)
	require.Empty(t, view)
}
