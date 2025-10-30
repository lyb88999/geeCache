package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

var (
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
)

// Limiter 限流器接口
type Limiter interface {
	Allow() bool
	Wait(ctx context.Context) error
}

// Config 限流配置
type Config struct {
	Rate  int // 每秒允许的请求数
	Burst int // 突发请求数
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
	return &Config{
		Rate:  1000, // 每秒1000个请求
		Burst: 1500, // 允许突发1500个请求
	}
}

// TokenBucketLimiter 令牌桶限流器
type TokenBucketLimiter struct {
	limiter *rate.Limiter
	config  *Config
	logger  *zap.Logger
}

// NewTokenBucketLimiter 创建令牌桶限流器
func NewTokenBucketLimiter(config *Config, logger *zap.Logger) *TokenBucketLimiter {
	if config == nil {
		config = DefaultConfig()
	}
	if logger == nil {
		logger = zap.L()
	}

	return &TokenBucketLimiter{
		limiter: rate.NewLimiter(rate.Limit(config.Rate), config.Burst),
		config:  config,
		logger:  logger.With(zap.String("component", "rate_limiter")),
	}
}

// Allow 检查是否允许请求（不等待）
func (l *TokenBucketLimiter) Allow() bool {
	allowed := l.limiter.Allow()
	if !allowed {
		l.logger.Warn("rate limit exceeded")
	}
	return allowed
}

// Wait 等待直到允许请求
func (l *TokenBucketLimiter) Wait(ctx context.Context) error {
	err := l.limiter.Wait(ctx)
	if err != nil {
		l.logger.Error("wait for rate limit failed", zap.Error(err))
	}
	return err
}

// AllowN 检查是否允许 n 个请求
func (l *TokenBucketLimiter) AllowN(n int) bool {
	return l.limiter.AllowN(time.Now(), n)
}

// WaitN 等待 n 个令牌
func (l *TokenBucketLimiter) WaitN(ctx context.Context, n int) error {
	return l.limiter.WaitN(ctx, n)
}

// PerKeyLimiter 按key限流器（例如按IP、用户ID限流）
type PerKeyLimiter struct {
	limiters sync.Map // key -> *TokenBucketLimiter
	config   *Config
	logger   *zap.Logger

	// 清理过期限流器
	cleanupInterval time.Duration
	lastAccess      sync.Map // key -> time.Time
	ttl             time.Duration
}

// NewPerKeyLimiter 创建按key的限流器
func NewPerKeyLimiter(config *Config, logger *zap.Logger) *PerKeyLimiter {
	if config == nil {
		config = DefaultConfig()
	}
	if logger == nil {
		logger = zap.L()
	}

	pkl := &PerKeyLimiter{
		config:          config,
		logger:          logger.With(zap.String("component", "per_key_limiter")),
		cleanupInterval: 5 * time.Minute,
		ttl:             10 * time.Minute,
	}

	// 启动清理goroutine
	go pkl.cleanup()

	return pkl
}

// GetLimiter 获取指定key的限流器
func (pkl *PerKeyLimiter) GetLimiter(key string) *TokenBucketLimiter {
	// 更新访问时间
	pkl.lastAccess.Store(key, time.Now())

	// 获取或创建限流器
	if limiter, ok := pkl.limiters.Load(key); ok {
		return limiter.(*TokenBucketLimiter)
	}

	// 创建新的限流器
	newLimiter := NewTokenBucketLimiter(pkl.config, pkl.logger.With(zap.String("key", key)))
	actual, _ := pkl.limiters.LoadOrStore(key, newLimiter)
	return actual.(*TokenBucketLimiter)
}

// Allow 检查指定key是否允许请求
func (pkl *PerKeyLimiter) Allow(key string) bool {
	return pkl.GetLimiter(key).Allow()
}

// Wait 等待指定key的限流
func (pkl *PerKeyLimiter) Wait(ctx context.Context, key string) error {
	return pkl.GetLimiter(key).Wait(ctx)
}

// cleanup 定期清理过期的限流器
func (pkl *PerKeyLimiter) cleanup() {
	ticker := time.NewTicker(pkl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		pkl.lastAccess.Range(func(key, value interface{}) bool {
			lastTime := value.(time.Time)
			if now.Sub(lastTime) > pkl.ttl {
				pkl.limiters.Delete(key)
				pkl.lastAccess.Delete(key)
				pkl.logger.Debug("cleaned up limiter", zap.String("key", key.(string)))
			}
			return true
		})
	}
}

// SlidingWindowLimiter 滑动窗口限流器
type SlidingWindowLimiter struct {
	mu       sync.Mutex
	limit    int           // 时间窗口内的最大请求数
	window   time.Duration // 时间窗口大小
	requests []time.Time   // 请求时间戳
	logger   *zap.Logger
}

// NewSlidingWindowLimiter 创建滑动窗口限流器
func NewSlidingWindowLimiter(limit int, window time.Duration, logger *zap.Logger) *SlidingWindowLimiter {
	if logger == nil {
		logger = zap.L()
	}

	return &SlidingWindowLimiter{
		limit:    limit,
		window:   window,
		requests: make([]time.Time, 0),
		logger:   logger.With(zap.String("component", "sliding_window_limiter")),
	}
}

// Allow 检查是否允许请求
func (l *SlidingWindowLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-l.window)

	// 移除窗口外的请求
	validRequests := make([]time.Time, 0)
	for _, reqTime := range l.requests {
		if reqTime.After(windowStart) {
			validRequests = append(validRequests, reqTime)
		}
	}
	l.requests = validRequests

	// 检查是否超过限制
	if len(l.requests) >= l.limit {
		l.logger.Warn("sliding window rate limit exceeded",
			zap.Int("current", len(l.requests)),
			zap.Int("limit", l.limit))
		return false
	}

	// 记录本次请求
	l.requests = append(l.requests, now)
	return true
}

// Wait 等待直到允许请求（简单实现：轮询）
func (l *SlidingWindowLimiter) Wait(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if l.Allow() {
			return nil
		}

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Middleware HTTP 限流中间件
func Middleware(limiter Limiter) func(next func()) error {
	return func(next func()) error {
		if !limiter.Allow() {
			return ErrRateLimitExceeded
		}
		next()
		return nil
	}
}
