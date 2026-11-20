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

func TestBuiltinTasksOmitsModeBoundTasksWhenDisabled(t *testing.T) {
	cfg := config.Default("").Server.Scheduler
	tasks := BuiltinTasks(cfg, Deps{})
	if len(tasks) != 11 {
		t.Fatalf("len(BuiltinTasks()) = %d, want 11 with overlay features disabled", len(tasks))
	}
	want := map[string]bool{
		"ssl_renewal": true, "geoip_update": true, "blocklist_update": true,
		"cve_update": true, "update_check": true, "token_cleanup": true,
		"log_rotation": true, "disk_check": true, "backup_daily": true, "backup_hourly": true,
		"healthcheck_self": true,
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

func TestPendingTaskAlwaysSucceeds(t *testing.T) {
	if err := pendingTask("subsystem not built yet")(context.Background()); err != nil {
		t.Errorf("pendingTask() error = %v, want nil (honest skip, not failure)", err)
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
