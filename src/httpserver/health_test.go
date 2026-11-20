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
		scheduler: testSchedulerReporter(true),
		version:   "1.2.3",
		commit:    "abc123",
		buildDate: "2026-01-01",
	}
}

type testSchedulerReporter bool

func (r testSchedulerReporter) Healthy() bool { return bool(r) }

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
		{0, "0s"},
		{45 * time.Second, "45s"},
		{90 * time.Minute, "1h 30m"},
		{25 * time.Hour, "1d 1h"},
		{48*time.Hour + 5*time.Minute, "2d 5m"},
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

type testI2PReporter struct {
	enabled  bool
	provider string
}

func (r testI2PReporter) Enabled() bool        { return r.enabled }
func (testI2PReporter) Running() bool          { return false }
func (testI2PReporter) Healthy() bool          { return false }
func (testI2PReporter) EepsiteAddress() string { return "" }
func (r testI2PReporter) ProviderName() string { return r.provider }
func (testI2PReporter) Err() string            { return "" }

func TestI2PNoProviderReportsDisabled(t *testing.T) {
	h := &healthDeps{i2p: testI2PReporter{enabled: true, provider: "none"}}
	info, check := h.i2pInfo()
	if info.Enabled || info.Running || info.Status != "disabled" || info.Provider != "none" {
		t.Errorf("i2pInfo() = %#v, want disabled no-provider state", info)
	}
	if check != "" {
		t.Errorf("i2pInfo() check = %q, want omitted", check)
	}
}

func TestSchedulerHealthCheck(t *testing.T) {
	for _, test := range []struct {
		name      string
		reporter  SchedulerReporter
		wantCheck string
	}{
		{name: "running", reporter: testSchedulerReporter(true), wantCheck: "ok"},
		{name: "stopped", reporter: testSchedulerReporter(false), wantCheck: "error"},
		{name: "missing", reporter: nil, wantCheck: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := &healthDeps{scheduler: test.reporter}
			if got := h.schedulerHealth(); got != test.wantCheck {
				t.Errorf("schedulerHealth() = %q, want %q", got, test.wantCheck)
			}
		})
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz", nil)
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

// TestFormatHealthTextCanonicalOrder locks the flattened text output to the
// canonical field order of AI.md PART 13 "Plain Text (Accept: text/plain)".
func TestFormatHealthTextCanonicalOrder(t *testing.T) {
	h := testHealthDeps(t, openTestDB(t))
	body := formatHealthText(h.buildHealthResponse(context.Background()))

	want := []string{
		"project.name: ",
		"project.tagline: ",
		"project.description: ",
		"status: ",
		"version: ",
		"go_version: ",
		"build.commit: ",
		"build.date: ",
		"uptime: ",
		"mode: ",
		"timestamp: ",
		"cluster.enabled: ",
		"features.tor: ",
		"features.i2p: ",
		"features.geoip: ",
		"checks.database: ",
		"checks.cache: ",
		"checks.disk: ",
		"checks.scheduler: ",
		"stats.requests_total: ",
		"stats.requests_24h: ",
		"stats.active_connections: ",
	}

	pos := 0
	for _, key := range want {
		idx := strings.Index(body[pos:], key)
		if idx < 0 {
			t.Fatalf("body missing %q or out of order:\n%s", key, body)
		}
		pos += idx + len(key)
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
