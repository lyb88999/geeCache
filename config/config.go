package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Server         ServerConfig         `mapstructure:"server"`
	Cache          CacheConfig          `mapstructure:"cache"`
	Registry       RegistryConfig       `mapstructure:"registry"`
	Log            LogConfig            `mapstructure:"log"`
	Health         HealthConfig         `mapstructure:"health"`
	Metrics        MetricsConfig        `mapstructure:"metrics"`
	CircuitBreaker CircuitBreakerConfig `mapstructure:"circuit_breaker"`
	Retry          RetryConfig          `mapstructure:"retry"`
	RateLimit      RateLimitConfig      `mapstructure:"rate_limit"`
	Admin          AdminConfig          `mapstructure:"admin"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port            int           `mapstructure:"port"`
	APIPort         int           `mapstructure:"api_port"`
	EnableAPI       bool          `mapstructure:"enable_api"`
	InstanceID      string        `mapstructure:"instance_id"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

// CacheConfig 缓存配置
type CacheConfig struct {
	Strategy     string        `mapstructure:"strategy"` // lru, lfu, fifo
	MaxBytes     int64         `mapstructure:"max_bytes"`
	GroupName    string        `mapstructure:"group_name"`
	Replicas     int           `mapstructure:"replicas"`       // 一致性哈希虚拟节点倍数
	PeerTimeout  time.Duration `mapstructure:"peer_timeout"`   // 节点间通信超时
	MaxIdleConns int           `mapstructure:"max_idle_conns"` // 最大空闲连接数
}

// RegistryConfig 注册中心配置
type RegistryConfig struct {
	Endpoints   []string      `mapstructure:"endpoints"`
	DialTimeout time.Duration `mapstructure:"dial_timeout"`
	Username    string        `mapstructure:"username"`
	Password    string        `mapstructure:"password"`
	ServiceName string        `mapstructure:"service_name"`
	TTL         time.Duration `mapstructure:"ttl"` // 服务注册TTL
}

// LogConfig 日志配置
type LogConfig struct {
	Level      string `mapstructure:"level"`       // debug, info, warn, error
	Format     string `mapstructure:"format"`      // json, console
	OutputPath string `mapstructure:"output_path"` // stdout, 或文件路径
}

// HealthConfig 健康检查配置
type HealthConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
}

// MetricsConfig 监控指标配置
type MetricsConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	Endpoint  string `mapstructure:"endpoint"`
	Namespace string `mapstructure:"namespace"`
}

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	Enabled          bool    `mapstructure:"enabled"`
	MaxRequests      uint32  `mapstructure:"max_requests"`
	Interval         int     `mapstructure:"interval"`          // 秒
	Timeout          int     `mapstructure:"timeout"`           // 秒
	FailureThreshold float64 `mapstructure:"failure_threshold"` // 0-1
	MinimumRequests  uint32  `mapstructure:"minimum_requests"`
}

// RetryConfig 重试配置
type RetryConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	MaxRetries     int     `mapstructure:"max_retries"`
	InitialBackoff int     `mapstructure:"initial_backoff"` // 毫秒
	MaxBackoff     int     `mapstructure:"max_backoff"`     // 毫秒
	Multiplier     float64 `mapstructure:"multiplier"`
}

// RateLimitConfig 限流配置
type RateLimitConfig struct {
	Enabled bool `mapstructure:"enabled"`
	Rate    int  `mapstructure:"rate"`  // 每秒允许的请求数
	Burst   int  `mapstructure:"burst"` // 突发请求数
}

// AdminConfig 管理API配置
type AdminConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
}

// Load 加载配置
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// 设置配置文件路径
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath("/etc/geecache")
	}

	// 设置环境变量
	v.SetEnvPrefix("GEECACHE")
	v.AutomaticEnv()

	// 设置默认值
	setDefaults(v)

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// 配置文件不存在时使用默认值
	}

	// 解析配置
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 验证配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// setDefaults 设置默认值
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.port", 8001)
	v.SetDefault("server.api_port", 9999)
	v.SetDefault("server.enable_api", false)
	v.SetDefault("server.instance_id", "")
	v.SetDefault("server.read_timeout", 10*time.Second)
	v.SetDefault("server.write_timeout", 10*time.Second)
	v.SetDefault("server.shutdown_timeout", 30*time.Second)

	// Cache defaults
	v.SetDefault("cache.strategy", "lru")
	v.SetDefault("cache.max_bytes", 2<<20) // 2MB
	v.SetDefault("cache.group_name", "scores")
	v.SetDefault("cache.replicas", 50)
	v.SetDefault("cache.peer_timeout", 5*time.Second)
	v.SetDefault("cache.max_idle_conns", 100)

	// Registry defaults
	v.SetDefault("registry.endpoints", []string{"localhost:2379"})
	v.SetDefault("registry.dial_timeout", 5*time.Second)
	v.SetDefault("registry.service_name", "geeCache")
	v.SetDefault("registry.ttl", 10*time.Second)

	// Log defaults
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")
	v.SetDefault("log.output_path", "stdout")

	// Health defaults
	v.SetDefault("health.enabled", true)
	v.SetDefault("health.endpoint", "/health")

	// Metrics defaults
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.endpoint", "/metrics")
	v.SetDefault("metrics.namespace", "geecache")

	// Circuit Breaker defaults
	v.SetDefault("circuit_breaker.enabled", true)
	v.SetDefault("circuit_breaker.max_requests", 10)
	v.SetDefault("circuit_breaker.interval", 10)
	v.SetDefault("circuit_breaker.timeout", 60)
	v.SetDefault("circuit_breaker.failure_threshold", 0.5)
	v.SetDefault("circuit_breaker.minimum_requests", 10)

	// Retry defaults
	v.SetDefault("retry.enabled", true)
	v.SetDefault("retry.max_retries", 3)
	v.SetDefault("retry.initial_backoff", 100)
	v.SetDefault("retry.max_backoff", 5000)
	v.SetDefault("retry.multiplier", 2.0)

	// Rate Limit defaults
	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.rate", 1000)
	v.SetDefault("rate_limit.burst", 1500)

	// Admin defaults
	v.SetDefault("admin.enabled", true)
	v.SetDefault("admin.endpoint", "/admin")
}

// Validate 验证配置
func (c *Config) Validate() error {
	// Server validation
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Server.EnableAPI && (c.Server.APIPort <= 0 || c.Server.APIPort > 65535) {
		return fmt.Errorf("invalid API port: %d", c.Server.APIPort)
	}

	// Cache validation
	if c.Cache.MaxBytes <= 0 {
		return fmt.Errorf("cache max_bytes must be positive")
	}
	if c.Cache.Strategy != "lru" && c.Cache.Strategy != "lfu" && c.Cache.Strategy != "fifo" {
		return fmt.Errorf("invalid cache strategy: %s", c.Cache.Strategy)
	}
	if c.Cache.Replicas <= 0 {
		return fmt.Errorf("cache replicas must be positive")
	}

	// Registry validation
	if len(c.Registry.Endpoints) == 0 {
		return fmt.Errorf("registry endpoints cannot be empty")
	}
	if c.Registry.ServiceName == "" {
		return fmt.Errorf("registry service_name cannot be empty")
	}

	// Log validation
	if c.Log.Level != "debug" && c.Log.Level != "info" && c.Log.Level != "warn" && c.Log.Level != "error" {
		return fmt.Errorf("invalid log level: %s", c.Log.Level)
	}
	if c.Log.Format != "json" && c.Log.Format != "console" {
		return fmt.Errorf("invalid log format: %s", c.Log.Format)
	}

	// Circuit Breaker validation
	if c.CircuitBreaker.Enabled {
		if c.CircuitBreaker.FailureThreshold < 0 || c.CircuitBreaker.FailureThreshold > 1 {
			return fmt.Errorf("circuit breaker failure_threshold must be between 0 and 1")
		}
		if c.CircuitBreaker.MaxRequests == 0 {
			return fmt.Errorf("circuit breaker max_requests must be positive")
		}
	}

	// Retry validation
	if c.Retry.Enabled {
		if c.Retry.MaxRetries < 0 {
			return fmt.Errorf("retry max_retries must be non-negative")
		}
		if c.Retry.Multiplier <= 0 {
			return fmt.Errorf("retry multiplier must be positive")
		}
	}

	// Rate Limit validation
	if c.RateLimit.Enabled {
		if c.RateLimit.Rate <= 0 {
			return fmt.Errorf("rate_limit rate must be positive")
		}
		if c.RateLimit.Burst < c.RateLimit.Rate {
			return fmt.Errorf("rate_limit burst must be >= rate")
		}
	}

	return nil
}
