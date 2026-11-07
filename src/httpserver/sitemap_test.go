package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
)

func TestSitemapRoutesServePublicPages(t *testing.T) {
	cfg := config.Default(":memory:")
	sd := &sitemapDeps{cfg: cfg, resolver: NewProxyResolver(nil), startTime: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	r := chi.NewRouter()
	sd.registerSitemapRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	for _, want := range []string{"http://example.com/", "http://example.com/server/privacy", "2026-10-09"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("sitemap missing %q", want)
		}
	}
	for _, excluded := range []string{"/api/", "/server/settings", "/login"} {
		if strings.Contains(rec.Body.String(), excluded) {
			t.Errorf("sitemap contains excluded path %q", excluded)
		}
	}
}

func TestSitemapDisabledIsNotFound(t *testing.T) {
	cfg := config.Default(":memory:")
	cfg.Server.SEO.Sitemap.Enabled = false
	sd := &sitemapDeps{cfg: cfg, resolver: NewProxyResolver(nil)}
	req := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	rec := httptest.NewRecorder()
	sd.sitemapHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestFaviconHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	rec := httptest.NewRecorder()
	faviconHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/x-icon" {
		t.Errorf("Content-Type = %q", got)
	}
	if rec.Body.Len() == 0 {
		t.Error("favicon body is empty")
	}
}

func TestSitemapIncludesLiveLinkAndStatsResource(t *testing.T) {
	sqlDB, err := db.Open(":memory:", db.DefaultPool(), nil)
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	defer sqlDB.Close()
	if _, err := db.CreateLinkCustomSlug(context.Background(), sqlDB, "public-link", "https://example.com", nil); err != nil {
		t.Fatalf("db.CreateLinkCustomSlug() error = %v", err)
	}
	cfg := config.Default(":memory:")
	sd := &sitemapDeps{cfg: cfg, resolver: NewProxyResolver(nil), startTime: time.Now().UTC(), sqlDB: sqlDB}
	req := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	sd.sitemapHandler(rec, req)
	for _, want := range []string{"http://example.com/public-link", "http://example.com/public-link/stats"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("sitemap missing public link URL %q", want)
		}
	}
}

func TestSitemapSplitsPagesAtConfiguredLimit(t *testing.T) {
	cfg := config.Default(":memory:")
	cfg.Server.SEO.Sitemap.MaxURLs = 2
	sd := &sitemapDeps{cfg: cfg, resolver: NewProxyResolver(nil), startTime: time.Now().UTC()}
	r := chi.NewRouter()
	sd.registerSitemapRoutes(r)

	index := httptest.NewRecorder()
	r.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "<sitemapindex") {
		t.Fatalf("index response status/body = %d/%s", index.Code, index.Body.String())
	}
	lastPart := (len(sd.staticSitemapEntries()) + 1) / 2
	last := httptest.NewRecorder()
	r.ServeHTTP(last, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sitemap-%d.xml", lastPart), nil))
	if last.Code != http.StatusOK || !strings.Contains(last.Body.String(), "<urlset") {
		t.Fatalf("last sitemap response status/body = %d/%s", last.Code, last.Body.String())
	}
	missing := httptest.NewRecorder()
	r.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/sitemap-%d.xml", lastPart+1), nil))
	if missing.Code != http.StatusNotFound {
		t.Errorf("out-of-range sitemap status = %d, want 404", missing.Code)
	}
}
