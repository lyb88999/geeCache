package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lyb88999/geeCache/config"
	"github.com/lyb88999/geeCache/geeCache"
	"github.com/lyb88999/geeCache/health"
	"github.com/lyb88999/geeCache/logger"
	"github.com/lyb88999/geeCache/registry"
	"go.uber.org/zap"
)

var db = map[string]string{
	"Tom":  "630",
	"Jack": "589",
	"Sam":  "567",
}

func main() {
	// 解析命令行参数
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to config file")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志系统
	if err := logger.Init(logger.Config{
		Level:      cfg.Log.Level,
		Format:     cfg.Log.Format,
		OutputPath: cfg.Log.OutputPath,
	}); err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	log := logger.GetLogger()
	log.Info("starting GeeCache",
		zap.String("version", "1.0.0"),
		zap.Any("config", cfg))

	// 如果没有指定instanceID，使用默认值
	if cfg.Server.InstanceID == "" {
		cfg.Server.InstanceID = fmt.Sprintf("node%d", cfg.Server.Port)
	}

	// 构建当前节点地址
	addr := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)

	// 根据缓存类型选择相应的策略
	var strategyFactory geeCache.CacheStrategyFactory
	switch cfg.Cache.Strategy {
	case "fifo":
		log.Info("using FIFO cache strategy")
		strategyFactory = geeCache.NewFIFOCache
	case "lfu":
		log.Info("using LFU cache strategy")
		strategyFactory = geeCache.NewLFUCache
	default:
		log.Info("using LRU cache strategy")
		strategyFactory = geeCache.NewLRUCache
	}

	// 创建缓存组
	gee := geeCache.NewGroupWithStrategy(cfg.Cache.GroupName, cfg.Cache.MaxBytes, geeCache.GetterFunc(
		func(key string) ([]byte, error) {
			log.Info("searching in slow db", zap.String("key", key))
			if v, ok := db[key]; ok {
				return []byte(v), nil
			}
			return nil, fmt.Errorf("%s not exist", key)
		}), strategyFactory)

	// 创建HTTP连接池配置
	poolConfig := &geeCache.HTTPPoolConfig{
		Replicas:        cfg.Cache.Replicas,
		Timeout:         cfg.Cache.PeerTimeout,
		MaxIdleConns:    cfg.Cache.MaxIdleConns,
		IdleConnTimeout: 90 * time.Second,
	}

	// 创建注册中心
	regConfig := &registry.Config{
		Endpoints:   cfg.Registry.Endpoints,
		DialTimeout: cfg.Registry.DialTimeout,
		Username:    cfg.Registry.Username,
		Password:    cfg.Registry.Password,
	}
	reg, err := registry.NewEtcdRegistry(regConfig)
	if err != nil {
		log.Fatal("failed to create registry", zap.Error(err))
	}

	// 创建HTTP连接池并启动
	peers := geeCache.NewHTTPPoolWithConfig(addr, poolConfig)
	peers.SetRegistry(reg, cfg.Registry.ServiceName, cfg.Server.InstanceID)

	if err := peers.Start(); err != nil {
		log.Fatal("failed to start HTTPPool", zap.Error(err))
	}

	// 注册到缓存组
	gee.RegisterPeers(peers)

	// 创建健康检查器
	healthChecker := health.NewChecker(log)

	// 注册健康检查
	healthChecker.RegisterCheck(health.NewSimpleCheck("cache", func(ctx context.Context) error {
		// 简单检查：尝试获取一个键
		_, err := gee.Get("Tom")
		return err
	}))

	healthChecker.RegisterCheck(health.NewSimpleCheck("registry", func(ctx context.Context) error {
		// 检查注册中心连接
		_, err := reg.Discover(ctx, cfg.Registry.ServiceName)
		return err
	}))

	// 设置元数据
	healthChecker.SetMetadata("version", "1.0.0")
	healthChecker.SetMetadata("instance_id", cfg.Server.InstanceID)
	healthChecker.SetMetadata("cache_strategy", cfg.Cache.Strategy)

	// 创建HTTP服务器
	mux := http.NewServeMux()

	// 注册缓存服务处理器
	mux.Handle(geeCache.DefaultBasePath, peers)

	// 注册健康检查端点
	if cfg.Health.Enabled {
		mux.HandleFunc(cfg.Health.Endpoint, healthChecker.Handler())
		log.Info("health check enabled", zap.String("endpoint", cfg.Health.Endpoint))
	}

	// API服务器
	var apiServer *http.Server
	if cfg.Server.EnableAPI {
		apiMux := http.NewServeMux()
		apiMux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
			key := r.URL.Query().Get("key")
			log.Info("api request", zap.String("key", key))

			view, err := gee.Get(key)
			if err != nil {
				log.Error("failed to get value", zap.String("key", key), zap.Error(err))
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(view.ByteSlice())
		})

		apiServer = &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Server.APIPort),
			Handler:      apiMux,
			ReadTimeout:  cfg.Server.ReadTimeout,
			WriteTimeout: cfg.Server.WriteTimeout,
		}

		go func() {
			log.Info("API server starting", zap.Int("port", cfg.Server.APIPort))
			if err := apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatal("API server error", zap.Error(err))
			}
		}()
	}

	// 启动缓存服务器
	cacheServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	go func() {
		log.Info("cache server starting", zap.Int("port", cfg.Server.Port))
		if err := cacheServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("cache server error", zap.Error(err))
		}
	}()

	log.Info("GeeCache started successfully",
		zap.String("instance_id", cfg.Server.InstanceID),
		zap.Int("port", cfg.Server.Port))

	// 等待中断信号以优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down gracefully...")

	// 创建关闭上下文
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// 关闭HTTPPool（会自动从注册中心注销）
	if err := peers.Stop(); err != nil {
		log.Error("failed to stop HTTPPool", zap.Error(err))
	}

	// 关闭缓存服务器
	if err := cacheServer.Shutdown(shutdownCtx); err != nil {
		log.Error("failed to shutdown cache server", zap.Error(err))
	}

	// 关闭API服务器
	if apiServer != nil {
		if err := apiServer.Shutdown(shutdownCtx); err != nil {
			log.Error("failed to shutdown API server", zap.Error(err))
		}
	}

	// 关闭注册中心连接
	if err := reg.Close(); err != nil {
		log.Error("failed to close registry", zap.Error(err))
	}

	log.Info("server exited")
}
