package geeCache

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lyb88999/geeCache/geeCache/consistentHash"
	"github.com/lyb88999/geeCache/registry"
	"go.uber.org/zap"
)

const defaultBasePath = "/_geecache/"
const defaultReplicas = 50

// HTTPPoolConfig HTTP连接池配置
type HTTPPoolConfig struct {
	Replicas        int           // 一致性哈希虚拟节点倍数
	Timeout         time.Duration // 请求超时时间
	MaxIdleConns    int           // 最大空闲连接数
	IdleConnTimeout time.Duration // 空闲连接超时
}

// DefaultHTTPPoolConfig 默认配置
func DefaultHTTPPoolConfig() *HTTPPoolConfig {
	return &HTTPPoolConfig{
		Replicas:        defaultReplicas,
		Timeout:         5 * time.Second,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}
}

type HTTPPool struct {
	self        string                 // 当前节点地址
	basePath    string                 // 节点间通讯地址的前缀
	mu          sync.Mutex             // 保护peers和httpGetters
	peers       *consistentHash.Map    // 用来根据具体的key选择节点
	httpGetters map[string]*httpGetter // 映射远程节点与对应的httpGetter
	config      *HTTPPoolConfig        // 配置

	registry    registry.ServiceRegistry // 注册中心
	serviceName string                   // 服务名称
	instanceID  string                   // 实例ID
	ctx         context.Context          // 用于控制goroutine的生命周期
	cancel      context.CancelFunc       // 取消函数
	logger      *zap.Logger              // 日志记录器
}

// NewHTTPPool 创建HTTP连接池
func NewHTTPPool(self string) *HTTPPool {
	return NewHTTPPoolWithConfig(self, DefaultHTTPPoolConfig())
}

// NewHTTPPoolWithConfig 使用自定义配置创建HTTP连接池
func NewHTTPPoolWithConfig(self string, config *HTTPPoolConfig) *HTTPPool {
	if config == nil {
		config = DefaultHTTPPoolConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())
	logger := zap.L().With(
		zap.String("component", "http_pool"),
		zap.String("self", self))

	return &HTTPPool{
		self:     self,
		basePath: defaultBasePath,
		config:   config,
		ctx:      ctx,
		cancel:   cancel,
		logger:   logger,
	}
}

// SetRegistry 设置注册中心
func (p *HTTPPool) SetRegistry(reg registry.ServiceRegistry, serviceName, instanceID string) {
	p.registry = reg
	p.serviceName = serviceName
	p.instanceID = instanceID
	p.logger = p.logger.With(
		zap.String("service", serviceName),
		zap.String("instance", instanceID))
}

func (p *HTTPPool) Start() error {
	if p.registry == nil {
		return fmt.Errorf("registry is not set, please call SetRegistry first")
	}

	p.logger.Info("starting http pool")

	// 1.初始化一致性哈希环
	p.mu.Lock()
	p.peers = consistentHash.New(p.config.Replicas, nil)
	p.httpGetters = make(map[string]*httpGetter)
	p.mu.Unlock()

	// 2.注册当前节点到服务中心
	err := p.registry.Register(p.ctx, p.serviceName, p.instanceID, p.self, 10*time.Second)
	if err != nil {
		p.logger.Error("failed to register service", zap.Error(err))
		return fmt.Errorf("failed to register service: %w", err)
	}
	p.logger.Info("registered to service center")

	// 3.发现现有节点并添加到哈希环
	instances, err := p.registry.Discover(p.ctx, p.serviceName)
	if err != nil {
		p.logger.Error("failed to discover services", zap.Error(err))
		return fmt.Errorf("failed to discover services: %w", err)
	}

	p.mu.Lock()
	for instanceID, addr := range instances {
		if addr != p.self {
			p.peers.Add(addr)
			p.httpGetters[addr] = p.newHTTPGetter(addr)
			p.logger.Info("discovered peer", zap.String("peer_id", instanceID), zap.String("addr", addr))
		}
	}
	p.mu.Unlock()

	// 4.启动监听流程 处理节点变化
	eventChan, err := p.registry.Watch(p.ctx, p.serviceName)
	if err != nil {
		p.logger.Error("failed to watch services", zap.Error(err))
		return fmt.Errorf("failed to watch services: %w", err)
	}

	go p.handleEvents(eventChan)
	p.logger.Info("started watching service changes")
	return nil
}

func (p *HTTPPool) handleEvents(eventChan <-chan registry.Event) {
	p.logger.Info("starting event handler")
	defer p.logger.Info("event handler stopped")

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				p.logger.Warn("event channel closed")
				return
			}

			switch event.Type {
			case registry.EventTypePut:
				if event.Addr != p.self {
					p.addPeer(event.Addr)
					p.logger.Info("peer online", zap.String("instance", event.InstanceID), zap.String("addr", event.Addr))
				}
			case registry.EventTypeDelete:
				if event.InstanceID != p.instanceID {
					p.removePeer(event.InstanceID, instances(p.registry, p.ctx, p.serviceName))
					p.logger.Info("peer offline", zap.String("instance", event.InstanceID))
				}
			}
		case <-p.ctx.Done():
			p.logger.Info("context cancelled")
			return
		}
	}
}

// Set 手动设置节点（不使用注册中心时）
func (p *HTTPPool) Set(peers ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logger.Info("manually setting peers", zap.Int("count", len(peers)))

	p.peers = consistentHash.New(p.config.Replicas, nil)
	p.peers.Add(peers...)
	p.httpGetters = make(map[string]*httpGetter)

	for _, peer := range peers {
		p.httpGetters[peer] = p.newHTTPGetter(peer)
	}
}

func (p *HTTPPool) PickPeer(key string) (PeerGetter, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if peer := p.peers.Get(key); peer != "" && peer != p.self {
		p.logger.Debug("picked peer", zap.String("key", key), zap.String("peer", peer))
		return p.httpGetters[peer], true
	}
	return nil, false
}

func (p *HTTPPool) addPeer(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.peers == nil {
		p.peers = consistentHash.New(p.config.Replicas, nil)
	}
	if p.httpGetters == nil {
		p.httpGetters = make(map[string]*httpGetter)
	}

	p.peers.Add(addr)
	p.httpGetters[addr] = p.newHTTPGetter(addr)
}

func (p *HTTPPool) removePeer(instanceID string, instances map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	addr, exists := instances[instanceID]
	if !exists {
		return
	}

	p.peers.Remove(addr)
	delete(p.httpGetters, addr)
}

func instances(reg registry.ServiceRegistry, ctx context.Context, serviceName string) map[string]string {
	result, err := reg.Discover(ctx, serviceName)
	if err != nil {
		return make(map[string]string)
	}
	return result
}

func (p *HTTPPool) Stop() error {
	p.logger.Info("stopping http pool")

	if p.registry != nil {
		err := p.registry.Unregister(p.ctx, p.serviceName, p.instanceID)
		if err != nil {
			p.logger.Error("failed to unregister", zap.Error(err))
		}
	}

	if p.cancel != nil {
		p.cancel()
	}

	p.logger.Info("http pool stopped")
	return nil
}

var _ PeerPicker = (*HTTPPool)(nil)

func (p *HTTPPool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, p.basePath) {
		p.logger.Error("unexpected path", zap.String("path", r.URL.Path))
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	p.logger.Debug("received request", zap.String("method", r.Method), zap.String("path", r.URL.Path))

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
		p.logger.Error("group not found", zap.String("group", groupName))
		http.Error(w, "no such group: "+groupName, http.StatusNotFound)
		return
	}

	view, err := group.Get(key)
	if err != nil {
		p.logger.Error("failed to get value", zap.String("group", groupName), zap.String("key", key), zap.Error(err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(view.ByteSlice())
}

// newHTTPGetter 创建HTTP getter（带连接池和超时）
func (p *HTTPPool) newHTTPGetter(baseURL string) *httpGetter {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        p.config.MaxIdleConns,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     p.config.IdleConnTimeout,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &httpGetter{
		baseURL: baseURL + p.basePath,
		client: &http.Client{
			Transport: transport,
			Timeout:   p.config.Timeout,
		},
		logger: p.logger.With(zap.String("peer", baseURL)),
	}
}

type httpGetter struct {
	baseURL string
	client  *http.Client
	logger  *zap.Logger
}

func (h *httpGetter) Get(group string, key string) ([]byte, error) {
	u := fmt.Sprintf(
		"%v%v/%v",
		h.baseURL,
		url.QueryEscape(group),
		url.QueryEscape(key),
	)

	h.logger.Debug("fetching from peer", zap.String("url", u))

	res, err := h.client.Get(u)
	if err != nil {
		h.logger.Error("failed to fetch from peer", zap.String("url", u), zap.Error(err))
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		h.logger.Error("peer returned error", zap.String("url", u), zap.Int("status", res.StatusCode))
		return nil, fmt.Errorf("server returned: %v", res.Status)
	}

	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		h.logger.Error("failed to read response body", zap.Error(err))
		return nil, fmt.Errorf("reading response body: %v", err)
	}

	h.logger.Debug("successfully fetched from peer", zap.Int("bytes", len(bytes)))
	return bytes, nil
}

var _ PeerGetter = (*httpGetter)(nil)
