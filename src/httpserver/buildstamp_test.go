package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildStampMiddlewareSetsResponseHeader(t *testing.T) {
	handler := buildStampMiddleware("1.2.3-abc1234", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz", nil))
	if got := recorder.Header().Get("X-Build"); got != "1.2.3-abc1234" {
		t.Fatalf("X-Build = %q", got)
	}
}

func TestBuildStampMiddlewareHTMLCacheAndVersionPurge(t *testing.T) {
	handler := buildStampMiddleware("1.2.3-abc1234", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html></html>"))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "shortner_build", Value: "1.2.2-old"})
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("ETag"); got != `"1.2.3-abc1234"` {
		t.Errorf("ETag = %q", got)
	}
	if got := rec.Header().Get("Clear-Site-Data"); got != `"cache", "storage"` {
		t.Errorf("Clear-Site-Data = %q", got)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "shortner_build" || cookies[0].Value != "1.2.3-abc1234" || !cookies[0].Secure || cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Errorf("build stamp cookie = %#v", cookies)
	}
}

func TestBuildStampMiddlewarePurgeSkipsSubresourcesAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		status int
		dest   string
		mode   string
	}{
		{name: "subresource", method: http.MethodGet, status: http.StatusOK, dest: "iframe", mode: "navigate"},
		{name: "head", method: http.MethodHead, status: http.StatusOK},
		{name: "redirect", method: http.MethodGet, status: http.StatusFound},
		{name: "error", method: http.MethodGet, status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := buildStampMiddleware("1.2.3-abc1234", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(tc.status)
			}))
			req := httptest.NewRequest(tc.method, "/", nil)
			req.AddCookie(&http.Cookie{Name: "shortner_build", Value: "old"})
			req.Header.Set("Sec-Fetch-Dest", tc.dest)
			req.Header.Set("Sec-Fetch-Mode", tc.mode)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Header().Get("Clear-Site-Data"); got != "" {
				t.Errorf("Clear-Site-Data = %q, want absent", got)
			}
		})
	}
}
