package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/lyb88999/geeCache/geeCache"
	"go.uber.org/zap"
)

// API 管理 API
type API struct {
	logger *zap.Logger
	groups map[string]*geeCache.Group
}

// NewAPI 创建管理 API
func NewAPI(logger *zap.Logger) *API {
	if logger == nil {
		logger = zap.L()
	}

	return &API{
		logger: logger.With(zap.String("component", "admin_api")),
		groups: make(map[string]*geeCache.Group),
	}
}

// RegisterGroup 注册缓存组
func (a *API) RegisterGroup(name string, group *geeCache.Group) {
	a.groups[name] = group
	a.logger.Info("registered cache group", zap.String("group", name))
}

// RegisterHandlers 注册 HTTP 处理器
func (a *API) RegisterHandlers(mux *http.ServeMux, prefix string) {
	if prefix == "" {
		prefix = "/admin"
	}

	mux.HandleFunc(prefix+"/stats", a.handleStats)
	mux.HandleFunc(prefix+"/groups", a.handleGroups)
	mux.HandleFunc(prefix+"/cache/clear", a.handleCacheClear)
	mux.HandleFunc(prefix+"/cache/get", a.handleCacheGet)
	mux.HandleFunc(prefix+"/system", a.handleSystem)

	a.logger.Info("registered admin API handlers", zap.String("prefix", prefix))
}

// StatsResponse 统计响应
type StatsResponse struct {
	Groups    map[string]GroupStats `json:"groups"`
	System    SystemStats           `json:"system"`
	Timestamp time.Time             `json:"timestamp"`
}

// GroupStats 组统计
type GroupStats struct {
	Name          string    `json:"name"`
	CacheSize     int64     `json:"cache_size_bytes"`
	CacheMaxBytes int64     `json:"cache_max_bytes"`
	Items         int64     `json:"items"`
	HitRate       float64   `json:"hit_rate"`
	TotalHits     int64     `json:"total_hits"`
	TotalMisses   int64     `json:"total_misses"`
	TotalGets     int64     `json:"total_gets"`
	Loads         int64     `json:"loads"`
	LocalLoads    int64     `json:"local_loads"`
	PeerLoads     int64     `json:"peer_loads"`
	PeerErrors    int64     `json:"peer_errors"`
	LoadSuccess   int64     `json:"load_success"`
	LoadErrors    int64     `json:"load_errors"`
	CreatedAt     time.Time `json:"created_at"`
	UptimeSeconds float64   `json:"uptime_seconds"`
}

// SystemStats 系统统计
type SystemStats struct {
	Goroutines    int              `json:"goroutines"`
	MemStats      runtime.MemStats `json:"mem_stats"`
	Uptime        string           `json:"uptime"`
	UptimeSeconds float64          `json:"uptime_seconds"`
	GoVersion     string           `json:"go_version"`
	NumCPU        int              `json:"num_cpu"`
}

var startTime = time.Now()

// handleStats 处理统计请求
func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := StatsResponse{
		Groups:    make(map[string]GroupStats),
		Timestamp: time.Now(),
	}

	// 收集各组统计
	for name, group := range a.groups {
		groupStats := group.GetStats()
		uptime := time.Since(groupStats.CreatedAt)

		stats.Groups[name] = GroupStats{
			Name:          name,
			CacheSize:     groupStats.CacheBytes,
			CacheMaxBytes: group.CacheBytes(),
			Items:         groupStats.CacheItems,
			HitRate:       groupStats.HitRate(),
			TotalHits:     groupStats.Hits,
			TotalMisses:   groupStats.Misses,
			TotalGets:     groupStats.Gets,
			Loads:         groupStats.Loads,
			LocalLoads:    groupStats.LocalLoads,
			PeerLoads:     groupStats.PeerLoads,
			PeerErrors:    groupStats.PeerErrors,
			LoadSuccess:   groupStats.LoadSuccess,
			LoadErrors:    groupStats.LoadErrors,
			CreatedAt:     groupStats.CreatedAt,
			UptimeSeconds: uptime.Seconds(),
		}
	}

	// 系统统计
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	uptime := time.Since(startTime)
	stats.System = SystemStats{
		Goroutines:    runtime.NumGoroutine(),
		MemStats:      memStats,
		Uptime:        uptime.String(),
		UptimeSeconds: uptime.Seconds(),
		GoVersion:     runtime.Version(),
		NumCPU:        runtime.NumCPU(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		a.logger.Error("failed to encode stats", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// GroupsResponse 组列表响应
type GroupsResponse struct {
	Groups []GroupInfo `json:"groups"`
	Count  int         `json:"count"`
}

// GroupInfo 组信息
type GroupInfo struct {
	Name      string  `json:"name"`
	Items     int64   `json:"items"`
	HitRate   float64 `json:"hit_rate"`
	MaxBytes  int64   `json:"max_bytes"`
	UsedBytes int64   `json:"used_bytes"`
}

// handleGroups 处理获取所有组
func (a *API) handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	groups := make([]GroupInfo, 0, len(a.groups))
	for name, group := range a.groups {
		stats := group.GetStats()
		groups = append(groups, GroupInfo{
			Name:      name,
			Items:     stats.CacheItems,
			HitRate:   stats.HitRate(),
			MaxBytes:  group.CacheBytes(),
			UsedBytes: stats.CacheBytes,
		})
	}

	resp := GroupsResponse{
		Groups: groups,
		Count:  len(groups),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		a.logger.Error("failed to encode groups", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleCacheClear 处理清空缓存
func (a *API) handleCacheClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	groupName := r.URL.Query().Get("group")
	if groupName == "" {
		http.Error(w, "group parameter is required", http.StatusBadRequest)
		return
	}

	group, exists := a.groups[groupName]
	if !exists {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	// 清空缓存
	group.Clear()

	a.logger.Info("cache cleared", zap.String("group", groupName))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "cache cleared",
		"group":   groupName,
	})
}

// handleCacheGet 处理获取缓存值（用于调试）
func (a *API) handleCacheGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	groupName := r.URL.Query().Get("group")
	key := r.URL.Query().Get("key")

	if groupName == "" || key == "" {
		http.Error(w, "group and key parameters are required", http.StatusBadRequest)
		return
	}

	group, exists := a.groups[groupName]
	if !exists {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	value, err := group.Get(key)
	if err != nil {
		a.logger.Warn("failed to get value",
			zap.String("group", groupName),
			zap.String("key", key),
			zap.Error(err))
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"group": groupName,
		"key":   key,
		"value": value.String(),
		"size":  value.Len(),
	})
}

// handleSystem 处理系统信息
func (a *API) handleSystem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	uptime := time.Since(startTime)

	resp := map[string]interface{}{
		"go_version": runtime.Version(),
		"goroutines": runtime.NumGoroutine(),
		"cpus":       runtime.NumCPU(),
		"uptime":     uptime.String(),
		"memory": map[string]interface{}{
			"alloc":         memStats.Alloc,
			"total_alloc":   memStats.TotalAlloc,
			"sys":           memStats.Sys,
			"num_gc":        memStats.NumGC,
			"gc_pause_ns":   memStats.PauseNs[(memStats.NumGC+255)%256],
			"heap_alloc":    memStats.HeapAlloc,
			"heap_sys":      memStats.HeapSys,
			"heap_idle":     memStats.HeapIdle,
			"heap_inuse":    memStats.HeapInuse,
			"heap_released": memStats.HeapReleased,
			"heap_objects":  memStats.HeapObjects,
		},
		"timestamp": time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		a.logger.Error("failed to encode system info", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// HTTPHandler 返回一个 HTTP 处理器
func (a *API) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	a.RegisterHandlers(mux, "/admin")
	return mux
}
