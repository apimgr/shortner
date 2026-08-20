package metrics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
)

func testConfig() config.Metrics {
	return config.Metrics{
		Enabled:         true,
		IncludeSystem:   true,
		IncludeRuntime:  true,
		DurationBuckets: []float64{0.1, 1},
		SizeBuckets:     []float64{100, 1000},
	}
}

func TestNewAndInitAppInfo(t *testing.T) {
	m := New(testConfig())
	if m == nil || m.Registry == nil {
		t.Fatal("New returned nil metrics or registry")
	}
	m.InitAppInfo("1.0.0", "abc123", "2026-01-01")

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == namePrefix+"app_info" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %sapp_info metric to be registered", namePrefix)
	}
}

func TestNewFallsBackToDefaultBuckets(t *testing.T) {
	cfg := testConfig()
	cfg.DurationBuckets = nil
	cfg.SizeBuckets = nil
	m := New(cfg)
	if m == nil {
		t.Fatal("New returned nil with empty bucket config")
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/api/v1/links":       "/api/v1/links",
		"/api/v1/links/12345": "/api/v1/links/:id",
		"/api/v1/links/550e8400-e29b-41d4-a716-446655440000": "/api/v1/links/:id",
		"/server/healthz": "/server/healthz",
		"":                "",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartUptimeUpdater(t *testing.T) {
	m := New(testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	m.StartUptimeUpdater(ctx)
	time.Sleep(1100 * time.Millisecond)
	cancel()
	if v := testutilGaugeValue(t, m); v <= 0 {
		t.Errorf("expected app_uptime_seconds > 0 after 1s, got %v", v)
	}
}

func testutilGaugeValue(t *testing.T, m *Metrics) float64 {
	t.Helper()
	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == namePrefix+"app_uptime_seconds" {
			ms := f.GetMetric()
			if len(ms) > 0 {
				return ms[0].GetGauge().GetValue()
			}
		}
	}
	return 0
}

func TestParseQuery(t *testing.T) {
	cases := []struct {
		query   string
		wantOp  string
		wantTbl string
	}{
		{"SELECT * FROM links WHERE slug = ?", "select", "links"},
		{"select id from scheduler_tasks", "select", "scheduler_tasks"},
		{"INSERT INTO links (slug) VALUES (?)", "insert", "links"},
		{"UPDATE links SET clicks = clicks + 1", "update", "links"},
		{"DELETE FROM links WHERE id = ?", "delete", "links"},
		{"", "other", "unknown"},
	}
	for _, c := range cases {
		op, tbl := parseQuery(c.query)
		if op != c.wantOp || tbl != c.wantTbl {
			t.Errorf("parseQuery(%q) = (%q, %q), want (%q, %q)", c.query, op, tbl, c.wantOp, c.wantTbl)
		}
	}
}

func TestClassifyError(t *testing.T) {
	cases := map[error]string{
		errors.New("connection refused"):            "connection",
		errors.New("context deadline timeout"):      "timeout",
		errors.New("UNIQUE constraint failed"):      "duplicate",
		errors.New("duplicate key value"):           "duplicate",
		errors.New("FOREIGN KEY constraint failed"): "constraint",
		errors.New("something else"):                "other",
	}
	for err, want := range cases {
		if got := classifyError(err); got != want {
			t.Errorf("classifyError(%q) = %q, want %q", err, got, want)
		}
	}
}

func TestRecordDBQuery(t *testing.T) {
	m := New(testConfig())
	m.recordDBQuery("select", "links", 5*time.Millisecond, nil)
	m.recordDBQuery("select", "links", 5*time.Millisecond, errors.New("connection refused"))

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var gotQueries, gotErrors bool
	for _, f := range families {
		switch f.GetName() {
		case namePrefix + "db_queries_total":
			gotQueries = true
		case namePrefix + "db_errors_total":
			gotErrors = true
		}
	}
	if !gotQueries || !gotErrors {
		t.Errorf("expected db_queries_total and db_errors_total to be recorded, got queries=%v errors=%v", gotQueries, gotErrors)
	}
}

// stubDriver is a minimal driver.Driver used to exercise
// RegisterInstrumentedDriver without a real database.
type stubDriver struct{}

func (stubDriver) Open(name string) (driver.Conn, error) {
	return nil, errors.New("stub: not implemented")
}

func TestRegisterInstrumentedDriverIsIdempotent(t *testing.T) {
	m := New(testConfig())
	name1 := m.RegisterInstrumentedDriver("stubtest", stubDriver{})
	name2 := m.RegisterInstrumentedDriver("stubtest", stubDriver{})
	if name1 != name2 {
		t.Errorf("RegisterInstrumentedDriver not idempotent: %q != %q", name1, name2)
	}
}

func TestUpdateConnectionMetrics(t *testing.T) {
	m := New(testConfig())
	m.UpdateConnectionMetrics(sql.DBStats{OpenConnections: 3, InUse: 1})

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var gotOpen, gotInUse bool
	for _, f := range families {
		switch f.GetName() {
		case namePrefix + "db_connections_open":
			gotOpen = true
		case namePrefix + "db_connections_in_use":
			gotInUse = true
		}
	}
	if !gotOpen || !gotInUse {
		t.Errorf("expected db_connections_open and db_connections_in_use to be recorded, got open=%v in_use=%v", gotOpen, gotInUse)
	}
}

func TestCollectRuntime(t *testing.T) {
	m := New(testConfig())
	numGC, pauseNs := m.collectRuntime(0, 0)
	_ = numGC
	_ = pauseNs

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == namePrefix+"go_goroutines" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %sgo_goroutines to be recorded after collectRuntime", namePrefix)
	}
}

func TestCollectSystem(t *testing.T) {
	m := New(testConfig())
	// dataDir="" falls back to "/", which always exists.
	m.collectSystem("")

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == namePrefix+"system_memory_used_bytes" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %ssystem_memory_used_bytes to be recorded after collectSystem", namePrefix)
	}
}

func TestStartCollectorRunsAndStops(t *testing.T) {
	m := New(testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	m.StartCollector(ctx, "", nil, true, true)
	time.Sleep(50 * time.Millisecond)
	cancel()
	// Give the goroutine a moment to observe ctx.Done() and exit; there is
	// no explicit join, so this just guards against an immediate panic on
	// shutdown rather than asserting the goroutine has fully returned.
	time.Sleep(50 * time.Millisecond)
}
