package geeCache

import (
	"context"
	"fmt"
	"github.com/lyb88999/geeCache/geeCache/consistentHash"
	"github.com/lyb88999/geeCache/registry"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultBasePath = "/_geecache/"
const defaultReplicas = 50

type HTTPPool struct {
	self        string                 // 当前节点地址，例如 "http://localhost:8001"
	basePath    string                 // 节点间通讯地址的前缀
	mu          sync.Mutex             // 保护peers和httpGetters
	peers       *consistentHash.Map    // 用来根据具体的key选择节点
	httpGetters map[string]*httpGetter // 映射远程节点与对应的httpGetter

	registry    registry.ServiceRegistry // 注册中心
	serviceName string                   // 服务名称 例如 "geeCache"
	instanceID  string                   // 实例ID 例如 "node1"
	ctx         context.Context          // 用于控制goroutine的生命周期
	cancel      context.CancelFunc       // 取消函数
}

// NewHTTPPool 创建HTTP连接池
func NewHTTPPool(self string) *HTTPPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &HTTPPool{
		self:     self,
		basePath: defaultBasePath,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// SetRegistry 设置注册中心
func (p *HTTPPool) SetRegistry(reg registry.ServiceRegistry, serviceName, instanceID string) {
	p.registry = reg
	p.serviceName = serviceName
	p.instanceID = instanceID
}

func (p *HTTPPool) Start() error {
	if p.registry == nil {
		return fmt.Errorf("registor is not set, please call SetRegistry first")
	}
	// 1.初始化一致性哈希环
	p.mu.Lock()
	p.peers = consistentHash.New(defaultReplicas, nil)
	p.httpGetters = make(map[string]*httpGetter)
	p.mu.Unlock()

	// 2.注册当前节点到服务中心
	err := p.registry.Register(p.ctx, p.serviceName, p.instanceID, p.self, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}
	p.Log("registered to service center as %s", p.instanceID)

	// 3.发现现有节点并添加到哈希环
	instances, err := p.registry.Discover(p.ctx, p.serviceName)
	if err != nil {
		return fmt.Errorf("failed to discover services: %w", err)
	}
	p.mu.Lock()
	for instanceID, addr := range instances {
		if addr != p.self {
			p.peers.Add(addr)
			p.httpGetters[addr] = &httpGetter{baseURL: addr + p.basePath}
			p.Log("discovered peer: %s -> %s", instanceID, addr)
		}
	}
	p.mu.Unlock()

	// 4.启动监听流程 处理节点变化
	eventChan, err := p.registry.Watch(p.ctx, p.serviceName)
	if err != nil {
		return fmt.Errorf("failed to watch services: %w", err)
	}

	go p.handleEvents(eventChan)
	p.Log("started watching service changes")
	return nil
}

func (p *HTTPPool) handleEvents(eventChan <-chan registry.Event) {
	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				p.Log("event channel closed, stop watching")
				return
			}
			switch event.Type {
			case registry.EventTypePut:
				if event.Addr != p.self {
					p.addPeer(event.Addr)
					p.Log("peer online: %s -> %s", event.InstanceID, event.Addr)
				}
			case registry.EventTypeDelete:
				if event.InstanceID != p.instanceID {
					p.removePeer(event.InstanceID, instances(p.registry, p.ctx, p.serviceName))
					p.Log("peer offline: %s", event.InstanceID)
				}
			}
		case <-p.ctx.Done():
			p.Log("context cancelled, stop handling events")
			return
		}
	}
}

func (p *HTTPPool) Set(peers ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.peers = consistentHash.New(defaultReplicas, nil)
	p.peers.Add(peers...)
	p.httpGetters = make(map[string]*httpGetter)
	// 为每个节点创建了一个HTTP客户端 httpGetter
	for _, peer := range peers {
		p.httpGetters[peer] = &httpGetter{
			baseURL: peer + p.basePath,
		}
	}
}

func (p *HTTPPool) PickPeer(key string) (PeerGetter, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if peer := p.peers.Get(key); peer != "" && peer != p.self {
		p.Log("Pick peer %s", peer)
		return p.httpGetters[peer], true
	}
	return nil, false
}

// addPeer 添加节点到哈希环
func (p *HTTPPool) addPeer(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.peers == nil {
		p.peers = consistentHash.New(defaultReplicas, nil)
	}
	if p.httpGetters == nil {
		p.httpGetters = make(map[string]*httpGetter)
	}

	p.peers.Add(addr)
	p.httpGetters[addr] = &httpGetter{baseURL: addr + p.basePath}
}

// removePeer 从哈希环移除节点
func (p *HTTPPool) removePeer(instanceID string, instances map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 查找该instanceID对应的地址
	addr, exists := instances[instanceID]
	if !exists {
		return
	}

	p.peers.Remove(addr)
	delete(p.httpGetters, addr)
}

// instances 获取当前所有实例（辅助函数）
func instances(reg registry.ServiceRegistry, ctx context.Context, serviceName string) map[string]string {
	result, err := reg.Discover(ctx, serviceName)
	if err != nil {
		return make(map[string]string)
	}
	return result
}

// Stop 停止HTTPPool，从服务中心注销
func (p *HTTPPool) Stop() error {
	if p.registry != nil {
		err := p.registry.Unregister(p.ctx, p.serviceName, p.instanceID)
		if err != nil {
			p.Log("failed to unregister: %v", err)
		}
	}

	// 取消所有goroutine
	if p.cancel != nil {
		p.cancel()
	}

	p.Log("stopped")
	return nil
}

var _ PeerPicker = (*HTTPPool)(nil)

func (p *HTTPPool) Log(format string, v ...interface{}) {
	log.Printf("[Server %s] %s", p.self, fmt.Sprintf(format, v...))
}

func (p *HTTPPool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, p.basePath) {
		panic("HTTPPool serving unexpected path: " + r.URL.Path)
	}
	p.Log("%s %s", r.Method, r.URL.Path)
	// 正确的url : "/<basePath>/<groupname>/<key>"
	parts := strings.SplitN(r.URL.Path[len(p.basePath):], "/", 2)
	if len(parts) != 2 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	groupName := parts[0]
	key := parts[1]
	group := GetGroup(groupName)
	if group == nil {
		http.Error(w, "no such group: "+groupName, http.StatusNotFound)
		return
	}

	view, err := group.Get(key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(view.ByteSlice())
}

type httpGetter struct {
	baseURL string
}

func (h *httpGetter) Get(group string, key string) ([]byte, error) {
	u := fmt.Sprintf(
		"%v%v/%v",
		h.baseURL,
		url.QueryEscape(group),
		url.QueryEscape(key),
	)
	res, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned: %v", res.Status)
	}
	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %v", err)
	}
	return bytes, nil
}

var _ PeerGetter = (*httpGetter)(nil)
