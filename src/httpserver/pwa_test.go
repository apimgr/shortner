package httpserver

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/config"
	serverassets "github.com/apimgr/shortner/src/server"
)

func TestPWARoutesServeManifestWithoutStaticOfflinePage(t *testing.T) {
	router := chi.NewRouter()
	registerPWARoutes(router, "1.2.3", "abc1234")

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/manifest.json", contentType: "application/manifest+json", contains: `"display":"standalone"`},
	}
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/offline.html", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("/offline.html status = %d, want 404", missing.Code)
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
			if tt.path == "/manifest.json" && !strings.Contains(rec.Body.String(), `"src":"/static/icons/icon-180.png?v=1.2.3-abc1234"`) {
				t.Error("manifest does not include a version-stamped 180x180 icon")
			}
		})
	}
}

func TestManifestETagChangesWithBranding(t *testing.T) {
	baseRouter := chi.NewRouter()
	registerPWARoutes(baseRouter, "1.2.3", "abc1234")
	baseRec := httptest.NewRecorder()
	baseRouter.ServeHTTP(baseRec, httptest.NewRequest(http.MethodGet, "/manifest.json", nil))
	baseETag := baseRec.Header().Get("ETag")

	cfg := config.Default("")
	cfg.Server.Branding.Favicon = "/custom-favicon.png"
	brandedRouter := chi.NewRouter()
	registerPWARoutes(brandedRouter, "1.2.3", "abc1234", newBrandingStateStore(cfg))
	request := httptest.NewRequest(http.MethodGet, "/manifest.json", nil)
	request.Header.Set("If-None-Match", baseETag)
	brandedRec := httptest.NewRecorder()
	brandedRouter.ServeHTTP(brandedRec, request)
	if brandedRec.Code != http.StatusOK {
		t.Fatalf("changed manifest with stale ETag returned %d, want 200", brandedRec.Code)
	}
	if got := brandedRec.Header().Get("ETag"); got == baseETag || got == "" {
		t.Fatalf("branded manifest ETag = %q, want a new value distinct from %q", got, baseETag)
	}
	if !strings.Contains(brandedRec.Body.String(), "/branding/favicon?size=180") {
		t.Fatal("branded manifest does not reference the configured favicon")
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
	if !strings.Contains(rec.Body.String(), `response.headers.get("X-Offline-Cache") === "public"`) {
		t.Errorf("service worker does not require the public-page cache signal")
	}
	if !strings.Contains(rec.Body.String(), `cache.delete(request)`) {
		t.Errorf("service worker does not remove a stale cached copy of a private page")
	}
	if !strings.Contains(rec.Body.String(), `shortner-offline-strings`) || !strings.Contains(rec.Body.String(), `offline-strings.json`) {
		t.Errorf("service worker does not load translated offline strings")
	}
	if strings.Contains(rec.Body.String(), `"/offline.html"`) || !strings.Contains(rec.Body.String(), "offlineFallbackResponse()") {
		t.Errorf("service worker must synthesize its offline fallback without a static page")
	}
	for _, required := range []string{
		"const NAV_TIMEOUT_MS = 8000",
		"withTimeout(fetch(request), NAV_TIMEOUT_MS)",
		"if (response.redirected) {",
		"return new Response(\"\", { status: 504, statusText: \"Gateway Timeout\" })",
		"response.ok && !response.redirected",
		"if (!response.ok || response.redirected) throw",
		"return response && response.ok && !response.redirected ? response : null",
	} {
		if !strings.Contains(rec.Body.String(), required) {
			t.Errorf("service worker missing required recovery rule %q", required)
		}
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("ETag"); got != `"1.2.3-abc1234"` {
		t.Errorf("ETag = %q, want build-stamp ETag", got)
	}
	conditional := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	conditional.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	conditionalRec := httptest.NewRecorder()
	router.ServeHTTP(conditionalRec, conditional)
	if conditionalRec.Code != http.StatusNotModified {
		t.Errorf("conditional service worker status = %d, want 304", conditionalRec.Code)
	}
}

func TestPWAUpdateReloadsOnlyAfterAcceptanceAndHasFallback(t *testing.T) {
	appJS, err := fs.ReadFile(serverassets.StaticFS, "static/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(appJS)
	if got := strings.Count(script, `navigator.serviceWorker.addEventListener("controllerchange"`); got != 1 {
		t.Fatalf("controllerchange listeners = %d, want exactly one", got)
	}
	for _, required := range []string{
		`if (updateAccepted) reloadOnce();`,
		`window.setTimeout(reloadOnce, 5000);`,
		`waitingWorker.postMessage({ type: "SKIP_WAITING" });`,
	} {
		if !strings.Contains(script, required) {
			t.Errorf("PWA update script missing required behavior %q", required)
		}
	}
}

func TestPWAHeadRoutesHaveEmptyBodiesAndCorrectLengths(t *testing.T) {
	router := chi.NewRouter()
	registerPWARoutes(router, "1.2.3", "abc1234")
	for _, path := range []string{"/manifest.json", "/sw.js"} {
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
			if got := rec.Header().Get("ETag"); got != `"1.2.3-abc1234"` {
				t.Errorf("ETag = %q, want build-stamp ETag", got)
			}
		})
	}
	conditional := httptest.NewRequest(http.MethodGet, "/static/js/app.js", nil)
	conditional.Header.Set("If-None-Match", `"1.2.3-abc1234"`)
	conditionalRec := httptest.NewRecorder()
	h.ServeHTTP(conditionalRec, conditional)
	if conditionalRec.Code != http.StatusNotModified {
		t.Errorf("conditional static status = %d, want 304", conditionalRec.Code)
	}
}
