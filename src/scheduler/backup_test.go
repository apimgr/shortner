package scheduler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/backup"
	"github.com/apimgr/shortner/src/config"

	_ "modernc.org/sqlite"
)

// recorder collects the audit entries a backup cycle emits.
type recorder struct {
	entries []applog.Entry
}

func (r *recorder) Write(e applog.Entry) error {
	r.entries = append(r.entries, e)
	return nil
}

func (r *recorder) events() []string {
	out := make([]string, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.Event)
	}
	return out
}

func (r *recorder) has(event string) bool {
	for _, e := range r.entries {
		if e.Event == event {
			return true
		}
	}
	return false
}

// newBackupDeps builds a throwaway server layout and the BackupDeps that
// back it.
func newBackupDeps(t *testing.T, audit backup.Auditor) BackupDeps {
	t.Helper()
	root := t.TempDir()

	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backups")
	dbPath := filepath.Join(root, "db", "server.db")

	for _, dir := range []string{configDir, dataDir, backupDir, filepath.Dir(dbPath)} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	configFile := filepath.Join(configDir, "server.yml")
	if err := os.WriteFile(configFile, []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	handle, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := handle.Exec("CREATE TABLE links (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	return BackupDeps{
		Dir:        backupDir,
		Prefix:     "shortner",
		ConfigFile: configFile,
		DBPath:     dbPath,
		ConfigDir:  configDir,
		DataDir:    dataDir,
		AppVersion: "1.2.3",
		Cfg:        config.Default("").Server.Backup,
		Audit:      audit,
	}
}

func TestBackupDepsConfigured(t *testing.T) {
	var zero BackupDeps
	if zero.configured() {
		t.Error("a zero BackupDeps must not be considered configured")
	}
	deps := newBackupDeps(t, nil)
	if !deps.configured() {
		t.Error("a fully populated BackupDeps should be configured")
	}
}

func TestBackupDepsEncrypt(t *testing.T) {
	deps := newBackupDeps(t, nil)

	// No password: nothing to derive a key from, so never encrypted.
	deps.Cfg.Encryption.Enabled = true
	if deps.encrypt() {
		t.Error("encrypt() = true with no password")
	}

	deps.Cfg.EncryptionPassword = "hunter2"
	if !deps.encrypt() {
		t.Error("encrypt() = false with the toggle on and a password set")
	}

	// Compliance mode forces encryption on even with the toggle off.
	deps.Cfg.Encryption.Enabled = false
	deps.Compliance = true
	if !deps.encrypt() {
		t.Error("encrypt() = false in compliance mode with a password set")
	}

	deps.Compliance = false
	if deps.encrypt() {
		t.Error("encrypt() = true with the toggle off and compliance off")
	}
}

func TestBackupDepsPolicy(t *testing.T) {
	deps := newBackupDeps(t, nil)
	status := backup.DiskStatus{TotalBytes: 1000}

	// Default max_total_size is "10%" of the backup volume.
	policy := deps.Policy(status)
	if policy.MaxBackups != deps.Cfg.Retention.MaxBackups {
		t.Errorf("MaxBackups = %d, want %d", policy.MaxBackups, deps.Cfg.Retention.MaxBackups)
	}
	if policy.MaxTotalBytes != 100 {
		t.Errorf("MaxTotalBytes = %d, want 100", policy.MaxTotalBytes)
	}

	// A falsey max_total_size disables the cap entirely.
	deps.Cfg.Retention.MaxTotalSize = "false"
	if got := deps.Policy(status).MaxTotalBytes; got != 0 {
		t.Errorf("MaxTotalBytes = %d, want 0 when the cap is disabled", got)
	}
}

func TestBackupDepsOptionsPinsTime(t *testing.T) {
	deps := newBackupDeps(t, nil)
	opts := deps.options(backup.KindFull)
	if opts.Now.IsZero() {
		t.Fatal("options().Now is zero — the failure audit could not name the file")
	}
	if opts.Kind != backup.KindFull {
		t.Errorf("Kind = %q, want %q", opts.Kind, backup.KindFull)
	}
	if opts.CreatedBy != "scheduler" {
		t.Errorf("CreatedBy = %q, want scheduler", opts.CreatedBy)
	}
}

func TestBackupDailyTaskCreatesAndAudits(t *testing.T) {
	rec := &recorder{}
	deps := newBackupDeps(t, rec)

	if err := backupDailyTask(Deps{Backup: deps})(context.Background()); err != nil {
		t.Fatalf("backup_daily: %v", err)
	}

	files, err := backup.List(deps.Dir, deps.Prefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// One dated full plus the always-replaced daily incremental.
	if len(files) != 2 {
		t.Fatalf("backup dir holds %d file(s), want 2", len(files))
	}
	var sawFull, sawDaily bool
	for _, f := range files {
		switch f.Kind {
		case backup.KindFull:
			sawFull = true
		case backup.KindDaily:
			sawDaily = true
		}
	}
	if !sawFull || !sawDaily {
		t.Errorf("kinds written = %+v, want a full and a daily incremental", files)
	}

	if !rec.has(backup.EventCreated) || !rec.has(backup.EventDailyUpdated) {
		t.Errorf("audit events = %v, want backup.created and backup.daily_updated", rec.events())
	}
}

func TestBackupDailyTaskEncrypted(t *testing.T) {
	rec := &recorder{}
	deps := newBackupDeps(t, rec)
	deps.Cfg.EncryptionPassword = "a-scheduled-password"
	deps.Cfg.Encryption.Enabled = true

	if err := backupDailyTask(Deps{Backup: deps})(context.Background()); err != nil {
		t.Fatalf("backup_daily: %v", err)
	}

	files, err := backup.List(deps.Dir, deps.Prefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no backups written")
	}
	for _, f := range files {
		if !backup.IsEncryptedName(f.Name) {
			t.Errorf("%s is not encrypted", f.Name)
		}
		if _, err := backup.LoadManifest(f.Path, "a-scheduled-password"); err != nil {
			t.Errorf("LoadManifest(%s): %v", f.Name, err)
		}
	}
}

func TestBackupDailyTaskAppliesRetention(t *testing.T) {
	rec := &recorder{}
	deps := newBackupDeps(t, rec)

	// A stale full from an earlier day that the default max_backups=1
	// policy must sweep away.
	stale := filepath.Join(deps.Dir, "shortner_backup_2020-01-01.tar.gz")
	if err := os.WriteFile(stale, []byte("stale archive"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	if err := backupDailyTask(Deps{Backup: deps})(context.Background()); err != nil {
		t.Fatalf("backup_daily: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale backup survived retention: %v", err)
	}
	if !rec.has(backup.EventRetentionCleanup) {
		t.Errorf("audit events = %v, want backup.retention_cleanup", rec.events())
	}
}

func TestBackupDailyTaskComplianceWithoutPasswordFails(t *testing.T) {
	rec := &recorder{}
	deps := newBackupDeps(t, rec)
	deps.Compliance = true

	// AI.md PART 21: compliance mode refuses an unencrypted backup, and
	// there is no password to encrypt with.
	if err := backupDailyTask(Deps{Backup: deps})(context.Background()); err == nil {
		t.Fatal("backup_daily succeeded in compliance mode with no password")
	}
	if !rec.has(backup.EventFailed) {
		t.Errorf("audit events = %v, want backup.failed", rec.events())
	}
}

func TestBackupTasksAreInertWhenUnconfigured(t *testing.T) {
	deps := Deps{}
	if err := backupDailyTask(deps)(context.Background()); err != nil {
		t.Errorf("backup_daily on a zero BackupDeps: %v", err)
	}
	if err := backupHourlyTask(deps)(context.Background()); err != nil {
		t.Errorf("backup_hourly on a zero BackupDeps: %v", err)
	}
}

func TestBackupHourlyTaskDiffsAgainstNewestFull(t *testing.T) {
	rec := &recorder{}
	deps := newBackupDeps(t, rec)

	if err := backupDailyTask(Deps{Backup: deps})(context.Background()); err != nil {
		t.Fatalf("backup_daily: %v", err)
	}
	if err := backupHourlyTask(Deps{Backup: deps})(context.Background()); err != nil {
		t.Fatalf("backup_hourly: %v", err)
	}

	hourly := filepath.Join(deps.Dir, backup.FileName(deps.Prefix, backup.KindHourly, deps.options(backup.KindHourly).Now, false))
	manifest, err := backup.LoadManifest(hourly, "")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.Kind != backup.KindHourly {
		t.Errorf("Kind = %q, want %q", manifest.Kind, backup.KindHourly)
	}
	// Nothing changed since the full, so the incremental carries no files
	// but still records what it was diffed against.
	if manifest.BaseChecksum == "" {
		t.Error("BaseChecksum is empty — the hourly was not diffed against the full")
	}
	if len(manifest.Files) != 0 {
		t.Errorf("Files = %v, want an empty incremental", manifest.Files)
	}
}

func TestNewestManifestTolerates(t *testing.T) {
	deps := newBackupDeps(t, nil)

	// No backups at all.
	if m := newestManifest(deps, nil); m != nil {
		t.Errorf("newestManifest with no files = %+v, want nil", m)
	}

	// A file that is not a readable archive must not abort the run.
	junk := filepath.Join(deps.Dir, "shortner_backup_2020-01-01.tar.gz")
	if err := os.WriteFile(junk, []byte("not an archive"), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	files, err := backup.List(deps.Dir, deps.Prefix)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if m := newestManifest(deps, files); m != nil {
		t.Errorf("newestManifest over an unreadable archive = %+v, want nil", m)
	}
}
