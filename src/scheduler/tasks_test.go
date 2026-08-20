package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/geoip"
)

func TestBuiltinTasksRegistersAllTwelve(t *testing.T) {
	cfg := config.Default("").Server.Scheduler
	tasks := BuiltinTasks(cfg, Deps{})
	if len(tasks) != 12 {
		t.Fatalf("len(BuiltinTasks()) = %d, want 12 (AI.md PART 18 'Built-in Tasks (Required)')", len(tasks))
	}
	want := map[string]bool{
		"ssl_renewal": true, "geoip_update": true, "blocklist_update": true,
		"cve_update": true, "update_check": true, "token_cleanup": true,
		"log_rotation": true, "backup_daily": true, "backup_hourly": true,
		"healthcheck_self": true, "tor_health": true, "i2p_health": true,
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

func TestPendingTaskAlwaysSucceeds(t *testing.T) {
	if err := pendingTask("subsystem not built yet")(context.Background()); err != nil {
		t.Errorf("pendingTask() error = %v, want nil (honest skip, not failure)", err)
	}
}

func TestTokenCleanupTask(t *testing.T) {
	sqlDB := openTestDB(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour)
	if _, _, err := db.CreateResourceToken(ctx, sqlDB, "link", "expired", &past); err != nil {
		t.Fatalf("CreateResourceToken() error = %v", err)
	}
	if err := tokenCleanupTask(sqlDB)(ctx); err != nil {
		t.Errorf("tokenCleanupTask() error = %v", err)
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
