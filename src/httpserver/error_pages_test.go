package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/apperr"
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
