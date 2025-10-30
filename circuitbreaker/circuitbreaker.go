package circuitbreaker

import (
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
)

var (
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrTooManyRequests = errors.New("too many requests")
)

// State 熔断器状态
type State int

const (
	StateClosed   State = iota // 关闭状态，正常工作
	StateOpen                  // 打开状态，拒绝请求
	StateHalfOpen              // 半开状态，尝试恢复
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Counts 统计信息
type Counts struct {
	Requests             uint32 // 总请求数
	TotalSuccesses       uint32 // 总成功数
	TotalFailures        uint32 // 总失败数
	ConsecutiveSuccesses uint32 // 连续成功数
	ConsecutiveFailures  uint32 // 连续失败数
}

func (c *Counts) onRequest() {
	c.Requests++
}

func (c *Counts) onSuccess() {
	c.TotalSuccesses++
	c.ConsecutiveSuccesses++
	c.ConsecutiveFailures = 0
}

func (c *Counts) onFailure() {
	c.TotalFailures++
	c.ConsecutiveFailures++
	c.ConsecutiveSuccesses = 0
}

func (c *Counts) clear() {
	c.Requests = 0
	c.TotalSuccesses = 0
	c.TotalFailures = 0
	c.ConsecutiveSuccesses = 0
	c.ConsecutiveFailures = 0
}

// Config 熔断器配置
type Config struct {
	MaxRequests      uint32        // 半开状态下允许的最大请求数
	Interval         time.Duration // 统计时间窗口
	Timeout          time.Duration // 打开状态持续时间
	FailureThreshold float64       // 失败率阈值(0-1)
	MinimumRequests  uint32        // 最小请求数，低于此值不触发熔断
	OnStateChange    func(from, to State)
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
	return &Config{
		MaxRequests:      10,
		Interval:         10 * time.Second,
		Timeout:          60 * time.Second,
		FailureThreshold: 0.5,
		MinimumRequests:  10,
	}
}

// CircuitBreaker 熔断器
type CircuitBreaker struct {
	config     *Config
	state      State
	generation uint64
	counts     Counts
	expiry     time.Time
	mu         sync.Mutex
	logger     *zap.Logger
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(config *Config, logger *zap.Logger) *CircuitBreaker {
	if config == nil {
		config = DefaultConfig()
	}
	if logger == nil {
		logger = zap.L()
	}

	cb := &CircuitBreaker{
		config: config,
		state:  StateClosed,
		logger: logger.With(zap.String("component", "circuit_breaker")),
	}

	cb.toNewGeneration(time.Now())
	return cb
}

// Execute 执行函数
func (cb *CircuitBreaker) Execute(fn func() error) error {
	generation, err := cb.beforeRequest()
	if err != nil {
		return err
	}

	defer func() {
		if e := recover(); e != nil {
			cb.afterRequest(generation, false)
			panic(e)
		}
	}()

	err = fn()
	cb.afterRequest(generation, err == nil)
	return err
}

// State 获取当前状态
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	state, _ := cb.currentState(now)
	return state
}

// Counts 获取统计信息
func (cb *CircuitBreaker) Counts() Counts {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.counts
}

// beforeRequest 请求前检查
func (cb *CircuitBreaker) beforeRequest() (uint64, error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)

	if state == StateOpen {
		return generation, ErrCircuitOpen
	} else if state == StateHalfOpen && cb.counts.Requests >= cb.config.MaxRequests {
		return generation, ErrTooManyRequests
	}

	cb.counts.onRequest()
	return generation, nil
}

// afterRequest 请求后处理
func (cb *CircuitBreaker) afterRequest(before uint64, success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)

	// 如果generation不匹配，说明状态已经改变，忽略此次结果
	if generation != before {
		return
	}

	if success {
		cb.onSuccess(state, now)
	} else {
		cb.onFailure(state, now)
	}
}

// onSuccess 成功时的处理
func (cb *CircuitBreaker) onSuccess(state State, now time.Time) {
	cb.counts.onSuccess()

	if state == StateHalfOpen {
		// 半开状态下，如果连续成功达到一定次数，转为关闭状态
		if cb.counts.ConsecutiveSuccesses >= cb.config.MaxRequests {
			cb.setState(StateClosed, now)
		}
	}
}

// onFailure 失败时的处理
func (cb *CircuitBreaker) onFailure(state State, now time.Time) {
	cb.counts.onFailure()

	switch state {
	case StateClosed:
		// 关闭状态下，检查是否需要打开
		if cb.shouldTrip() {
			cb.setState(StateOpen, now)
		}
	case StateHalfOpen:
		// 半开状态下，任何失败都直接打开
		cb.setState(StateOpen, now)
	}
}

// shouldTrip 判断是否应该触发熔断
func (cb *CircuitBreaker) shouldTrip() bool {
	counts := cb.counts

	// 请求数不足最小值，不触发
	if counts.Requests < cb.config.MinimumRequests {
		return false
	}

	// 计算失败率
	failureRate := float64(counts.TotalFailures) / float64(counts.Requests)

	cb.logger.Debug("checking failure rate",
		zap.Uint32("requests", counts.Requests),
		zap.Uint32("failures", counts.TotalFailures),
		zap.Float64("failure_rate", failureRate),
		zap.Float64("threshold", cb.config.FailureThreshold))

	return failureRate >= cb.config.FailureThreshold
}

// currentState 获取当前状态
func (cb *CircuitBreaker) currentState(now time.Time) (State, uint64) {
	switch cb.state {
	case StateClosed:
		// 检查是否需要重置统计
		if !cb.expiry.IsZero() && cb.expiry.Before(now) {
			cb.toNewGeneration(now)
		}
	case StateOpen:
		// 检查是否应该进入半开状态
		if cb.expiry.Before(now) {
			cb.setState(StateHalfOpen, now)
		}
	}
	return cb.state, cb.generation
}

// setState 设置状态
func (cb *CircuitBreaker) setState(newState State, now time.Time) {
	if cb.state == newState {
		return
	}

	prev := cb.state
	cb.state = newState

	cb.logger.Info("circuit breaker state changed",
		zap.String("from", prev.String()),
		zap.String("to", newState.String()))

	cb.toNewGeneration(now)

	if cb.config.OnStateChange != nil {
		cb.config.OnStateChange(prev, newState)
	}
}

// toNewGeneration 进入新的generation
func (cb *CircuitBreaker) toNewGeneration(now time.Time) {
	cb.generation++
	cb.counts.clear()

	var zero time.Time
	switch cb.state {
	case StateClosed:
		if cb.config.Interval == 0 {
			cb.expiry = zero
		} else {
			cb.expiry = now.Add(cb.config.Interval)
		}
	case StateOpen:
		cb.expiry = now.Add(cb.config.Timeout)
	default: // StateHalfOpen
		cb.expiry = zero
	}
}

// Reset 重置熔断器
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.logger.Info("resetting circuit breaker")
	cb.toNewGeneration(time.Now())
	cb.state = StateClosed
}
