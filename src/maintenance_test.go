package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/paths"
)

func TestRunMaintenanceHelp(t *testing.T) {
	for _, cmd := range []string{"", "help", "--help", "-h"} {
		t.Run("cmd="+cmd, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return runMaintenance("shortner", cmd, maintenanceOptions{}) })
			if code != 0 {
				t.Errorf("code = %d, want 0", code)
			}
			if !strings.Contains(out, "Perform server maintenance operations") {
				t.Errorf("output = %q, want maintenance help text", out)
			}
		})
	}
}

func TestRunTokenMaintenanceListsMaskedAndRevokes(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	dbDir := filepath.Join(root, "data", "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "server.db")
	sqlDB, err := db.Open(dbPath, db.DefaultPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := db.CreateOwner(context.Background(), sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	raw, token, err := db.CreatePrimaryToken(context.Background(), sqlDB, ownerID, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	logsDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	opts := maintenanceOptions{paths: paths.Paths{ConfigFile: filepath.Join(configDir, "server.yml"), DB: dbDir, Logs: logsDir}, args: []string{"list"}}
	out, stderr, code := captureOutput(t, func() int { return runMaintenance("shortner", "token", opts) })
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(out, "prefix\tkind\towner\tname\tcreated\tlast_used\tstatus") || !strings.Contains(out, token.TokenPrefix) {
		t.Errorf("list output missing token metadata: %q", out)
	}
	if strings.Contains(out, raw) {
		t.Error("list output exposed the raw token")
	}
	opts.args = []string{"revoke", token.TokenPrefix}
	_, stderr, code = captureOutput(t, func() int { return runMaintenance("shortner", "token", opts) })
	if code != 0 {
		t.Fatalf("revoke code = %d, stderr = %q", code, stderr)
	}
	sqlDB, err = db.Open(dbPath, db.DefaultPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	all, err := db.ListAllTokens(context.Background(), sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Status() != "revoked" {
		t.Fatalf("tokens after revoke = %#v, want one revoked token", all)
	}
}

func TestRunMaintenanceKnownActionsNotYetAvailable(t *testing.T) {
	for cmd, part := range maintenanceReadDeps {
		t.Run(cmd, func(t *testing.T) {
			_, stderr, code := captureOutput(t, func() int { return runMaintenance("shortner", cmd, maintenanceOptions{}) })
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			wantMsg := "--maintenance " + cmd + " is not yet available"
			if !strings.Contains(stderr, wantMsg) {
				t.Errorf("stderr = %q, want to contain %q", stderr, wantMsg)
			}
			if !strings.Contains(stderr, part) {
				t.Errorf("stderr = %q, want to reference AI.md %q", stderr, part)
			}
		})
	}
}

func TestRunMaintenanceUnknownCommand(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runMaintenance("shortner", "bogus", maintenanceOptions{}) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --maintenance command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestMaintenanceModeRejectsUnknownMode(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int {
		return runMaintenance("shortner", "mode", maintenanceOptions{args: []string{"staging"}})
	})
	if code != 2 || !strings.Contains(stderr, "mode must be production, development, or debug") {
		t.Fatalf("mode result = (%d, %q), want usage error", code, stderr)
	}
}

func TestMaintenanceSecretRejectsAutomaticKeys(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int {
		return runMaintenance("shortner", "secret", maintenanceOptions{args: []string{"rotate", "csrf_token_secret"}})
	})
	if code != 2 || !strings.Contains(stderr, "cannot be manually rotated") {
		t.Fatalf("secret result = (%d, %q), want rejected automatic key", code, stderr)
	}
}

func TestRunMaintenanceUpdateIsAnAlias(t *testing.T) {
	// AI.md PART 22: `--maintenance update [cmd]` accepts the same
	// subcommands as `--update [cmd]`, so its arguments must reach runUpdate.
	opts := maintenanceOptions{args: []string{"help"}}
	out, _, code := captureOutput(t, func() int { return runMaintenance("shortner", "update", opts) })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "Update management") {
		t.Errorf("output = %q, want the --update help text", out)
	}
}

func TestBackupDeleteRequiresKnownFileAndConfirmation(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, "backup")
	configDir := filepath.Join(root, "config")
	logsDir := filepath.Join(root, "logs")
	for _, dir := range []string{backupDir, configDir, logsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	name := "shortner_backup_2026-08-21_123456.tar.gz"
	file := filepath.Join(backupDir, name)
	if err := os.WriteFile(file, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{Backup: backupDir}
	listed, _, listCode := captureOutput(t, func() int { return runBackupList("shortner", p) })
	if listCode != 0 || !strings.Contains(listed, name) {
		t.Fatalf("runBackupList() = (%d, %q), want filename", listCode, listed)
	}
	cfg := config.Default(filepath.Join(root, "db", "server.db"))
	cfg.Server.Token = "operator-token"
	configPath := filepath.Join(configDir, "server.yml")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	oldLine, oldPassword := promptLine, promptPassword
	t.Cleanup(func() { promptLine, promptPassword = oldLine, oldPassword })
	promptLine = func(string) (string, error) { return "DELETE " + name, nil }
	promptPassword = func(string) (string, error) { return "operator-token", nil }
	p.Config, p.ConfigFile, p.DB, p.Logs = configDir, configPath, filepath.Join(root, "db"), logsDir
	_, stderr, code := captureOutput(t, func() int { return runBackupDelete("shortner", p, name) })
	if code != 0 {
		t.Fatalf("runBackupDelete() = %d, stderr=%q", code, stderr)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("backup still exists or stat failed: %v", err)
	}
}

func TestArgAt(t *testing.T) {
	args := []string{"branch", "beta"}
	if got := argAt(args, 0); got != "branch" {
		t.Errorf("argAt(0) = %q, want branch", got)
	}
	if got := argAt(args, 1); got != "beta" {
		t.Errorf("argAt(1) = %q, want beta", got)
	}
	if got := argAt(args, 2); got != "" {
		t.Errorf("argAt(2) = %q, want empty", got)
	}
	if got := argAt(nil, 0); got != "" {
		t.Errorf("argAt(nil, 0) = %q, want empty", got)
	}
}

func TestMaintenanceReadDepsCoversEveryHelpCommand(t *testing.T) {
	// Regression guard: every command listed in maintenanceHelp's Commands
	// section must have a corresponding entry in maintenanceReadDeps, or
	// runMaintenance would wrongly report it as "unknown".
	// backup and restore are implemented (AI.md PART 21) and update is an
	// alias for --update (AI.md PART 22), so all three dispatch directly
	// rather than through maintenanceReadDeps.
	implemented := map[string]bool{"backup": true, "restore": true, "update": true, "pgp": true, "token": true, "mode": true, "setup": true, "secret": true}
	for _, cmd := range []string{"backup", "restore", "update", "mode", "setup", "pgp", "secret", "token", "data", "compliance"} {
		if implemented[cmd] {
			continue
		}
		if _, ok := maintenanceReadDeps[cmd]; !ok {
			t.Errorf("maintenanceReadDeps missing entry for documented command %q", cmd)
		}
	}
}
