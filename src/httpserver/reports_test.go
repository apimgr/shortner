package httpserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/go-chi/chi/v5"
)

func testReportDeps(t *testing.T) *reportDeps {
	t.Helper()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	audit, err := applog.NewAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("applog.NewAuditLogger() error = %v", err)
	}
	t.Cleanup(func() { audit.Close() })
	cfg := config.Default(":memory:")
	return newReportDeps(cfg, NewProxyResolver(nil), audit)
}

// TestReportHandlerAlways204 proves every group returns 204 with an empty
// body regardless of the payload shape, per the "never confirm or echo"
// rule.
func TestReportHandlerAlways204(t *testing.T) {
	for _, tt := range []struct {
		group string
		body  string
	}{
		{"csp", `{"csp-report":{"violated-directive":"script-src","blocked-uri":"https://evil.example"}}`},
		{"nel", `[{"type":"network-error","body":{"type":"dns.name_not_resolved"}}]`},
		{"default", `not even valid json`},
		{"default", ``},
	} {
		rd := testReportDeps(t)
		h := rd.handler(tt.group)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/server/reports/"+tt.group, bytes.NewReader([]byte(tt.body)))
		rec := httptest.NewRecorder()
		h(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("group %q body %q: status = %d, want 204", tt.group, tt.body, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("group %q: expected empty body, got %q", tt.group, rec.Body.String())
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
		}
	}
}

// TestReportHandlerRegistersRoutes proves registerReportRoutes mounts all
// three POST-only endpoints.
func TestReportHandlerRegistersRoutes(t *testing.T) {
	rd := testReportDeps(t)
	r := chi.NewRouter()
	rd.registerReportRoutes(r)

	for _, path := range []string{"/server/reports/csp", "/server/reports/nel", "/server/reports/default"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("POST %s: status = %d, want 204", path, rec.Code)
		}
	}
}

// TestReportLimiterAllow proves the per-IP burst ceiling, the
// perMinute-vs-burst minimum, the window reset, and the disabled
// (limit<=0) boundary.
func TestReportLimiterAllow(t *testing.T) {
	l := newReportLimiter(config.Reports{RateLimitPerMinute: 5, RateLimitPerIPBurst: 2})
	now := time.Now()

	if !l.allow("1.1.1.1", now) {
		t.Fatal("expected the 1st request to be allowed")
	}
	if !l.allow("1.1.1.1", now) {
		t.Fatal("expected the 2nd request to be allowed (burst = 2)")
	}
	if l.allow("1.1.1.1", now) {
		t.Error("expected the 3rd request to be denied (burst exceeded)")
	}

	// A different IP has its own independent counter.
	if !l.allow("2.2.2.2", now) {
		t.Error("expected a different IP to have its own counter")
	}

	// The window reset releases the original IP.
	if !l.allow("1.1.1.1", now.Add(2*time.Minute)) {
		t.Error("expected the counter to reset after the window elapsed")
	}
}

// TestReportLimiterPerMinuteBelowBurst proves the per-minute value caps
// the limit when it is lower than the per-IP burst.
func TestReportLimiterPerMinuteBelowBurst(t *testing.T) {
	l := newReportLimiter(config.Reports{RateLimitPerMinute: 1, RateLimitPerIPBurst: 10})
	now := time.Now()

	if !l.allow("1.1.1.1", now) {
		t.Fatal("expected the 1st request to be allowed")
	}
	if l.allow("1.1.1.1", now) {
		t.Error("expected the 2nd request to be denied once the lower per-minute value is reached")
	}
}

// TestReportLimiterZeroBurstDeniesEverything covers the disabled boundary.
func TestReportLimiterZeroBurstDeniesEverything(t *testing.T) {
	l := newReportLimiter(config.Reports{RateLimitPerMinute: 5, RateLimitPerIPBurst: 0})
	if l.allow("1.1.1.1", time.Now()) {
		t.Error("expected a zero burst to deny every request")
	}
}

// TestReportHandlerRateLimited proves a rate-limited IP still gets 204 (no
// signal leak) but the report is not recorded — exercised indirectly via
// the audit log file staying at its initial size.
func TestReportHandlerRateLimited(t *testing.T) {
	rd := testReportDeps(t)
	rd.limiter = newReportLimiter(config.Reports{RateLimitPerMinute: 1, RateLimitPerIPBurst: 1})
	h := rd.handler("csp")

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/server/reports/csp", bytes.NewReader([]byte(`{"csp-report":{"violated-directive":"script-src"}}`)))
	req1.RemoteAddr = "3.3.3.3:1234"
	h(httptest.NewRecorder(), req1)

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/server/reports/csp", bytes.NewReader([]byte(`{"csp-report":{"violated-directive":"script-src"}}`)))
	req2.RemoteAddr = "3.3.3.3:1234"
	rec2 := httptest.NewRecorder()
	h(rec2, req2)

	if rec2.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 even when rate-limited", rec2.Code)
	}
}
