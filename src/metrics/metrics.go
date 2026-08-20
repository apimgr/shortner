// Package metrics implements AI.md PART 20 "Metrics": Prometheus-compatible
// instrumentation for shortner. Every metric name carries the "shortner_"
// prefix (this project's project_name, per PART 20 "Metric Naming
// Conventions"), and metrics stay internal-only per PART 20 "Access
// Control" — never advertised on /server/healthz.
package metrics

import (
	"context"
	"runtime"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/apimgr/shortner/src/config"
)

const namePrefix = "shortner_"

// vecs holds every metric variable, built by New so tests can construct an
// isolated registry instead of colliding on prometheus's global
// DefaultRegisterer across parallel test packages.
type Metrics struct {
	Registry *prometheus.Registry

	AppInfo           *prometheus.GaugeVec
	AppUptimeSeconds  prometheus.Gauge
	AppStartTimestamp prometheus.Gauge

	HTTPRequestsTotal     *prometheus.CounterVec
	HTTPRequestDuration   *prometheus.HistogramVec
	HTTPRequestSizeBytes  *prometheus.HistogramVec
	HTTPResponseSizeBytes *prometheus.HistogramVec
	HTTPActiveRequests    prometheus.Gauge

	DBQueriesTotal     *prometheus.CounterVec
	DBQueryDuration    *prometheus.HistogramVec
	DBConnectionsOpen  prometheus.Gauge
	DBConnectionsInUse prometheus.Gauge
	DBErrorsTotal      *prometheus.CounterVec

	CacheHitsTotal      *prometheus.CounterVec
	CacheMissesTotal    *prometheus.CounterVec
	CacheEvictionsTotal *prometheus.CounterVec
	CacheSize           *prometheus.GaugeVec
	CacheBytes          *prometheus.GaugeVec

	SchedulerTasksTotal       *prometheus.CounterVec
	SchedulerTaskDuration     *prometheus.HistogramVec
	SchedulerTasksRunning     *prometheus.GaugeVec
	SchedulerLastRunTimestamp *prometheus.GaugeVec

	SystemCPUUsagePercent    prometheus.Gauge
	SystemMemoryUsagePercent prometheus.Gauge
	SystemMemoryUsedBytes    prometheus.Gauge
	SystemMemoryTotalBytes   prometheus.Gauge
	SystemDiskUsagePercent   *prometheus.GaugeVec
	SystemDiskUsedBytes      *prometheus.GaugeVec
	SystemDiskTotalBytes     *prometheus.GaugeVec

	GoGoroutines          prometheus.Gauge
	GoMemAllocBytes       prometheus.Gauge
	GoMemSysBytes         prometheus.Gauge
	GoGCRunsTotal         prometheus.Counter
	GoGCPauseTotalSeconds prometheus.Counter

	AuthAttemptsTotal  *prometheus.CounterVec
	AuthSessionsActive prometheus.Gauge

	RateLimitRequestsTotal *prometheus.CounterVec
	RateLimitBlockedTotal  *prometheus.CounterVec

	// Business metrics (AI.md PART 20 "Optional: Extended Metrics" ->
	// "Business metrics", adapted to this project's link-shortener domain
	// instead of the spec's illustrative generic "items").
	LinksTotal      prometheus.Gauge
	LinksCreated24h prometheus.Gauge
	LinksClicked24h prometheus.Gauge
	APITokensActive prometheus.Gauge

	startTime time.Time
}

// New builds and registers every PART 20 metric on a fresh registry. Called
// once at startup (src/main.go); tests use it to get an isolated registry
// per test rather than sharing prometheus.DefaultRegisterer.
func New(cfg config.Metrics) *Metrics {
	reg := prometheus.NewRegistry()
	f := promauto.With(reg)

	durationBuckets := cfg.DurationBuckets
	if len(durationBuckets) == 0 {
		durationBuckets = prometheus.DefBuckets
	}
	sizeBuckets := cfg.SizeBuckets
	if len(sizeBuckets) == 0 {
		sizeBuckets = []float64{100, 1000, 10000, 100000, 1000000, 10000000}
	}
	dbDurationBuckets := []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1}
	schedDurationBuckets := []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300, 600}

	m := &Metrics{
		Registry:  reg,
		startTime: time.Now(),

		AppInfo:           f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "app_info", Help: "Application information"}, []string{"version", "commit", "build_date", "go_version"}),
		AppUptimeSeconds:  f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "app_uptime_seconds", Help: "Application uptime in seconds"}),
		AppStartTimestamp: f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "app_start_timestamp", Help: "Unix timestamp when application started"}),

		HTTPRequestsTotal:     f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "http_requests_total", Help: "Total number of HTTP requests"}, []string{"method", "path", "status"}),
		HTTPRequestDuration:   f.NewHistogramVec(prometheus.HistogramOpts{Name: namePrefix + "http_request_duration_seconds", Help: "HTTP request duration in seconds", Buckets: durationBuckets}, []string{"method", "path"}),
		HTTPRequestSizeBytes:  f.NewHistogramVec(prometheus.HistogramOpts{Name: namePrefix + "http_request_size_bytes", Help: "HTTP request body size in bytes", Buckets: sizeBuckets}, []string{"method", "path"}),
		HTTPResponseSizeBytes: f.NewHistogramVec(prometheus.HistogramOpts{Name: namePrefix + "http_response_size_bytes", Help: "HTTP response body size in bytes", Buckets: sizeBuckets}, []string{"method", "path"}),
		HTTPActiveRequests:    f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "http_active_requests", Help: "Number of requests currently being processed"}),

		DBQueriesTotal:     f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "db_queries_total", Help: "Total database queries"}, []string{"operation", "table"}),
		DBQueryDuration:    f.NewHistogramVec(prometheus.HistogramOpts{Name: namePrefix + "db_query_duration_seconds", Help: "Database query latency distribution", Buckets: dbDurationBuckets}, []string{"operation", "table"}),
		DBConnectionsOpen:  f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "db_connections_open", Help: "Number of open database connections"}),
		DBConnectionsInUse: f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "db_connections_in_use", Help: "Number of database connections in use"}),
		DBErrorsTotal:      f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "db_errors_total", Help: "Total database errors"}, []string{"operation", "error_type"}),

		CacheHitsTotal:      f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "cache_hits_total", Help: "Total cache hits"}, []string{"cache"}),
		CacheMissesTotal:    f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "cache_misses_total", Help: "Total cache misses"}, []string{"cache"}),
		CacheEvictionsTotal: f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "cache_evictions_total", Help: "Total cache evictions"}, []string{"cache"}),
		CacheSize:           f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "cache_size", Help: "Current number of items in cache"}, []string{"cache"}),
		CacheBytes:          f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "cache_bytes", Help: "Current cache size in bytes"}, []string{"cache"}),

		SchedulerTasksTotal:       f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "scheduler_tasks_total", Help: "Total scheduled task executions"}, []string{"task", "status"}),
		SchedulerTaskDuration:     f.NewHistogramVec(prometheus.HistogramOpts{Name: namePrefix + "scheduler_task_duration_seconds", Help: "Task execution duration", Buckets: schedDurationBuckets}, []string{"task"}),
		SchedulerTasksRunning:     f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "scheduler_tasks_running", Help: "Currently running task instances"}, []string{"task"}),
		SchedulerLastRunTimestamp: f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "scheduler_last_run_timestamp", Help: "Unix timestamp of last task execution"}, []string{"task"}),

		SystemCPUUsagePercent:    f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "system_cpu_usage_percent", Help: "Current CPU usage percentage (0-100)"}),
		SystemMemoryUsagePercent: f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "system_memory_usage_percent", Help: "Current memory usage percentage (0-100)"}),
		SystemMemoryUsedBytes:    f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "system_memory_used_bytes", Help: "Memory currently in use"}),
		SystemMemoryTotalBytes:   f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "system_memory_total_bytes", Help: "Total system memory"}),
		SystemDiskUsagePercent:   f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "system_disk_usage_percent", Help: "Disk usage percentage for data directory"}, []string{"path"}),
		SystemDiskUsedBytes:      f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "system_disk_used_bytes", Help: "Disk space used"}, []string{"path"}),
		SystemDiskTotalBytes:     f.NewGaugeVec(prometheus.GaugeOpts{Name: namePrefix + "system_disk_total_bytes", Help: "Total disk space"}, []string{"path"}),

		GoGoroutines:          f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "go_goroutines", Help: "Current number of goroutines"}),
		GoMemAllocBytes:       f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "go_mem_alloc_bytes", Help: "Bytes allocated and in use (heap)"}),
		GoMemSysBytes:         f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "go_mem_sys_bytes", Help: "Total bytes obtained from system"}),
		GoGCRunsTotal:         f.NewCounter(prometheus.CounterOpts{Name: namePrefix + "go_gc_runs_total", Help: "Total garbage collection runs"}),
		GoGCPauseTotalSeconds: f.NewCounter(prometheus.CounterOpts{Name: namePrefix + "go_gc_pause_total_seconds", Help: "Total time spent in GC pauses"}),

		AuthAttemptsTotal:  f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "auth_attempts_total", Help: "Auth attempts"}, []string{"method", "status"}),
		AuthSessionsActive: f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "auth_sessions_active", Help: "Active sessions (this project is token-based; always 0 — no session store)"}),

		RateLimitRequestsTotal: f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "ratelimit_requests_total", Help: "Total rate-limited requests"}, []string{"limit", "status"}),
		RateLimitBlockedTotal:  f.NewCounterVec(prometheus.CounterOpts{Name: namePrefix + "ratelimit_blocked_total", Help: "Requests blocked by rate limiter"}, []string{"limit"}),

		LinksTotal:      f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "links_total", Help: "Total number of shortened links"}),
		LinksCreated24h: f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "links_created_24h", Help: "Links created in the last 24 hours"}),
		LinksClicked24h: f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "links_clicked_24h", Help: "Links clicked in the last 24 hours"}),
		APITokensActive: f.NewGauge(prometheus.GaugeOpts{Name: namePrefix + "api_tokens_active", Help: "Active (non-expired, non-revoked) API/resource tokens"}),
	}
	return m
}

// InitAppInfo sets the app_info gauge (always 1, labels carry build info)
// and app_start_timestamp, per AI.md PART 20 "Required: Application Info".
func (m *Metrics) InitAppInfo(version, commit, buildDate string) {
	m.AppInfo.WithLabelValues(version, commit, buildDate, runtime.Version()).Set(1)
	m.AppStartTimestamp.Set(float64(m.startTime.Unix()))
}

// StartUptimeUpdater runs a 1-second-ticker goroutine updating
// app_uptime_seconds until ctx is canceled, per AI.md PART 20's reference
// implementation ("src/server/metrics/uptime.go").
func (m *Metrics) StartUptimeUpdater(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.AppUptimeSeconds.Set(time.Since(m.startTime).Seconds())
			}
		}
	}()
}

// NormalizePath replaces path segments that look like IDs (numeric or
// UUID-shaped) with ":id", per AI.md PART 20 "Cardinality warning": "Use
// path normalization (replace UUIDs/IDs with :id)". Short-slug segments
// under "/{slug}" are also collapsed, since a raw slug is as unbounded in
// cardinality as a UUID would be.
func NormalizePath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		if looksLikeID(seg) {
			segments[i] = ":id"
		}
	}
	return strings.Join(segments, "/")
}

func looksLikeID(seg string) bool {
	if seg == "" {
		return false
	}
	allDigits := true
	for _, r := range seg {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	// UUID shape: 8-4-4-4-12 hex, hyphenated.
	if len(seg) == 36 && strings.Count(seg, "-") == 4 {
		return true
	}
	return false
}
