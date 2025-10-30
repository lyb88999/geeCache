package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"time"
)

// Metrics 包含所有Prometheus指标
type Metrics struct {
	// 请求相关
	RequestsTotal    *prometheus.CounterVec   // 总请求数
	RequestsDuration *prometheus.HistogramVec // 请求耗时分布

	// 缓存相关
	CacheHits   *prometheus.CounterVec // 缓存命中数
	CacheMisses *prometheus.CounterVec // 缓存未命中数
	CacheSize   *prometheus.GaugeVec   // 缓存大小

	// 远程节点相关
	PeerRequests *prometheus.CounterVec   // 远程节点请求数
	PeerErrors   *prometheus.CounterVec   // 远程节点请求错误数
	PeerDuration *prometheus.HistogramVec // 远程节点请求耗时分布

	// 系统相关
	Goroutines prometheus.Gauge // 当前Goroutine数
	Uptime     prometheus.Gauge // 运行时间
}

var (
	// 全局metric实例
	defaultMetrics *Metrics
	startTime      time.Time
)

// Init 初始化 metrics
func Init(namespace string) *Metrics {
	if namespace == "" {
		namespace = "geecache"
	}

	startTime = time.Now()

	m := &Metrics{
		// 请求指标
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "requests_total",
				Help:      "Total number of requests",
			},
			[]string{"group", "status"}, // status: success, error
		),

		RequestsDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "request_duration_seconds",
				Help:      "Request duration in seconds",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"group", "source"}, // source: local, peer
		),

		// 缓存指标
		CacheHits: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "cache_hits_total",
				Help:      "Total number of cache hits",
			},
			[]string{"group"},
		),

		CacheMisses: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "cache_misses_total",
				Help:      "Total number of cache misses",
			},
			[]string{"group"},
		),

		CacheSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "cache_size_bytes",
				Help:      "Current cache size in bytes",
			},
			[]string{"group"},
		),

		// 远程节点指标
		PeerRequests: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "peer_requests_total",
				Help:      "Total number of peer requests",
			},
			[]string{"peer", "status"},
		),

		PeerErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "peer_errors_total",
				Help:      "Total number of peer errors",
			},
			[]string{"peer", "error_type"},
		),

		PeerDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "peer_request_duration_seconds",
				Help:      "Peer request duration in seconds",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
			},
			[]string{"peer"},
		),

		// 系统指标
		Goroutines: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "goroutines",
				Help:      "Number of goroutines",
			},
		),

		Uptime: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "uptime_seconds",
				Help:      "Uptime in seconds",
			},
		),
	}

	defaultMetrics = m
	return m
}

// GetMetrics 获取全局 metrics 实例
func GetMetrics() *Metrics {
	if defaultMetrics == nil {
		return Init("geecache")
	}
	return defaultMetrics
}

// RecordRequest 记录请求
func (m *Metrics) RecordRequest(group, status string, duration time.Duration) {
	m.RequestsTotal.WithLabelValues(group, status).Inc()
	m.RequestsDuration.WithLabelValues(group, "total").Observe(duration.Seconds())
}

// RecordCacheHit 记录缓存命中
func (m *Metrics) RecordCacheHit(group string) {
	m.CacheHits.WithLabelValues(group).Inc()
}

// RecordCacheMiss 记录缓存未命中
func (m *Metrics) RecordCacheMiss(group string) {
	m.CacheMisses.WithLabelValues(group).Inc()
}

// SetCacheSize 设置缓存大小
func (m *Metrics) SetCacheSize(group string, size int64) {
	m.CacheSize.WithLabelValues(group).Set(float64(size))
}

// RecordPeerRequest 记录远程节点请求
func (m *Metrics) RecordPeerRequest(peer, status string, duration time.Duration) {
	m.PeerRequests.WithLabelValues(peer, status).Inc()
	m.PeerDuration.WithLabelValues(peer).Observe(duration.Seconds())
}

// RecordPeerError 记录远程节点错误
func (m *Metrics) RecordPeerError(peer, errorType string) {
	m.PeerErrors.WithLabelValues(peer, errorType).Inc()
}

// UpdateSystemMetrics 更新系统指标
func (m *Metrics) UpdateSystemMetrics(goroutines int) {
	m.Goroutines.Set(float64(goroutines))
	m.Uptime.Set(time.Since(startTime).Seconds())
}

// GetCacheHitRateQuery CacheHitRate 计算缓存命中率（需要从 prometheus 查询，这里提供辅助方法）
func (m *Metrics) GetCacheHitRateQuery(group string) string {
	return `rate(geecache_cache_hits_total{group="` + group + `"}[5m]) / 
			(rate(geecache_cache_hits_total{group="` + group + `"}[5m]) + 
			 rate(geecache_cache_misses_total{group="` + group + `"}[5m]))`
}
