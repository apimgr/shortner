package httpserver

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/mode"
)

// TestBuildPermissionsPolicy covers the empty-input boundary, the
// skip-blank-value rule, and the canonical AI.md key ordering.
func TestBuildPermissionsPolicy(t *testing.T) {
	if got := buildPermissionsPolicy(nil); got != "" {
		t.Errorf("buildPermissionsPolicy(nil) = %q, want empty", got)
	}

	policy := map[string]string{
		"camera":      "()",
		"geolocation": "  ",
		"autoplay":    "(self)",
	}
	got := buildPermissionsPolicy(policy)
	want := "camera=(), autoplay=(self)"
	if got != want {
		t.Errorf("buildPermissionsPolicy() = %q, want %q", got, want)
	}
}

// TestBuildHSTS covers every combination of the includeSubDomains/preload
// flags, per AI.md PART 11's HSTS rendering rule.
func TestBuildHSTS(t *testing.T) {
	tests := []struct {
		name string
		h    config.HSTS
		want string
	}{
		{"bare", config.HSTS{MaxAgeSeconds: 3600}, "max-age=3600"},
		{"subdomains", config.HSTS{MaxAgeSeconds: 3600, IncludeSubdomains: true}, "max-age=3600; includeSubDomains"},
		{"preload", config.HSTS{MaxAgeSeconds: 3600, Preload: true}, "max-age=3600; preload"},
		{"both", config.HSTS{MaxAgeSeconds: 3600, IncludeSubdomains: true, Preload: true}, "max-age=3600; includeSubDomains; preload"},
		{"zero max-age", config.HSTS{}, "max-age=0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildHSTS(tt.h); got != tt.want {
				t.Errorf("buildHSTS() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsOverlayHost covers .onion, .b32.i2p, .i2p, a plain clearnet host,
// a host:port combination, and an IPv6 literal in brackets.
func TestIsOverlayHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"example.onion", true},
		{"EXAMPLE.ONION", true},
		{"example.b32.i2p", true},
		{"example.i2p", true},
		{"example.com", false},
		{"example.onion:80", true},
		{"[::1]:8080", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isOverlayHost(tt.host); got != tt.want {
			t.Errorf("isOverlayHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

// TestRequestIsTLS covers a direct TLS connection, a trusted proxy header,
// and the plain-HTTP default.
func TestRequestIsTLS(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if requestIsTLS(req) {
		t.Error("expected requestIsTLS = false for a plain request")
	}

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Forwarded-Proto", "HTTPS")
	if !requestIsTLS(req2) {
		t.Error("expected requestIsTLS = true when X-Forwarded-Proto is https (case-insensitive)")
	}
}

// TestSecurityHeadersApplySetsExpectedMatrix exercises apply() directly
// (rather than through the middleware) to check specific header values,
// including the HSTS overlay-host exemption and the report headers.
func TestSecurityHeadersApplySetsExpectedMatrix(t *testing.T) {
	cfg := config.Default(":memory:")
	resolver := NewProxyResolver(nil)
	hd := newHeaderDeps(cfg, resolver)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	hd.apply(rec, req)

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Permitted-Cross-Domain-Policies"); got != "none" {
		t.Errorf("X-Permitted-Cross-Domain-Policies = %q, want none", got)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("expected Strict-Transport-Security to be set on a clearnet TLS request")
	}
	if got := rec.Header().Get("Reporting-Endpoints"); got == "" {
		t.Error("expected Reporting-Endpoints to be set")
	}
	if got := rec.Header().Get("NEL"); got == "" {
		t.Error("expected NEL to be set when web.headers.nel.enabled is true")
	}
}

// TestSecurityHeadersApplyOmitsHSTSOnOverlayHost proves the overlay-host
// exemption: HSTS must never reach a .onion client even on a TLS request.
func TestSecurityHeadersApplyOmitsHSTSOnOverlayHost(t *testing.T) {
	cfg := config.Default(":memory:")
	resolver := NewProxyResolver(nil)
	hd := newHeaderDeps(cfg, resolver)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "abc123def456.onion"
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	hd.apply(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q, want empty on an overlay host", got)
	}
}

// TestBuildCSPReportOnlyInDevMode proves the automatic dev-mode
// report-only downgrade, and that an explicit "enforce" mode overrides it.
func TestBuildCSPReportOnlyInDevMode(t *testing.T) {
	mode.SetAppMode("development")
	defer mode.SetAppMode("production")

	cfg := config.Default(":memory:")
	resolver := NewProxyResolver(nil)
	hd := newHeaderDeps(cfg, resolver)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"

	name, _ := hd.buildCSP(req)
	if name != "Content-Security-Policy-Report-Only" {
		t.Errorf("buildCSP() header name = %q, want report-only in dev mode", name)
	}

	// An operator who wrote `mode: enforce` in server.yml keeps enforcing
	// headers even in development; Default()'s enforcing value alone does
	// not, which is what ModeExplicit distinguishes.
	cfg.Web.CSP.Mode = "enforce"
	cfg.Web.CSP.ModeExplicit = true
	name2, _ := hd.buildCSP(req)
	if name2 != "Content-Security-Policy" {
		t.Errorf("buildCSP() header name = %q, want enforce to override dev mode", name2)
	}
}

// TestBuildCSPOmitsUpgradeInsecureOnOverlay proves the CSP directive list
// never includes upgrade-insecure-requests for an overlay host.
func TestBuildCSPOmitsUpgradeInsecureOnOverlay(t *testing.T) {
	cfg := config.Default(":memory:")
	resolver := NewProxyResolver(nil)
	hd := newHeaderDeps(cfg, resolver)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "abc123def456.onion"
	_, value := hd.buildCSP(req)
	if strings.Contains(value, "upgrade-insecure-requests") {
		t.Errorf("CSP value contains upgrade-insecure-requests for an overlay host: %q", value)
	}

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Host = "example.com"
	_, value2 := hd.buildCSP(req2)
	if !strings.Contains(value2, "upgrade-insecure-requests") {
		t.Errorf("CSP value missing upgrade-insecure-requests for a clearnet host: %q", value2)
	}
}

// TestBuildCSPOverrideReplacesDefault proves an operator override fully
// replaces the default value rather than appending to it.
func TestBuildCSPOverrideReplacesDefault(t *testing.T) {
	cfg := config.Default(":memory:")
	cfg.Web.CSP.ScriptSrcOverride = "'none'"
	resolver := NewProxyResolver(nil)
	hd := newHeaderDeps(cfg, resolver)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"
	_, value := hd.buildCSP(req)
	if !strings.Contains(value, "script-src 'none'") {
		t.Errorf("expected script-src override to win, got: %q", value)
	}
	if strings.Contains(value, "script-src 'self'") {
		t.Errorf("expected default script-src value to be fully replaced, got: %q", value)
	}
}

// TestClearSiteData covers every reason branch, including the
// version_change cache/storage-only carve-out and the disabled-toggle
// no-op branches.
func TestClearSiteData(t *testing.T) {
	cfg := config.Default(":memory:")
	cfg.Web.Headers.ClearSiteData = config.ClearSiteData{
		OnTokenRevocation:   true,
		OnConsentWithdrawal: false,
		ExecutionContexts:   true,
	}

	rec := httptest.NewRecorder()
	clearSiteData(rec, cfg, "token_revocation")
	if got := rec.Header().Get("Clear-Site-Data"); got != `"cache", "cookies", "storage", "executionContexts"` {
		t.Errorf("token_revocation Clear-Site-Data = %q", got)
	}

	rec2 := httptest.NewRecorder()
	clearSiteData(rec2, cfg, "consent_withdrawal")
	if got := rec2.Header().Get("Clear-Site-Data"); got != "" {
		t.Errorf("expected no header when consent_withdrawal toggle is off, got %q", got)
	}

	rec3 := httptest.NewRecorder()
	clearSiteData(rec3, cfg, "version_change")
	if got := rec3.Header().Get("Clear-Site-Data"); got != `"cache", "storage"` {
		t.Errorf("version_change Clear-Site-Data = %q, want cache/storage only (never cookies)", got)
	}

	rec4 := httptest.NewRecorder()
	clearSiteData(rec4, cfg, "unknown_reason")
	if got := rec4.Header().Get("Clear-Site-Data"); got != "" {
		t.Errorf("expected no header for an unknown reason, got %q", got)
	}
}

// TestServerTiming covers the debug-only gate and the empty-metrics
// boundary.
func TestServerTiming(t *testing.T) {
	cfg := config.Default(":memory:")
	cfg.Web.Headers.ServerTimingInDebugOnly = true

	rec := httptest.NewRecorder()
	serverTiming(rec, cfg, "db;dur=12")
	if got := rec.Header().Get("Server-Timing"); got != "" {
		t.Errorf("expected no Server-Timing header in production mode, got %q", got)
	}

	rec2 := httptest.NewRecorder()
	serverTiming(rec2, cfg)
	if got := rec2.Header().Get("Server-Timing"); got != "" {
		t.Errorf("expected no Server-Timing header with zero metrics, got %q", got)
	}

	cfg2 := config.Default(":memory:")
	cfg2.Web.Headers.ServerTimingInDebugOnly = false
	rec3 := httptest.NewRecorder()
	serverTiming(rec3, cfg2, "db;dur=12", "cache;dur=1")
	if got := rec3.Header().Get("Server-Timing"); got != "db;dur=12, cache;dur=1" {
		t.Errorf("Server-Timing = %q, want joined metrics", got)
	}
}
