package registry

import (
	"context"
	"fmt"
	clientv3 "go.etcd.io/etcd/client/v3"
	"log"
	"time"
)

// EtcdRegistry 基于etcd的服务注册实现
type EtcdRegistry struct {
	client  *clientv3.Client
	leaseID clientv3.LeaseID // 租约ID
	// key格式: /services/{serviceName}/{instanceID}
	keyPrefix string
}

func NewEtcdRegistry(config *Config) (ServiceRegistry, error) {
	if config == nil {
		config = DefaultConfig()
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   config.Endpoints,
		DialTimeout: config.DialTimeout,
		Username:    config.Username,
		Password:    config.Password,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}
	return &EtcdRegistry{
		client:    client,
		keyPrefix: "/services"}, nil
}

func (e *EtcdRegistry) Register(ctx context.Context, serviceName, instanceID, addr string, ttl time.Duration) error {
	// 1. 创建租约
	lease, err := e.client.Grant(ctx, int64(ttl.Seconds()))
	if err != nil {
		return fmt.Errorf("failed to create lease: %w", err)
	}
	e.leaseID = lease.ID

	// 2. 注册服务实例
	key := e.makeKey(serviceName, instanceID)
	_, err = e.client.Put(ctx, key, addr, clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	// 3. 启动心跳续约
	keepAliveChan, err := e.client.KeepAlive(ctx, lease.ID)
	if err != nil {
		return fmt.Errorf("failed to keep alive: %w", err)
	}

	// 4. 消费心跳响应
	go func() {
		for {
			select {
			case resp, ok := <-keepAliveChan:
				if !ok {
					log.Printf("[Register] keep alive channel closed for instance %s", instanceID)
					return
				}
				if resp != nil {
					log.Printf("[Registry] keep alive success for instance %s, TTL: %d", instanceID, resp.TTL)
				}
			case <-ctx.Done():
				log.Printf("[Registry] context cancelled, stop keep alive for instance %s", instanceID)
				return
			}
		}
	}()
	log.Printf("[Registry] registered service: %s, instance: %s, addr: %s", serviceName, instanceID, addr)
	return nil
}

func (e *EtcdRegistry) Unregister(ctx context.Context, serviceName, instanceID string) error {
	key := e.makeKey(serviceName, instanceID)
	// 1. 删除服务key
	_, err := e.client.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to unregister service: %w", err)
	}
	// 2. 撤销租约
	if e.leaseID != 0 {
		_, err = e.client.Revoke(ctx, e.leaseID)
		if err != nil {
			log.Printf("[Register] failed to revoke lease: %v", err)
		}
	}
	log.Printf("[Registry] unregistered service: %s, instance: %s", serviceName, instanceID)
	return nil
}

func (e *EtcdRegistry) Discover(ctx context.Context, serviceName string) (map[string]string, error) {
	prefix := e.makeServicePrefix(serviceName)
	resp, err := e.client.Get(ctx, prefix, clientv3.WithLease(e.leaseID))
	if err != nil {
		return nil, fmt.Errorf("failed to discover service: %w", err)
	}
	instances := make(map[string]string)
	for _, kv := range resp.Kvs {
		instanceID := e.extraInstanceID(string(kv.Key), serviceName)
		instances[instanceID] = string(kv.Value)
	}
	return instances, nil
}

func (e *EtcdRegistry) Watch(ctx context.Context, serviceName string) (<-chan Event, error) {
	prefix := e.makeServicePrefix(serviceName)
	watchChan := e.client.Watch(ctx, prefix, clientv3.WithPrefix())

	eventChan := make(chan Event, 10)

	go func() {
		defer close(eventChan)
		for {
			select {
			case watchResp, ok := <-watchChan:
				if !ok {
					log.Printf("[Registry] watch channel closed for service %s", serviceName)
					return
				}
				if watchResp.Err() != nil {
					log.Printf("[Register] watch error: %v", watchResp.Err())
					continue
				}
				for _, event := range watchResp.Events {
					instanceID := e.extraInstanceID(string(event.Kv.Key), serviceName)
					var evt Event
					switch event.Type {
					case clientv3.EventTypePut:
						evt = Event{
							Type:       EventTypePut,
							InstanceID: instanceID,
							Addr:       string(event.Kv.Value),
						}
					case clientv3.EventTypeDelete:
						evt = Event{
							Type:       EventTypeDelete,
							InstanceID: instanceID,
							Addr:       "",
						}
					}
					select {
					case eventChan <- evt:
					case <-ctx.Done():
						return
					}
				}
			case <-ctx.Done():
				log.Printf("[Registry] context cancelled, stop watching service %s", serviceName)
				return
			}
		}
	}()
	return eventChan, nil
}

func (e *EtcdRegistry) Close() error {
	if e.client != nil {
		return e.client.Close()
	}
	return nil
}

func (e *EtcdRegistry) makeKey(serviceName, instanceID string) string {
	return fmt.Sprintf("%s/%s/%s", e.keyPrefix, serviceName, instanceID)
}

func (e *EtcdRegistry) makeServicePrefix(serviceName string) string {
	return fmt.Sprintf("%s/%s/", e.keyPrefix, serviceName)
}

func (e *EtcdRegistry) extraInstanceID(key, serviceName string) string {
	prefix := e.makeServicePrefix(serviceName)
	return key[len(prefix):]
}
