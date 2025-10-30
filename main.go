package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/lyb88999/geeCache/admin"
	"github.com/lyb88999/geeCache/circuitbreaker"
	"github.com/lyb88999/geeCache/config"
	"github.com/lyb88999/geeCache/geeCache"
	"github.com/lyb88999/geeCache/health"
	"github.com/lyb88999/geeCache/logger"
	"github.com/lyb88999/geeCache/metrics"
	"github.com/lyb88999/geeCache/ratelimit"
	"github.com/lyb88999/geeCache/registry"
	"github.com/lyb88999/geeCache/retry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
		zap.String("version", "2.0.0"),
		zap.String("instance_id", cfg.Server.InstanceID))

	// 初始化 Prometheus metrics
	var metricsCollector *metrics.Metrics
	if cfg.Metrics.Enabled {
		metricsCollector = metrics.Init(cfg.Metrics.Namespace)
		log.Info("metrics initialized", zap.String("namespace", cfg.Metrics.Namespace))

		// 启动系统指标更新
		go updateSystemMetrics(metricsCollector)
	}

	// 如果没有指定instanceID，使用默认值
	if cfg.Server.InstanceID == "" {
		cfg.Server.InstanceID = fmt.Sprintf("node%d", cfg.Server.Port)
	}

	// 构建当前节点地址
	addr := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)

	// 初始化重试器
	var retryer *retry.Retryer
	if cfg.Retry.Enabled {
		retryer = retry.NewRetryer(&retry.Config{
			MaxRetries:     cfg.Retry.MaxRetries,
			InitialBackoff: time.Duration(cfg.Retry.InitialBackoff) * time.Millisecond,
			MaxBackoff:     time.Duration(cfg.Retry.MaxBackoff) * time.Millisecond,
			Multiplier:     cfg.Retry.Multiplier,
		}, log)
		log.Info("retry enabled", zap.Int("max_retries", cfg.Retry.MaxRetries))
	}

	// 初始化熔断器
	var breaker *circuitbreaker.CircuitBreaker
	if cfg.CircuitBreaker.Enabled {
		breaker = circuitbreaker.NewCircuitBreaker(&circuitbreaker.Config{
			MaxRequests:      cfg.CircuitBreaker.MaxRequests,
			Interval:         time.Duration(cfg.CircuitBreaker.Interval) * time.Second,
			Timeout:          time.Duration(cfg.CircuitBreaker.Timeout) * time.Second,
			FailureThreshold: cfg.CircuitBreaker.FailureThreshold,
			MinimumRequests:  cfg.CircuitBreaker.MinimumRequests,
			OnStateChange: func(from, to circuitbreaker.State) {
				log.Warn("circuit breaker state changed",
					zap.String("from", from.String()),
					zap.String("to", to.String()))
			},
		}, log)
		log.Info("circuit breaker enabled",
			zap.Float64("failure_threshold", cfg.CircuitBreaker.FailureThreshold))
	}

	// 初始化限流器
	var limiter ratelimit.Limiter
	if cfg.RateLimit.Enabled {
		limiter = ratelimit.NewTokenBucketLimiter(&ratelimit.Config{
			Rate:  cfg.RateLimit.Rate,
			Burst: cfg.RateLimit.Burst,
		}, log)
		log.Info("rate limiter enabled",
			zap.Int("rate", cfg.RateLimit.Rate),
			zap.Int("burst", cfg.RateLimit.Burst))
	}

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

	// 创建缓存组（带重试和熔断）
	gee := geeCache.NewGroupWithStrategy(cfg.Cache.GroupName, cfg.Cache.MaxBytes,
		geeCache.GetterFunc(func(key string) ([]byte, error) {
			start := time.Now()
			log.Info("searching in slow db", zap.String("key", key))

			// 应用限流
			if limiter != nil {
				if !limiter.Allow() {
					log.Warn("rate limit exceeded", zap.String("key", key))
					if metricsCollector != nil {
						metricsCollector.RecordRequest(cfg.Cache.GroupName, "rate_limited", time.Since(start))
					}
					return nil, ratelimit.ErrRateLimitExceeded
				}
			}

			// 数据库查询逻辑
			dbQuery := func() error {
				// 模拟数据库查询
				if _, ok := db[key]; !ok {
					return fmt.Errorf("%s not exist", key)
				}
				return nil
			}

			var queryErr error

			// 应用熔断器
			if breaker != nil {
				queryErr = breaker.Execute(dbQuery)
			} else {
				queryErr = dbQuery()
			}

			if queryErr != nil {
				if metricsCollector != nil {
					metricsCollector.RecordRequest(cfg.Cache.GroupName, "error", time.Since(start))
				}
				return nil, queryErr
			}

			// 记录成功的指标
			if metricsCollector != nil {
				metricsCollector.RecordRequest(cfg.Cache.GroupName, "success", time.Since(start))
			}

			return []byte(db[key]), nil
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
		_, err := gee.Get("Tom")
		return err
	}))

	healthChecker.RegisterCheck(health.NewSimpleCheck("registry", func(ctx context.Context) error {
		_, err := reg.Discover(ctx, cfg.Registry.ServiceName)
		return err
	}))

	if breaker != nil {
		healthChecker.RegisterCheck(health.NewSimpleCheck("circuit_breaker", func(ctx context.Context) error {
			state := breaker.State()
			if state == circuitbreaker.StateOpen {
				return fmt.Errorf("circuit breaker is open")
			}
			return nil
		}))
	}

	// 设置元数据
	healthChecker.SetMetadata("version", "2.0.0")
	healthChecker.SetMetadata("instance_id", cfg.Server.InstanceID)
	healthChecker.SetMetadata("cache_strategy", cfg.Cache.Strategy)
	healthChecker.SetMetadata("features", map[string]bool{
		"metrics":         cfg.Metrics.Enabled,
		"circuit_breaker": cfg.CircuitBreaker.Enabled,
		"retry":           cfg.Retry.Enabled,
		"rate_limit":      cfg.RateLimit.Enabled,
		"admin_api":       cfg.Admin.Enabled,
	})

	// 创建管理 API
	var adminAPI *admin.API
	if cfg.Admin.Enabled {
		adminAPI = admin.NewAPI(log)
		adminAPI.RegisterGroup(cfg.Cache.GroupName, gee)
		log.Info("admin API enabled", zap.String("endpoint", cfg.Admin.Endpoint))
	}

	// 创建HTTP服务器
	mux := http.NewServeMux()

	// 注册缓存服务处理器
	mux.Handle(geeCache.DefaultBasePath, peers)

	// 注册健康检查端点
	if cfg.Health.Enabled {
		mux.HandleFunc(cfg.Health.Endpoint, healthChecker.Handler())
		log.Info("health check enabled", zap.String("endpoint", cfg.Health.Endpoint))
	}

	// 注册 Prometheus metrics 端点
	if cfg.Metrics.Enabled {
		mux.Handle(cfg.Metrics.Endpoint, promhttp.Handler())
		log.Info("metrics enabled", zap.String("endpoint", cfg.Metrics.Endpoint))
	}

	// 注册管理 API 端点
	if cfg.Admin.Enabled && adminAPI != nil {
		adminAPI.RegisterHandlers(mux, cfg.Admin.Endpoint)
	}

	// API服务器
	var apiServer *http.Server
	if cfg.Server.EnableAPI {
		apiMux := http.NewServeMux()
		apiMux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			key := r.URL.Query().Get("key")
			log.Info("api request", zap.String("key", key))

			view, err := gee.Get(key)
			duration := time.Since(start)

			if err != nil {
				log.Error("failed to get value", zap.String("key", key), zap.Error(err))
				if metricsCollector != nil {
					metricsCollector.RecordRequest(cfg.Cache.GroupName, "error", duration)
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if metricsCollector != nil {
				metricsCollector.RecordRequest(cfg.Cache.GroupName, "success", duration)
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
		zap.Int("port", cfg.Server.Port),
		zap.String("health", cfg.Health.Endpoint),
		zap.String("metrics", cfg.Metrics.Endpoint),
		zap.String("admin", cfg.Admin.Endpoint))

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

	log.Info("server exited cleanly")
}

// updateSystemMetrics 定期更新系统指标
func updateSystemMetrics(m *metrics.Metrics) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.UpdateSystemMetrics(runtime.NumGoroutine())
	}
}
