package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/mode"
)

func TestBrowserErrorsUseSharedHTMLPage(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.branding = newBrandingStateStore(fd.cfg)
	handler := frontendErrorContextMiddleware(fd)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
	}))
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("browser error = %d %q, want HTML 404", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, marker := range []string{"class=\"skip-link\"", "id=\"main-content\"", "class=\"page-title\"", "class=\"section-card\""} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("browser error page is missing %s", marker)
		}
	}
}

func TestDebugError500RouteExistsOnlyInDebug(t *testing.T) {
	mode.SetDebugEnabled(false)
	production := New(Options{Config: config.Default("")})
	rec := httptest.NewRecorder()
	production.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/error500", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("production status = %d, want 404", rec.Code)
	}
	mode.SetDebugEnabled(true)
	defer mode.SetDebugEnabled(false)
	cfg := config.Default("")
	cfg.Server.Token = "adm_test"
	debug := New(Options{Config: cfg})
	req := httptest.NewRequest(http.MethodGet, "/debug/error500", nil)
	req.Header.Set("Accept", "text/html")
	rec = httptest.NewRecorder()
	debug.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated debug response = %d, want 401", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/debug/error500", nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Authorization", "Bearer adm_test")
	rec = httptest.NewRecorder()
	debug.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("debug response = %d %q, want themed HTML 500", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestPprofEndpointsRequireOperatorToken(t *testing.T) {
	mode.SetDebugEnabled(true)
	defer mode.SetDebugEnabled(false)
	cfg := config.Default("")
	cfg.Server.Token = "adm_test"
	debug := New(Options{Config: cfg})
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/vars"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			debug.httpServer.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
			}
			req = httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer adm_test")
			rec = httptest.NewRecorder()
			debug.httpServer.Handler.ServeHTTP(rec, req)
			if rec.Code == http.StatusMovedPermanently {
				location := rec.Header().Get("Location")
				if location == "" {
					t.Fatal("canonical debug redirect has no Location")
				}
				req = httptest.NewRequest(http.MethodGet, location, nil)
				req.Header.Set("Authorization", "Bearer adm_test")
				rec = httptest.NewRecorder()
				debug.httpServer.Handler.ServeHTTP(rec, req)
			}
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "adm_test") {
				t.Fatalf("authenticated response = %d, body contains secret: %v", rec.Code, strings.Contains(rec.Body.String(), "adm_test"))
			}
		})
	}
}

func TestDebugRoutesRejectEmptyOperatorToken(t *testing.T) {
	mode.SetDebugEnabled(true)
	defer mode.SetDebugEnabled(false)
	debug := New(Options{Config: config.Default("")})
	req := httptest.NewRequest(http.MethodGet, "/debug/vars", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	debug.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("empty configured operator token accepted: status = %d", rec.Code)
	}
}

func TestAPIErrorsStayJSONForHTMLAccept(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	handler := frontendErrorContextMiddleware(fd)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("API error = %d %q, want JSON 404", rec.Code, rec.Header().Get("Content-Type"))
	}
}
