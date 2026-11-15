package httpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestPWARoutesServeEmbeddedManifestAndOfflinePage(t *testing.T) {
	router := chi.NewRouter()
	registerPWARoutes(router, "1.2.3", "abc1234")

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/manifest.json", contentType: "application/manifest+json", contains: `"display": "standalone"`},
		{path: "/offline.html", contentType: "text/html; charset=utf-8", contains: "You are offline"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if !strings.Contains(rec.Body.String(), tt.contains) {
				t.Errorf("body does not contain %q", tt.contains)
			}
			if tt.path == "/manifest.json" && !strings.Contains(rec.Body.String(), `"src": "/static/icons/icon-180.png"`) {
				t.Error("manifest does not include the required 180x180 icon")
			}
		})
	}
}

func TestPWAServiceWorkerUsesBuildStampAndNoCache(t *testing.T) {
	router := chi.NewRouter()
	registerPWARoutes(router, "1.2.3", "abc1234")
	req := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "shortner-cache-v1.2.3-abc1234") {
		t.Errorf("service worker does not include the build cache stamp")
	}
	if strings.Contains(rec.Body.String(), "__BUILD_STAMP__") {
		t.Errorf("service worker contains an unresolved build-stamp placeholder")
	}
	if !strings.Contains(rec.Body.String(), "!/\\b(no-store|private)\\b/.test(cacheControl)") {
		t.Errorf("service worker does not avoid caching private/no-store HTML responses")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

func TestPWAHeadRoutesHaveEmptyBodiesAndCorrectLengths(t *testing.T) {
	router := chi.NewRouter()
	registerPWARoutes(router, "1.2.3", "abc1234")
	for _, path := range []string{"/manifest.json", "/offline.html", "/sw.js"} {
		t.Run(path, func(t *testing.T) {
			get := httptest.NewRecorder()
			router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path, nil))
			head := httptest.NewRecorder()
			router.ServeHTTP(head, httptest.NewRequest(http.MethodHead, path, nil))
			if head.Code != http.StatusOK {
				t.Fatalf("HEAD status = %d", head.Code)
			}
			if head.Body.Len() != 0 {
				t.Fatalf("HEAD wrote %d body bytes", head.Body.Len())
			}
			if got, want := head.Header().Get("Content-Length"), fmt.Sprint(get.Body.Len()); got != want {
				t.Errorf("HEAD Content-Length = %q, want %q", got, want)
			}
		})
	}
}

func TestStaticAssetCacheRequiresCurrentBuildStamp(t *testing.T) {
	h := staticAssetHandler("1.2.3-abc1234")
	for _, tt := range []struct {
		name   string
		target string
		want   string
	}{
		{name: "current stamp", target: "/static/js/app.js?v=1.2.3-abc1234", want: "public, max-age=31536000, immutable"},
		{name: "missing stamp", target: "/static/js/app.js", want: "no-cache"},
		{name: "stale stamp", target: "/static/js/app.js?v=1.2.2-abc1234", want: "no-cache"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}
