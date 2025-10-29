package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lyb88999/geeCache/registry"
)

// 这个示例演示如何使用服务注册中心

func main() {
	// 1. 创建注册中心配置
	config := registry.DefaultConfig()
	config.Endpoints = []string{"localhost:2379"} // etcd地址

	// 2. 创建etcd注册中心
	reg, err := registry.NewEtcdRegistry(config)
	if err != nil {
		log.Fatalf("failed to create registry: %v", err)
	}
	defer reg.Close()

	ctx := context.Background()

	// 3. 注册服务实例
	serviceName := "geeCache"
	instanceID := "node1"
	addr := "http://localhost:8001"
	ttl := 10 * time.Second

	err = reg.Register(ctx, serviceName, instanceID, addr, ttl)
	if err != nil {
		log.Fatalf("failed to register service: %v", err)
	}
	fmt.Printf("✅ 注册服务成功: %s/%s -> %s\n", serviceName, instanceID, addr)

	// 4. 发现服务实例
	instances, err := reg.Discover(ctx, serviceName)
	if err != nil {
		log.Fatalf("failed to discover service: %v", err)
	}
	fmt.Println("\n📋 发现的服务实例:")
	for id, address := range instances {
		fmt.Printf("  - %s: %s\n", id, address)
	}

	// 5. 监听服务变化
	fmt.Println("\n👀 开始监听服务变化...")
	eventChan, err := reg.Watch(ctx, serviceName)
	if err != nil {
		log.Fatalf("failed to watch service: %v", err)
	}

	// 启动一个goroutine来处理事件
	go func() {
		for event := range eventChan {
			switch event.Type {
			case registry.EventTypePut:
				fmt.Printf("🟢 实例上线: %s -> %s\n", event.InstanceID, event.Addr)
			case registry.EventTypeDelete:
				fmt.Printf("🔴 实例下线: %s\n", event.InstanceID)
			}
		}
	}()

	// 6. 模拟运行一段时间
	fmt.Println("\n⏳ 服务运行中，按Ctrl+C退出...")
	time.Sleep(30 * time.Second)

	// 7. 注销服务
	err = reg.Unregister(ctx, serviceName, instanceID)
	if err != nil {
		log.Fatalf("failed to unregister service: %v", err)
	}
	fmt.Printf("\n✅ 注销服务成功: %s/%s\n", serviceName, instanceID)
}
