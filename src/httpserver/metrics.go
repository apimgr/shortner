// The PART 20 metrics endpoints: /server/metrics[/{service}] and its
// /api/{api_version}, /api, and /metrics (root) aliases. Every route is
// mandatory-bearer-token-gated per service (prometheus, grafana, loki) —
// see AI.md PART 20 "Authentication". Metrics stay internal-only: never
// advertised, never in healthz FeaturesInfo.
package httpserver

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/metrics"
)

// metricsProjectName is the metric-name prefix and Grafana dashboard
// title, per AI.md PART 20 "Metric Naming Conventions" -> "Prefix":
// "{project_name}_" -> this project's frozen name.
const metricsProjectName = "shortner"

// metricsAuth wraps a service handler with the mandatory per-service
// bearer-token check, per AI.md PART 20 "Authentication": header-only
// `Authorization: Bearer {token}` (query-string tokens are forbidden),
// constant-time comparison, empty configured token -> 403 with an empty
// body, and the `allow_unauthenticated` firewalled escape hatch.
func metricsAuth(cfg config.Metrics, token string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Auth.AllowUnauthenticated {
			h(w, r)
			return
		}
		if token == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		auth := r.Header.Get("Authorization")
		want := "Bearer " + token
		if len(auth) != len(want) || subtle.ConstantTimeCompare([]byte(auth), []byte(want)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// lokiEntry is one line of the Loki push-API stream JSON body, per
// AI.md PART 20 "Service Semantics" -> loki.
type lokiStreams struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

// lokiHandler serves recent buffered log entries in Loki stream format,
// bounded by cfg.Loki.MaxEntries/MaxAge. log may be nil (no access
// logger configured), in which case it serves an empty stream set.
func lokiHandler(cfg config.Metrics, log *applog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		maxAge, err := config.ParseDuration(cfg.Loki.MaxAge, time.Hour)
		if err != nil {
			maxAge = time.Hour
		}
		streams := lokiStreams{Streams: []lokiStream{}}
		if log != nil {
			entries := log.Recent(cfg.Loki.MaxEntries, maxAge)
			if len(entries) > 0 {
				values := make([][2]string, 0, len(entries))
				for _, e := range entries {
					values = append(values, [2]string{fmt.Sprintf("%d", e.Time.UnixNano()), e.Line})
				}
				streams.Streams = append(streams.Streams, lokiStream{
					Stream: map[string]string{"app": metricsProjectName, "level": "mixed"},
					Values: values,
				})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(streams)
	}
}

// grafanaDashboard is the importable dashboard JSON served by
// /server/metrics/grafana, per AI.md PART 20 "Grafana Dashboard" —
// panels covering every exported metric category with {project_name}
// substituted for this project's frozen name.
func grafanaDashboardHandler() http.HandlerFunc {
	p := metricsProjectName
	dashboard := map[string]any{
		"title": p + " Metrics",
		"panels": []map[string]any{
			{"title": "Request Rate", "type": "graph", "targets": []map[string]string{
				{"expr": fmt.Sprintf("sum(rate(%s_http_requests_total[5m]))", p)},
			}},
			{"title": "Error Rate", "type": "graph", "targets": []map[string]string{
				{"expr": fmt.Sprintf(`sum(rate(%s_http_requests_total{status=~"5.."}[5m])) / sum(rate(%s_http_requests_total[5m]))`, p, p)},
			}},
			{"title": "Latency (p50, p95, p99)", "type": "graph", "targets": []map[string]string{
				{"expr": fmt.Sprintf("histogram_quantile(0.50, rate(%s_http_request_duration_seconds_bucket[5m]))", p), "legendFormat": "p50"},
				{"expr": fmt.Sprintf("histogram_quantile(0.95, rate(%s_http_request_duration_seconds_bucket[5m]))", p), "legendFormat": "p95"},
				{"expr": fmt.Sprintf("histogram_quantile(0.99, rate(%s_http_request_duration_seconds_bucket[5m]))", p), "legendFormat": "p99"},
			}},
			{"title": "Active Requests", "type": "stat", "targets": []map[string]string{
				{"expr": p + "_http_active_requests"},
			}},
			{"title": "Database Connections", "type": "graph", "targets": []map[string]string{
				{"expr": p + "_db_connections_open", "legendFormat": "open"},
				{"expr": p + "_db_connections_in_use", "legendFormat": "in_use"},
			}},
			{"title": "Cache Hit Rate", "type": "graph", "targets": []map[string]string{
				{"expr": fmt.Sprintf("sum(rate(%s_cache_hits_total[5m])) / (sum(rate(%s_cache_hits_total[5m])) + sum(rate(%s_cache_misses_total[5m])))", p, p, p)},
			}},
			{"title": "Memory Usage", "type": "gauge", "targets": []map[string]string{
				{"expr": p + "_system_memory_usage_percent"},
			}},
			{"title": "Goroutines", "type": "graph", "targets": []map[string]string{
				{"expr": p + "_go_goroutines"},
			}},
			{"title": "Uptime", "type": "stat", "targets": []map[string]string{
				{"expr": p + "_app_uptime_seconds"},
			}},
		},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dashboard)
	}
}

// RegisterMetricsRoutes mounts /server/metrics[/{service}] and its
// /api/{api_version}, /api, and /metrics (root, gated on
// cfg.Root.Enabled) aliases onto r, per AI.md PART 20 "Endpoints" — all
// aliases invoke the SAME handlers, never a redirect. m is nil when
// server.metrics.enabled is false, in which case no routes are mounted.
// Disabled-service (empty token) reasons are logged once here, to log
// files only, per AI.md PART 20 "Empty token = service disabled".
func RegisterMetricsRoutes(r chi.Router, cfg config.Metrics, m *metrics.Metrics, log *applog.Logger) {
	if m == nil {
		return
	}

	if !cfg.Auth.AllowUnauthenticated && log != nil {
		for name, tok := range map[string]string{
			"prometheus": cfg.Auth.Tokens.Prometheus,
			"grafana":    cfg.Auth.Tokens.Grafana,
			"loki":       cfg.Auth.Tokens.Loki,
		} {
			if tok == "" {
				_ = log.WriteLine(applog.LevelWarn, fmt.Sprintf("metrics: %s service disabled (no token configured)\n", name))
			}
		}
	}

	prom := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}).ServeHTTP)
	grafana := metricsAuth(cfg, cfg.Auth.Tokens.Grafana, grafanaDashboardHandler())
	loki := metricsAuth(cfg, cfg.Auth.Tokens.Loki, lokiHandler(cfg, log))

	mount := func(reg func(pattern string, h http.HandlerFunc), prefix string) {
		reg(prefix, prom)
		reg(prefix+"/prometheus", prom)
		reg(prefix+"/grafana", grafana)
		reg(prefix+"/loki", loki)
	}

	mount(r.Get, "/server/metrics")
	mount(r.Get, "/api/metrics")

	if cfg.Root.Enabled {
		mount(r.Get, "/metrics")
	}
}

// RegisterVersionedMetricsRoutes mounts /server/metrics[/{service}] onto
// v, the router already scoped to "/api/{api_version}" (AI.md PART 20
// "Versioned API path — same handlers"). Call alongside
// RegisterMetricsRoutes, which covers the other three alias groups.
func RegisterVersionedMetricsRoutes(v chi.Router, cfg config.Metrics, m *metrics.Metrics, log *applog.Logger) {
	if m == nil {
		return
	}
	prom := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{}).ServeHTTP)
	grafana := metricsAuth(cfg, cfg.Auth.Tokens.Grafana, grafanaDashboardHandler())
	loki := metricsAuth(cfg, cfg.Auth.Tokens.Loki, lokiHandler(cfg, log))

	v.Get("/server/metrics", prom)
	v.Get("/server/metrics/prometheus", prom)
	v.Get("/server/metrics/grafana", grafana)
	v.Get("/server/metrics/loki", loki)
}
