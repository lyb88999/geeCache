package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/lyb88999/geeCache/geeCache"
	"github.com/lyb88999/geeCache/registry"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
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

// 启动缓存服务器（使用注册中心）
func startCacheServer(addr, instanceID string, gee *geeCache.Group, etcdEndpoints []string) (*geeCache.HTTPPool, error) {
	// 1. 创建HTTPPool
	peers := geeCache.NewHTTPPool(addr)

	// 2. 创建注册中心
	config := &registry.Config{
		Endpoints:   etcdEndpoints,
		DialTimeout: 5 * time.Second,
	}
	reg, err := registry.NewEtcdRegistry(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create registry: %w", err)
	}

	// 3. 设置注册中心
	peers.SetRegistry(reg, "geeCache", instanceID)

	// 4. 启动并注册
	err = peers.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start HTTPPool: %w", err)
	}

	// 5. 注册到Group
	gee.RegisterPeers(peers)

	log.Printf("[GeeCache] server is running at %s (instance: %s)", addr, instanceID)
	return peers, nil
}

// 启动API服务, 与用户进行交互
func startAPIServer(apiAddr string, gee *geeCache.Group) *http.Server {
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

	server := &http.Server{
		Addr:    apiAddr,
		Handler: nil,
	}

	go func() {
		log.Println("[API] server is running at:", apiAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[API] server error: %v", err)
		}
	}()

	return server
}

func main() {
	var port int
	var api bool
	var cacheType string
	var instanceID string
	var etcdEndpoints string

	flag.IntVar(&port, "port", 8001, "GeeCache server port")
	flag.BoolVar(&api, "api", false, "Start a api server?")
	flag.StringVar(&cacheType, "cache", "lru", "Cache type: lru, fifo, lfu")
	flag.StringVar(&instanceID, "id", "", "Instance ID (default: node{port})")
	flag.StringVar(&etcdEndpoints, "etcd", "localhost:2379", "Etcd endpoints, comma separated")
	flag.Parse()

	// 如果没有指定instanceID，使用默认值
	if instanceID == "" {
		instanceID = fmt.Sprintf("node%d", port)
	}

	// 解析etcd地址
	etcdAddrs := []string{etcdEndpoints}
	// 如果包含逗号，分割
	// etcdAddrs = strings.Split(etcdEndpoints, ",")

	// 构建当前节点地址
	addr := fmt.Sprintf("http://localhost:%d", port)
	apiAddr := ":9999"

	// 根据缓存类型选择相应的策略
	var gee *geeCache.Group
	switch cacheType {
	case "fifo":
		log.Println("[Cache] Using FIFO cache strategy")
		gee = createGroupWithStrategy("scores", geeCache.NewFIFOCache)
	case "lfu":
		log.Println("[Cache] Using LFU cache strategy")
		gee = createGroupWithStrategy("scores", geeCache.NewLFUCache)
	default:
		log.Println("[Cache] Using LRU cache strategy")
		gee = createGroupWithStrategy("scores", geeCache.NewLRUCache)
	}

	// 启动缓存服务器
	peers, err := startCacheServer(addr, instanceID, gee, etcdAddrs)
	if err != nil {
		log.Fatalf("[Error] Failed to start cache server: %v", err)
	}

	// 启动API服务器
	var apiServer *http.Server
	if api {
		apiServer = startAPIServer(apiAddr, gee)
	}

	// 启动HTTP服务器
	go func() {
		err := http.ListenAndServe(fmt.Sprintf(":%d", port), peers)
		if err != nil {
			log.Fatalf("[Error] HTTP server error: %v", err)
		}
	}()

	// 等待中断信号以优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Shutdown] Shutting down gracefully...")

	// 关闭HTTPPool（会自动从注册中心注销）
	if err := peers.Stop(); err != nil {
		log.Printf("[Error] Failed to stop HTTPPool: %v", err)
	}

	// 关闭API服务器
	if apiServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := apiServer.Shutdown(ctx); err != nil {
			log.Printf("[Error] Failed to shutdown API server: %v", err)
		}
	}

	log.Println("[Shutdown] Server exited")
}
