package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/metrics"
)

func testMetricsConfig(prom, grafana, loki string, allowUnauth bool) config.Metrics {
	cfg := config.Default("").Server.Metrics
	cfg.Auth.AllowUnauthenticated = allowUnauth
	cfg.Auth.Tokens.Prometheus = prom
	cfg.Auth.Tokens.Grafana = grafana
	cfg.Auth.Tokens.Loki = loki
	return cfg
}

func TestMetricsAuthEmptyTokenIs403(t *testing.T) {
	cfg := testMetricsConfig("", "", "", false)
	h := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/server/metrics", nil))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty (AI.md PART 20: empty token = 403 empty body)", rec.Body.String())
	}
}

func TestMetricsAuthWrongTokenIs401(t *testing.T) {
	cfg := testMetricsConfig("secret", "", "", false)
	h := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/server/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestMetricsAuthCorrectTokenPasses(t *testing.T) {
	cfg := testMetricsConfig("secret", "", "", false)
	called := false
	h := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/server/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK || !called {
		t.Errorf("status = %d, called = %v, want 200 and true", rec.Code, called)
	}
}

func TestMetricsAuthQueryStringTokenRejected(t *testing.T) {
	cfg := testMetricsConfig("secret", "", "", false)
	h := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/server/metrics?token=secret", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	// Only the Authorization header is checked; a `?token=` query string is
	// never accepted (AI.md PART 20 "query-string tokens are FORBIDDEN"),
	// so this behaves like a missing/wrong header: 401.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (query-string tokens are never accepted)", rec.Code)
	}
}

func TestMetricsAuthAllowUnauthenticatedSkipsCheck(t *testing.T) {
	cfg := testMetricsConfig("", "", "", true)
	called := false
	h := metricsAuth(cfg, cfg.Auth.Tokens.Prometheus, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/server/metrics", nil))

	if rec.Code != http.StatusOK || !called {
		t.Errorf("status = %d, called = %v, want 200 and true with allow_unauthenticated", rec.Code, called)
	}
}

func TestGrafanaDashboardHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	grafanaDashboardHandler()(rec, httptest.NewRequest(http.MethodGet, "/server/metrics/grafana", nil))

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), metricsProjectName+"_http_requests_total") {
		t.Errorf("dashboard body missing expected metric expression: %s", rec.Body.String())
	}
}

func TestLokiHandlerEmptyLoggerServesEmptyStreams(t *testing.T) {
	cfg := testMetricsConfig("", "", "loki-token", false)
	rec := httptest.NewRecorder()
	lokiHandler(cfg, nil)(rec, httptest.NewRequest(http.MethodGet, "/server/metrics/loki", nil))

	if !strings.Contains(rec.Body.String(), `"streams":[]`) {
		t.Errorf("expected empty streams array, got %s", rec.Body.String())
	}
}

func TestRegisterMetricsRoutesNilMetricsMountsNothing(t *testing.T) {
	r := chi.NewRouter()
	RegisterMetricsRoutes(r, testMetricsConfig("t", "t", "t", false), nil, nil)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/server/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when metrics disabled (m == nil)", rec.Code)
	}
}

func TestRegisterMetricsRoutesMountsAliases(t *testing.T) {
	cfg := testMetricsConfig("prom-token", "graf-token", "loki-token", false)
	m := metrics.New(cfg)
	r := chi.NewRouter()
	RegisterMetricsRoutes(r, cfg, m, nil)

	paths := []string{"/server/metrics", "/server/metrics/prometheus", "/api/metrics", "/metrics"}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer prom-token")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", p, rec.Code)
		}
	}
}

func TestRegisterMetricsRoutesRootDisabledSkipsRootAlias(t *testing.T) {
	cfg := testMetricsConfig("prom-token", "", "", false)
	cfg.Root.Enabled = false
	m := metrics.New(cfg)
	r := chi.NewRouter()
	RegisterMetricsRoutes(r, cfg, m, nil)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer prom-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when root.enabled = false", rec.Code)
	}
}
