package main

import (
	"flag"
	"fmt"
	"github.com/lyb88999/geeCache/geeCache"
	"log"
	"net/http"
)

var db = map[string]string{
	"Tom":  "630",
	"Jack": "589",
	"Sam":  "567",
}

// 根据指定的策略创建缓存组
func createGroupWithStrategy(name string, factory geeCache.CacheStrategyFactory) *geeCache.Group {
	return geeCache.NewGroupWithStrategy(name, 2<<10, geeCache.GetterFunc(
		func(key string) ([]byte, error) {
			log.Println("[SlowDB] search key", key)
			if v, ok := db[key]; ok {
				return []byte(v), nil
			}
			return nil, fmt.Errorf("%s not exist", key)
		}), factory)
}

// 启动缓存服务器
func startCacheServer(addr string, addrs []string, gee *geeCache.Group) {
	// 检查地址是否有效
	if addr == "" {
		log.Fatalf("Invalid server address")
	}

	peers := geeCache.NewHTTPPool(addr)
	peers.Set(addrs...)
	gee.RegisterPeers(peers)
	log.Println("geeCache is running at: ", addr)

	// 提取端口号，确保格式正确
	serverAddr := addr[7:] // 假设格式为 "http://localhost:xxxx"
	log.Fatal(http.ListenAndServe(serverAddr, peers))
}

// 启动API服务, 与用户进行交互
func startAPIServer(apiAddr string, gee *geeCache.Group) {
	http.Handle("/api", http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			key := r.URL.Query().Get("key")
			view, err := gee.Get(key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(view.ByteSlice())
		}))
	log.Println("fontend server is running at:", apiAddr)
	log.Fatal(http.ListenAndServe(apiAddr[7:], nil))
}

func main() {
	var port int
	var api bool
	var cacheType string
	flag.IntVar(&port, "port", 8001, "GeeCache server port")
	flag.BoolVar(&api, "api", false, "Start a api server?")
	flag.StringVar(&cacheType, "cache", "lru", "Cache type: lru, fifo, lfu")
	flag.Parse()

	apiAddr := "http://localhost:9999"
	addrMap := map[int]string{
		8001: "http://localhost:8001",
		8002: "http://localhost:8002",
		8003: "http://localhost:8003",
	}

	var addrs []string
	for _, addr := range addrMap {
		addrs = append(addrs, addr)
	}

	// 根据缓存类型选择相应的策略
	var gee *geeCache.Group
	switch cacheType {
	case "fifo":
		log.Println("Using FIFO cache strategy")
		gee = createGroupWithStrategy("scores", geeCache.NewFIFOCache)
	case "lfu":
		log.Println("Using LFU cache strategy")
		gee = createGroupWithStrategy("scores", geeCache.NewLFUCache)
	default:
		log.Println("Using LRU cache strategy")
		gee = createGroupWithStrategy("scores", geeCache.NewLRUCache)
	}

	if api {
		go startAPIServer(apiAddr, gee)
	}

	// 检查端口是否在地址映射中
	addr, ok := addrMap[port]
	if !ok {
		log.Fatalf("Error: port %d not configured in addrMap", port)
	}

	startCacheServer(addr, addrs, gee)
}
