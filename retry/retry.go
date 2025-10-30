package retry

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
)

var (
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
)

// Config 重试配置
type Config struct {
	MaxRetries     int              // 最大重试次数
	InitialBackoff time.Duration    // 初始退避时间
	MaxBackoff     time.Duration    // 最大退避时间
	Multiplier     float64          // 退避时间倍数
	RetryableFunc  func(error) bool // 判断是否可重试的函数
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
	return &Config{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		Multiplier:     2.0,
		RetryableFunc:  DefaultRetryable,
	}
}

// DefaultRetryable 默认的重试判断函数
func DefaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	// 默认所有错误都可重试
	// 可以根据具体错误类型定制
	return true
}

// Retryer 重试器
type Retryer struct {
	config *Config
	logger *zap.Logger
}

// NewRetryer 创建重试器
func NewRetryer(config *Config, logger *zap.Logger) *Retryer {
	if config == nil {
		config = DefaultConfig()
	}
	if logger == nil {
		logger = zap.L()
	}

	return &Retryer{
		config: config,
		logger: logger.With(zap.String("component", "retryer")),
	}
}

// Execute 执行函数，带重试
func (r *Retryer) Execute(ctx context.Context, fn func() error) error {
	var lastErr error
	backoff := r.config.InitialBackoff

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		// 第一次不是重试
		if attempt > 0 {
			r.logger.Info("retrying request",
				zap.Int("attempt", attempt),
				zap.Int("max_retries", r.config.MaxRetries),
				zap.Duration("backoff", backoff))

			// 等待退避时间
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}

			// 计算下一次退避时间（指数退避）
			backoff = time.Duration(float64(backoff) * r.config.Multiplier)
			if backoff > r.config.MaxBackoff {
				backoff = r.config.MaxBackoff
			}
		}

		// 执行函数
		err := fn()
		if err == nil {
			if attempt > 0 {
				r.logger.Info("request succeeded after retry",
					zap.Int("attempt", attempt))
			}
			return nil
		}

		lastErr = err

		// 检查是否可以重试
		if !r.config.RetryableFunc(err) {
			r.logger.Info("error is not retryable",
				zap.Error(err))
			return err
		}

		// 检查上下文
		if ctx.Err() != nil {
			return ctx.Err()
		}

		r.logger.Warn("request failed, will retry",
			zap.Int("attempt", attempt),
			zap.Error(err))
	}

	r.logger.Error("max retries exceeded",
		zap.Int("max_retries", r.config.MaxRetries),
		zap.Error(lastErr))

	return ErrMaxRetriesExceeded
}

// ExecuteWithResult 执行函数并返回结果，带重试
func (r *Retryer) ExecuteWithResult(ctx context.Context, fn func() (interface{}, error)) (interface{}, error) {
	var lastErr error
	backoff := r.config.InitialBackoff

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		if attempt > 0 {
			r.logger.Info("retrying request",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			backoff = time.Duration(float64(backoff) * r.config.Multiplier)
			if backoff > r.config.MaxBackoff {
				backoff = r.config.MaxBackoff
			}
		}

		result, err := fn()
		if err == nil {
			if attempt > 0 {
				r.logger.Info("request succeeded after retry",
					zap.Int("attempt", attempt))
			}
			return result, nil
		}

		lastErr = err

		if !r.config.RetryableFunc(err) {
			return nil, err
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		r.logger.Warn("request failed, will retry",
			zap.Int("attempt", attempt),
			zap.Error(err))
	}

	r.logger.Error("max retries exceeded",
		zap.Int("max_retries", r.config.MaxRetries),
		zap.Error(lastErr))

	return nil, ErrMaxRetriesExceeded
}

// BackoffStrategy 退避策略
type BackoffStrategy interface {
	Next(attempt int) time.Duration
}

// ExponentialBackoff 指数退避
type ExponentialBackoff struct {
	Initial    time.Duration
	Max        time.Duration
	Multiplier float64
}

func (e *ExponentialBackoff) Next(attempt int) time.Duration {
	if attempt == 0 {
		return 0
	}

	backoff := e.Initial
	for i := 1; i < attempt; i++ {
		backoff = time.Duration(float64(backoff) * e.Multiplier)
		if backoff > e.Max {
			return e.Max
		}
	}
	return backoff
}

// LinearBackoff 线性退避
type LinearBackoff struct {
	Initial   time.Duration
	Increment time.Duration
	Max       time.Duration
}

func (l *LinearBackoff) Next(attempt int) time.Duration {
	if attempt == 0 {
		return 0
	}

	backoff := l.Initial + time.Duration(attempt-1)*l.Increment
	if backoff > l.Max {
		return l.Max
	}
	return backoff
}

// ConstantBackoff 固定退避
type ConstantBackoff struct {
	Delay time.Duration
}

func (c *ConstantBackoff) Next(attempt int) time.Duration {
	if attempt == 0 {
		return 0
	}
	return c.Delay
}
