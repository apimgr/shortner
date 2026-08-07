package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testHealthDeps(t *testing.T, sqlDB *sql.DB) *healthDeps {
	t.Helper()
	return &healthDeps{
		sqlDB:     sqlDB,
		dataDir:   t.TempDir(),
		startTime: time.Now().Add(-90 * time.Minute),
		stats:     NewStats(),
		version:   "1.2.3",
		commit:    "abc123",
		buildDate: "2026-01-01",
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCheckDatabaseOK(t *testing.T) {
	db := openTestDB(t)
	if got := checkDatabase(context.Background(), db); got != "ok" {
		t.Errorf("checkDatabase() = %q, want ok", got)
	}
}

func TestCheckDatabaseNil(t *testing.T) {
	if got := checkDatabase(context.Background(), nil); got != "error" {
		t.Errorf("checkDatabase(nil) = %q, want error", got)
	}
}

func TestCheckDatabaseClosed(t *testing.T) {
	db := openTestDB(t)
	db.Close()
	if got := checkDatabase(context.Background(), db); got != "error" {
		t.Errorf("checkDatabase(closed) = %q, want error", got)
	}
}

func TestCheckDiskOK(t *testing.T) {
	if got := checkDisk(t.TempDir()); got != "ok" {
		t.Errorf("checkDisk() = %q, want ok", got)
	}
}

func TestCheckDiskMissingDir(t *testing.T) {
	if got := checkDisk("/nonexistent/does/not/exist"); got != "error" {
		t.Errorf("checkDisk(missing) = %q, want error", got)
	}
}

func TestCheckDiskEmptyPath(t *testing.T) {
	if got := checkDisk(""); got != "error" {
		t.Errorf("checkDisk(\"\") = %q, want error", got)
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0d 0h 0m"},
		{90 * time.Minute, "0d 1h 30m"},
		{25 * time.Hour, "1d 1h 0m"},
		{48*time.Hour + 5*time.Minute, "2d 0h 5m"},
	}
	for _, tt := range tests {
		if got := formatUptime(tt.d); got != tt.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestBuildHealthResponseHealthy(t *testing.T) {
	h := testHealthDeps(t, openTestDB(t))
	resp := h.buildHealthResponse(context.Background())

	if resp.Status != "healthy" {
		t.Errorf("Status = %q, want healthy", resp.Status)
	}
	if resp.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", resp.Version)
	}
	if resp.Checks.Database != "ok" {
		t.Errorf("Checks.Database = %q, want ok", resp.Checks.Database)
	}
	if resp.Checks.Disk != "ok" {
		t.Errorf("Checks.Disk = %q, want ok", resp.Checks.Disk)
	}
	if resp.Project.Name != "Shortner" {
		t.Errorf("Project.Name = %q, want Shortner", resp.Project.Name)
	}
}

func TestBuildHealthResponseUnhealthyOnDBFailure(t *testing.T) {
	db := openTestDB(t)
	db.Close()
	h := testHealthDeps(t, db)
	resp := h.buildHealthResponse(context.Background())

	if resp.Status != "unhealthy" {
		t.Errorf("Status = %q, want unhealthy", resp.Status)
	}
}

func TestHealthHandlerJSON(t *testing.T) {
	h := testHealthDeps(t, openTestDB(t))
	req := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()

	h.healthHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if resp.Status != "healthy" {
		t.Errorf("Status = %q, want healthy", resp.Status)
	}
}

func TestHealthHandlerText(t *testing.T) {
	h := testHealthDeps(t, openTestDB(t))
	req := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
	req.Header.Set("User-Agent", "curl/8.0")
	rec := httptest.NewRecorder()

	h.healthHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", ct)
	}
	if !strings.Contains(rec.Body.String(), "status: healthy") {
		t.Errorf("body = %q, want to contain \"status: healthy\"", rec.Body.String())
	}
}

func TestHealthHandlerUnhealthyReturns503(t *testing.T) {
	db := openTestDB(t)
	db.Close()
	h := testHealthDeps(t, db)
	req := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()

	h.healthHandler()(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
