package scheduler

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/geoip"
)

type sequenceDBProbe struct {
	results []error
	calls   int
}

func (p *sequenceDBProbe) Probe(context.Context) error {
	if p.calls >= len(p.results) {
		p.calls++
		return nil
	}
	err := p.results[p.calls]
	p.calls++
	return err
}

func TestDatabaseCheckUsesBothHandleProbeAndRecovers(t *testing.T) {
	probe := &sequenceDBProbe{results: []error{errors.New("secret=hidden"), errors.New("failed"), nil}}
	task := databaseCheckTask(Deps{
		DBProbe:       probe,
		DatabaseError: config.DatabaseErrorNotification{Enabled: true, Failures: 2, Repeat: "24h"},
	})
	for i := 0; i < 3; i++ {
		if err := task(context.Background()); err != nil {
			t.Fatalf("databaseCheckTask() call %d error = %v", i+1, err)
		}
	}
	if probe.calls != 3 {
		t.Fatalf("database probes = %d, want 3", probe.calls)
	}
}

func TestDatabaseProbeErrorRedactsCredentials(t *testing.T) {
	credentialedURL := (&url.URL{
		Scheme: "https",
		User:   url.UserPassword("user", "secret123"),
		Host:   "db.example",
		Path:   "/query",
	}).String()
	for _, err := range []error{
		errors.New("connect failed password=secret123"),
		errors.New(credentialedURL + " failed"),
	} {
		got := redactDatabaseProbeError(err)
		if strings.Contains(got, "secret123") || got == "" {
			t.Fatalf("redactDatabaseProbeError(%q) = %q", err, got)
		}
	}
}

func TestBuiltinTasksOmitsModeBoundTasksWhenDisabled(t *testing.T) {
	cfg := config.Default("").Server.Scheduler
	tasks := BuiltinTasks(cfg, Deps{})
	if len(tasks) != 14 {
		t.Fatalf("len(BuiltinTasks()) = %d, want 14 with overlay features disabled", len(tasks))
	}
	want := map[string]bool{
		"ssl_renewal": true, "geoip_update": true, "blocklist_update": true, "keyserver_retry": true,
		"cve_update": true, "update_check": true, "token_cleanup": true,
		"log_rotation": true, "disk_check": true, "backup_daily": true, "backup_hourly": true,
		"healthcheck_self": true, "security_cleanup": true,
		"database_check": true,
	}
	for _, task := range tasks {
		if !want[task.ID] {
			t.Errorf("unexpected task id %q", task.ID)
		}
		delete(want, task.ID)
		if task.Run == nil {
			t.Errorf("task %q has nil Run func", task.ID)
		}
		if task.Schedule == "" {
			t.Errorf("task %q has empty Schedule", task.ID)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing task ids: %v", want)
	}
}

func TestSecurityCleanupTaskDeletesOnlyExpiredClosedReports(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, report := range []db.SecurityReport{
		{TrackingID: "sec_aaaaaaaaaaaaaaaa", ReceivedAt: now.Add(-60 * 24 * time.Hour), ClosedAt: now.Add(-60 * 24 * time.Hour), Severity: "high", Component: "api", Sealed: "expired"},
		{TrackingID: "sec_bbbbbbbbbbbbbbbb", ReceivedAt: now, Severity: "low", Component: "api", Sealed: "open"},
	} {
		if err := db.InsertSecurityReport(ctx, sqlDB, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := securityCleanupTask(Deps{DB: sqlDB, SecurityReportRetentionDays: 30})(ctx); err != nil {
		t.Fatalf("securityCleanupTask() error = %v", err)
	}
	if _, err := db.GetSecurityReport(ctx, sqlDB, "sec_aaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("expired closed report remains after cleanup")
	}
	if _, err := db.GetSecurityReport(ctx, sqlDB, "sec_bbbbbbbbbbbbbbbb"); err != nil {
		t.Fatalf("open report was deleted: %v", err)
	}
}

type enabledOverlay struct{}

func (enabledOverlay) Enabled() bool  { return true }
func (enabledOverlay) Running() bool  { return true }
func (enabledOverlay) Healthy() bool  { return true }
func (enabledOverlay) Restart() error { return nil }

func TestBuiltinTasksRegistersEnabledOverlayTasks(t *testing.T) {
	tasks := BuiltinTasks(config.Default("").Server.Scheduler, Deps{Tor: enabledOverlay{}, I2P: enabledOverlay{}})
	found := map[string]bool{}
	for _, task := range tasks {
		found[task.ID] = true
	}
	if !found["tor_health"] || !found["i2p_health"] {
		t.Fatalf("enabled overlay tasks missing: %#v", found)
	}
}

func TestTokenCleanupTask(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	ownerID, err := db.CreateOwner(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := db.CreateLinkedToken(ctx, sqlDB, ownerID, "expired", &past, 25, make([]byte, 32))
	if err != nil {
		t.Fatalf("CreateLinkedToken() error = %v", err)
	}
	if err := tokenCleanupTask(sqlDB, config.Tokens{})(ctx); err != nil {
		t.Errorf("tokenCleanupTask() error = %v", err)
	}
	listed, err := db.ListOwnerTokens(ctx, sqlDB, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != token.ID || listed[0].Status() != "expired" {
		t.Errorf("expired linked token should remain visible: %#v", listed)
	}
}

func TestHealthcheckSelfTask(t *testing.T) {
	sqlDB := openTestDB(t)
	if err := healthcheckSelfTask(sqlDB)(context.Background()); err != nil {
		t.Errorf("healthcheckSelfTask() error = %v", err)
	}
}

func TestLogRotationTask(t *testing.T) {
	l := testLogger(t)
	if err := logRotationTask([]*applog.Logger{l})(context.Background()); err != nil {
		t.Errorf("logRotationTask() error = %v", err)
	}
	// A nil entry in the slice must be skipped, not panic.
	if err := logRotationTask([]*applog.Logger{nil, l})(context.Background()); err != nil {
		t.Errorf("logRotationTask() with nil entry error = %v", err)
	}
}

func TestRemoveExpiredTempEntries(t *testing.T) {
	dir := t.TempDir()
	oldEntry := filepath.Join(dir, "shortner-expired")
	newEntry := filepath.Join(dir, "shortner-recent")
	otherProject := filepath.Join(dir, "other-project-expired")
	if err := os.WriteFile(oldEntry, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newEntry, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherProject, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	for _, path := range []string{oldEntry, otherProject} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeExpiredTempEntries(dir, "shortner-", now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("removeExpiredTempEntries() error = %v", err)
	}
	if _, err := os.Stat(oldEntry); !os.IsNotExist(err) {
		t.Fatalf("expired entry stat error = %v, want not-exist", err)
	}
	if _, err := os.Stat(newEntry); err != nil {
		t.Fatalf("recent entry was removed: %v", err)
	}
	if _, err := os.Stat(otherProject); err != nil {
		t.Fatalf("another project's temp entry was removed: %v", err)
	}
}

func TestCleanupDiskSpacePrunesOnlyOldRotatedLogs(t *testing.T) {
	logDir := t.TempDir()
	activeLog := filepath.Join(logDir, "scheduler.log")
	logger, err := applog.Open(activeLog, applog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	oldArchive := filepath.Join(logDir, "scheduler.log.2020-01-01")
	newArchive := filepath.Join(logDir, "scheduler.log.2026-11-26")
	for _, path := range []string{oldArchive, newArchive} {
		if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(oldArchive, old, old); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	if err := cleanupDiskSpace(context.Background(), Deps{
		Logs: []*applog.Logger{logger}, MaintenanceCleanup: config.MaintenanceCleanup{LogRetentionDays: 7},
	}, now); err != nil {
		t.Fatalf("cleanupDiskSpace() error = %v", err)
	}
	if _, err := os.Stat(oldArchive); !os.IsNotExist(err) {
		t.Fatalf("old archive stat error = %v, want not-exist", err)
	}
	if _, err := os.Stat(newArchive); err != nil {
		t.Fatalf("recent archive was removed: %v", err)
	}
	if _, err := os.Stat(activeLog); err != nil {
		t.Fatalf("active log was removed: %v", err)
	}
}

func TestSSLRenewalTaskDisabledOrDevTLD(t *testing.T) {
	deps := Deps{TLSEnabled: false}
	if err := sslRenewalTask(deps)(context.Background()); err != nil {
		t.Errorf("sslRenewalTask() with TLS disabled error = %v, want nil", err)
	}
	deps = Deps{TLSEnabled: true, DevTLD: true, FQDN: "myapp.local"}
	if err := sslRenewalTask(deps)(context.Background()); err != nil {
		t.Errorf("sslRenewalTask() with dev TLD error = %v, want nil", err)
	}
}

func TestSSLRenewalTaskNoCertificateOnDisk(t *testing.T) {
	deps := Deps{TLSEnabled: true, FQDN: "example.com", ConfigDir: t.TempDir()}
	if err := sslRenewalTask(deps)(context.Background()); err != nil {
		t.Errorf("sslRenewalTask() with no certificate error = %v, want nil (first run before ACME issuance)", err)
	}
}

func TestGeoipUpdateTaskNilManagerOrDisabled(t *testing.T) {
	// A nil Manager (e.g. GeoIP disabled at startup) must not panic or error.
	if err := geoipUpdateTask(Deps{})(context.Background()); err != nil {
		t.Errorf("geoipUpdateTask() with nil GeoIP error = %v, want nil", err)
	}
	deps := Deps{GeoIP: geoip.Open(t.TempDir(), false, config.GeoIPDatabases{}), GeoIPCfg: config.GeoIP{Enabled: false}}
	if err := geoipUpdateTask(deps)(context.Background()); err != nil {
		t.Errorf("geoipUpdateTask() with disabled GeoIPCfg error = %v, want nil", err)
	}
}

func TestGeoipUpdateTaskDownloadFailure(t *testing.T) {
	// An already-canceled context guarantees the download fails regardless
	// of whether this sandbox has outbound internet access, exercising the
	// error-propagation path deterministically.
	deps := Deps{
		GeoIP:    geoip.Open(t.TempDir(), true, config.GeoIPDatabases{Country: true}),
		GeoIPCfg: config.GeoIP{Enabled: true, Dir: t.TempDir(), Databases: config.GeoIPDatabases{Country: true}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := geoipUpdateTask(deps)(ctx); err == nil {
		t.Error("geoipUpdateTask() error = nil, want an error (context already canceled)")
	}
}
