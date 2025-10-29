package registry

import (
	"context"
	"time"
)

// ServiceRegistry 服务注册中心接口
type ServiceRegistry interface {
	// Register 注册服务实例
	// serviceName: 服务名称，例如 "geeCache"
	// instanceID: 实例唯一标识，例如 "node1"
	// addr: 实例地址，例如 "http://localhost:8001"
	// ttl: 租约时长
	Register(ctx context.Context, serviceName, instanceID, addr string, ttl time.Duration) error
	// Unregister 注销服务实例
	Unregister(ctx context.Context, serviceName, instanceID string) error
	// Discover 发现服务的所有实例
	// 返回实例ID到地址的映射
	Discover(ctx context.Context, serviceName string) (map[string]string, error)
	// Watch 监听服务实例变化
	// 返回一个channel, 当有实例上线或者下线时会收到通知
	Watch(ctx context.Context, serviceName string) (<-chan Event, error)
	// Close 关闭注册中心连接
	Close() error
}

// EventType 事件类型
type EventType int

const (
	EventTypePut    EventType = iota // 实例上线或更新
	EventTypeDelete                  // 实例下线
)

// Event 服务实例变化事件
type Event struct {
	Type       EventType // 事件类型
	InstanceID string    // 实例ID
	Addr       string    // 实例地址（删除事件时为空）
}

// Config 注册中心配置
type Config struct {
	Endpoints   []string      // etcd endpoints
	DialTimeout time.Duration // 连接超时时间
	Username    string        // 用户名（可选）
	Password    string        // 密码（可选）
}

func DefaultConfig() *Config {
	return &Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
	}
}
