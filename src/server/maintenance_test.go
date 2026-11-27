package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/security"
)

func TestDatabaseMaintenanceKeepsHealthAvailableAndRejectsWrites(t *testing.T) {
	store, err := db.OpenWithCache("libsql://127.0.0.1:1", db.DefaultPool(), nil, filepath.Join(t.TempDir(), "cache.db"), time.Hour, time.Hour, true)
	if err != nil {
		t.Fatalf("OpenWithCache() error = %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	if !store.Offline() || store.CacheUsable() {
		t.Fatalf("initial recovery status = %+v, want offline without usable cache", store.RecoveryStatus())
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := databaseMaintenanceMiddleware(store)(next)
	for _, path := range []string{"/server/healthz", "/api/v1/server/healthz", "/healthz.txt", "/static/css/public.css", "/locales/en.json"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Errorf("GET %s status = %d, want health request to pass through", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/links", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "30" || rec.Header().Get("X-Maintenance-Mode") != "true" {
		t.Fatalf("maintenance headers = %v", rec.Header())
	}
	if rec.Body.String() == "" {
		t.Fatal("maintenance response body is empty")
	}
}

func TestDatabaseMaintenanceRequiresCacheForReads(t *testing.T) {
	store, err := db.OpenWithCache("libsql://127.0.0.1:1", db.DefaultPool(), nil, filepath.Join(t.TempDir(), "cache.db"), time.Hour, time.Hour, false)
	if err != nil {
		t.Fatalf("OpenWithCache() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	handler := databaseMaintenanceMiddleware(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/server/about", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET without cache status = %d, want 503", rec.Code)
	}
}

func TestCSRFCookieIsNotSignedWithoutDatabaseSecrets(t *testing.T) {
	store, err := db.OpenInMaintenance("libsql://127.0.0.1:1", db.DefaultPool(), nil, filepath.Join(t.TempDir(), "maintenance.db"), time.Hour)
	if err != nil {
		t.Fatalf("OpenInMaintenance() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := config.Default("")
	d := &deps{csrf: cfg.Server.CSRF, resolver: NewProxyResolver(nil), secretKeys: security.NewSecretKeyring(nil, nil, nil, nil), store: store}
	rec := httptest.NewRecorder()
	d.csrfMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("GET status = %d, want 204", rec.Code)
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("offline GET issued cookies without persisted keys: %v", cookies)
	}
}

func TestMaintenanceHealthReportsRecoveryProgress(t *testing.T) {
	store, err := db.OpenInMaintenance("libsql://127.0.0.1:1", db.DefaultPool(), nil, filepath.Join(t.TempDir(), "maintenance.db"), time.Hour)
	if err != nil {
		t.Fatalf("OpenInMaintenance() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	h := &healthDeps{sqlDB: store, store: store, dataDir: t.TempDir(), startTime: time.Now(), stats: NewStats(), version: "test"}
	response := h.buildHealthResponse(context.Background())
	if response.Status != "maintenance" || response.Maintenance == nil {
		t.Fatalf("health response status/maintenance = %q/%+v", response.Status, response.Maintenance)
	}
	if response.Maintenance.Reason != "database_connection" || !response.Maintenance.SelfHealing.Enabled {
		t.Fatalf("maintenance details = %+v", response.Maintenance)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	h.healthHandler()(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want 503", rec.Code)
	}
	var decoded HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if decoded.Status != "maintenance" || decoded.Maintenance == nil {
		t.Fatalf("decoded health response = %+v", decoded)
	}
}
