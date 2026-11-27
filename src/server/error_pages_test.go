package server

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/apperr"
)

func TestRecoverMiddlewareWritesFallbackResponse(t *testing.T) {
	handler := RecoverMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("panic response = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	if !strings.Contains(response.Body.String(), `"error":"SERVER_ERROR"`) {
		t.Fatalf("panic response body = %s", response.Body.String())
	}
}

func TestFrontendTemplateFailureUsesFallbackResponse(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	pageTmplMu.Lock()
	previous, existed := pageTmplCache["error"]
	pageTmplCache["error"] = template.New("broken")
	pageTmplMu.Unlock()
	t.Cleanup(func() {
		pageTmplMu.Lock()
		defer pageTmplMu.Unlock()
		if existed {
			pageTmplCache["error"] = previous
		} else {
			delete(pageTmplCache, "error")
		}
	})
	request := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	request.Header.Set("Accept", "text/html")
	request = request.WithContext(context.WithValue(request.Context(), frontendErrorRendererKey{}, fd))
	response := httptest.NewRecorder()
	if !renderFrontendError(response, request, apperr.New(apperr.CodeServerError)) {
		t.Fatal("renderFrontendError returned false after using its fallback")
	}
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "<main>") {
		t.Fatalf("fallback response = %d %s", response.Code, response.Body.String())
	}
}
