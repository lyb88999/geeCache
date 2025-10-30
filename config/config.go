package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Registry RegistryConfig `mapstructure:"registry"`
	Log      LogConfig      `mapstructure:"log"`
	Health   HealthConfig   `mapstructure:"health"`
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
	v.SetDefault("cache.max_bytes", 2<<10) // 2MB
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
}

// Validate 验证配置
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Cache.MaxBytes <= 0 {
		return fmt.Errorf("cache max_bytes must be positive")
	}

	if c.Cache.Strategy != "lru" && c.Cache.Strategy != "lfu" && c.Cache.Strategy != "fifo" {
		return fmt.Errorf("invalid cache strategy: %s", c.Cache.Strategy)
	}

	if len(c.Registry.Endpoints) == 0 {
		return fmt.Errorf("registry endpoints cannot be empty")
	}

	if c.Log.Level != "debug" && c.Log.Level != "info" && c.Log.Level != "warn" && c.Log.Level != "error" {
		return fmt.Errorf("invalid log level: %s", c.Log.Level)
	}

	return nil
}
