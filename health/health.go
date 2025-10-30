package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Status 健康状态
type Status string

const (
	StatusHealthy   Status = "healthy"
	StatusUnhealthy Status = "unhealthy"
	StatusDegraded  Status = "degraded"
)

// Check 健康检查接口
type Check interface {
	Name() string
	Check(ctx context.Context) error
}

// CheckResult 检查结果
type CheckResult struct {
	Name      string        `json:"name"`
	Status    Status        `json:"status"`
	Message   string        `json:"message,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
	Duration  time.Duration `json:"duration"`
}

// Response 健康检查响应
type Response struct {
	Status    Status                 `json:"status"`
	Timestamp time.Time              `json:"timestamp"`
	Checks    map[string]CheckResult `json:"checks"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Checker 健康检查器
type Checker struct {
	checks   []Check
	mu       sync.RWMutex
	logger   *zap.Logger
	metadata map[string]interface{}
}

// NewChecker 创建健康检查器
func NewChecker(logger *zap.Logger) *Checker {
	if logger == nil {
		logger = zap.L()
	}
	return &Checker{
		checks:   make([]Check, 0),
		logger:   logger.With(zap.String("component", "health_checker")),
		metadata: make(map[string]interface{}),
	}
}

// RegisterCheck 注册健康检查
func (c *Checker) RegisterCheck(check Check) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks = append(c.checks, check)
	c.logger.Info("registered health check", zap.String("name", check.Name()))
}

// SetMetadata 设置元数据
func (c *Checker) SetMetadata(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metadata[key] = value
}

// CheckAll 执行所有健康检查
func (c *Checker) CheckAll(ctx context.Context) Response {
	c.mu.RLock()
	checks := make([]Check, len(c.checks))
	copy(checks, c.checks)
	metadata := make(map[string]interface{})
	for k, v := range c.metadata {
		metadata[k] = v
	}
	c.mu.RUnlock()

	results := make(map[string]CheckResult)
	overallStatus := StatusHealthy

	for _, check := range checks {
		result := c.executeCheck(ctx, check)
		results[check.Name()] = result

		if result.Status == StatusUnhealthy {
			overallStatus = StatusUnhealthy
		} else if result.Status == StatusDegraded && overallStatus == StatusHealthy {
			overallStatus = StatusDegraded
		}
	}

	return Response{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Checks:    results,
		Metadata:  metadata,
	}
}

// executeCheck 执行单个检查
func (c *Checker) executeCheck(ctx context.Context, check Check) CheckResult {
	start := time.Now()

	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := check.Check(checkCtx)
	duration := time.Since(start)

	result := CheckResult{
		Name:      check.Name(),
		Timestamp: time.Now(),
		Duration:  duration,
	}

	if err != nil {
		result.Status = StatusUnhealthy
		result.Message = err.Error()
		c.logger.Warn("health check failed",
			zap.String("name", check.Name()),
			zap.Error(err),
			zap.Duration("duration", duration))
	} else {
		result.Status = StatusHealthy
		c.logger.Debug("health check passed",
			zap.String("name", check.Name()),
			zap.Duration("duration", duration))
	}

	return result
}

// Handler 返回 HTTP 处理器
func (c *Checker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx := r.Context()
		response := c.CheckAll(ctx)

		w.Header().Set("Content-Type", "application/json")

		// 根据健康状态设置 HTTP 状态码
		switch response.Status {
		case StatusHealthy:
			w.WriteHeader(http.StatusOK)
		case StatusDegraded:
			w.WriteHeader(http.StatusOK) // 降级仍返回 200
		case StatusUnhealthy:
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			c.logger.Error("failed to encode health check response", zap.Error(err))
		}
	}
}

// SimpleCheck 简单的健康检查实现
type SimpleCheck struct {
	name    string
	checkFn func(ctx context.Context) error
}

// NewSimpleCheck 创建简单检查
func NewSimpleCheck(name string, checkFn func(ctx context.Context) error) Check {
	return &SimpleCheck{
		name:    name,
		checkFn: checkFn,
	}
}

func (s *SimpleCheck) Name() string {
	return s.name
}

func (s *SimpleCheck) Check(ctx context.Context) error {
	return s.checkFn(ctx)
}
