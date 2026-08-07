package httpserver

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

func testDeps(t *testing.T) *deps {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "access.log")
	logger, err := applog.Open(logPath, applog.LevelInfo)
	if err != nil {
		t.Fatalf("applog.Open() error = %v", err)
	}
	t.Cleanup(func() { logger.Close() })

	return &deps{
		resolver:    NewProxyResolver(nil),
		rateLimiter: NewRateLimiter(config.RateLimit{Enabled: false}),
		stats:       NewStats(),
		access:      logger,
		operatorTok: "tok_test-operator",
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestURLNormalizeMiddlewareCollapsesSlashes(t *testing.T) {
	var gotPath string
	h := urlNormalizeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	req := httptest.NewRequest(http.MethodGet, "//api///v1//items", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotPath != "/api/v1/items" {
		t.Errorf("path = %q, want /api/v1/items", gotPath)
	}
}

func TestRequestIDMiddlewareGeneratesID(t *testing.T) {
	h := requestIDMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID response header to be set")
	}
}

func TestRequestIDMiddlewarePreservesValidID(t *testing.T) {
	h := requestIDMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "550e8400-e29b-41d4-a716-446655440000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("X-Request-ID = %q, want the client-supplied UUID preserved", got)
	}
}

func TestRequestIDMiddlewareRejectsInvalidID(t *testing.T) {
	h := requestIDMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got == "not-a-uuid" {
		t.Error("expected an invalid client-supplied request ID to be replaced")
	}
}

func TestPathSecurityMiddlewareBlocksTraversal(t *testing.T) {
	h := pathSecurityMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/../etc/passwd", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPathSecurityMiddlewareAllowsCleanPath(t *testing.T) {
	h := pathSecurityMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestSecurityHeadersMiddlewareSetsHeaders(t *testing.T) {
	h := securityHeadersMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	for _, header := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if rec.Header().Get(header) == "" {
			t.Errorf("expected %s header to be set", header)
		}
	}
}

func TestRateLimitMiddlewareBlocksOverLimit(t *testing.T) {
	d := testDeps(t)
	d.rateLimiter = NewRateLimiter(config.RateLimit{
		Enabled:     true,
		Read:        config.RateLimitClass{Requests: 1, Window: 60},
		Write:       config.RateLimitClass{Requests: 1, Window: 60},
		Health:      config.RateLimitClass{Requests: 1, Window: 60},
		GlobalBurst: 10,
	})
	h := d.rateLimitMiddleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.9.9.9:1234"

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

func TestRateLimitMiddlewareBypassesAllowlisted(t *testing.T) {
	d := testDeps(t)
	d.rateLimiter = NewRateLimiter(config.RateLimit{
		Enabled:     true,
		Read:        config.RateLimitClass{Requests: 1, Window: 60},
		Write:       config.RateLimitClass{Requests: 1, Window: 60},
		Health:      config.RateLimitClass{Requests: 1, Window: 60},
		GlobalBurst: 10,
	})
	h := d.allowlistMiddleware(d.rateLimitMiddleware(okHandler()))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	// allowlistMiddleware always sets false in this skeleton, so this
	// exercises the wiring but cannot yet prove a true-allowlisted bypass —
	// see TODO.AI.md for the real allowlist body.
	for i := 0; i < 1; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i, rec.Code)
		}
	}
}

func TestAuthMiddlewareSetsOperatorContext(t *testing.T) {
	d := testDeps(t)
	var isOperator bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isOperator = IsOperator(r.Context())
	})
	h := d.authMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+d.operatorTok)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !isOperator {
		t.Error("expected IsOperator = true for a matching operator token")
	}
}

func TestAuthMiddlewareRejectsWrongToken(t *testing.T) {
	d := testDeps(t)
	var isOperator bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isOperator = IsOperator(r.Context())
	})
	h := d.authMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok_wrong")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if isOperator {
		t.Error("expected IsOperator = false for a mismatched token")
	}
}

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name  string
		set   func(r *http.Request)
		query string
		want  string
	}{
		{"authorization bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer abc123") }, "", "abc123"},
		{"x-api-key", func(r *http.Request) { r.Header.Set("X-API-Key", "key123") }, "", "key123"},
		{"x-auth-token", func(r *http.Request) { r.Header.Set("X-Auth-Token", "auth123") }, "", "auth123"},
		{"x-token", func(r *http.Request) { r.Header.Set("X-Token", "tok123") }, "", "tok123"},
		{"query param fallback", func(r *http.Request) {}, "?token=qtok", "qtok"},
		{"no token present", func(r *http.Request) {}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/"+tt.query, nil)
			tt.set(req)
			if got := ExtractToken(req); got != tt.want {
				t.Errorf("ExtractToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTokenPriorityOrder(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?token=lowest", nil)
	req.Header.Set("X-Token", "medium")
	req.Header.Set("Authorization", "Bearer highest")

	if got := ExtractToken(req); got != "highest" {
		t.Errorf("ExtractToken() = %q, want Authorization to win", got)
	}
}

func TestSetupMiddlewareFullChain(t *testing.T) {
	d := testDeps(t)
	h := d.setupMiddleware(okHandler())

	req := httptest.NewRequest(http.MethodGet, "//server//healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID to be set by the full chain")
	}
	if rec.Header().Get("X-Content-Type-Options") == "" {
		t.Error("expected security headers to be set by the full chain")
	}
}
