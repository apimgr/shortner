package httpserver

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

// testPrivacyDeps builds a privacyDeps with a real audit logger so
// auditGPC's write path is actually exercised.
func testPrivacyDeps(t *testing.T) (*privacyDeps, *applog.AuditLogger) {
	t.Helper()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	audit, err := applog.NewAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("applog.NewAuditLogger() error = %v", err)
	}
	t.Cleanup(func() { audit.Close() })

	cfg := config.Default(":memory:")
	pd := &privacyDeps{cfg: cfg, resolver: NewProxyResolver(nil), audit: audit}
	return pd, audit
}

// TestPrivacySignalMiddlewareHonorsSecGPC proves the default config (honor
// Sec-GPC, do not honor DNT) sets the context flag from Sec-GPC and leaves
// DNT ignored.
func TestPrivacySignalMiddlewareHonorsSecGPC(t *testing.T) {
	pd, _ := testPrivacyDeps(t)

	var optOut bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		optOut = IsGPCOptOut(r.Context())
	})
	h := pd.privacySignalMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Sec-GPC", "1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !optOut {
		t.Error("expected IsGPCOptOut = true when Sec-GPC: 1 is honored")
	}
}

// TestPrivacySignalMiddlewareIgnoresDNTByDefault proves DNT is not honored
// unless the operator opts in.
func TestPrivacySignalMiddlewareIgnoresDNTByDefault(t *testing.T) {
	pd, _ := testPrivacyDeps(t)

	var optOut bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		optOut = IsGPCOptOut(r.Context())
	})
	h := pd.privacySignalMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("DNT", "1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if optOut {
		t.Error("expected IsGPCOptOut = false for DNT when honor_dnt is off")
	}
}

// TestPrivacySignalMiddlewareHonorsDNTWhenEnabled proves an operator that
// opts in to HonorDNT gets the opt-out from DNT alone.
func TestPrivacySignalMiddlewareHonorsDNTWhenEnabled(t *testing.T) {
	pd, _ := testPrivacyDeps(t)
	pd.cfg.Web.Headers.HonorDNT = true

	var optOut bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		optOut = IsGPCOptOut(r.Context())
	})
	h := pd.privacySignalMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("DNT", "1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !optOut {
		t.Error("expected IsGPCOptOut = true once honor_dnt is enabled")
	}
}

// TestPrivacySignalMiddlewareNoSignalIsNotConsent proves absence of any
// signal never sets the opt-out flag (an opt-out only ever means true).
func TestPrivacySignalMiddlewareNoSignalIsNotConsent(t *testing.T) {
	pd, _ := testPrivacyDeps(t)

	var optOut bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		optOut = IsGPCOptOut(r.Context())
	})
	h := pd.privacySignalMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if optOut {
		t.Error("expected IsGPCOptOut = false with no privacy signal present")
	}
}

// TestPrivacySignalMiddlewareNilAuditIsSafe proves a nil audit logger
// (e.g. a CLI context) never panics when a signal is honored.
func TestPrivacySignalMiddlewareNilAuditIsSafe(t *testing.T) {
	cfg := config.Default(":memory:")
	pd := &privacyDeps{cfg: cfg, resolver: NewProxyResolver(nil), audit: nil}

	h := pd.privacySignalMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Sec-GPC", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with a nil audit logger", rec.Code)
	}
}

// TestEssentialCookiesOnly proves the single-source-of-truth cookie gate
// matches the context flag exactly.
func TestEssentialCookiesOnly(t *testing.T) {
	pd, _ := testPrivacyDeps(t)
	h := pd.privacySignalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !EssentialCookiesOnly(r) {
			t.Error("expected EssentialCookiesOnly to be true when Sec-GPC opted out")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Sec-GPC", "1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	h2 := pd.privacySignalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if EssentialCookiesOnly(r) {
			t.Error("expected EssentialCookiesOnly to be false with no opt-out signal")
		}
	}))
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	h2.ServeHTTP(httptest.NewRecorder(), req2)
}
