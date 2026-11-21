package main

import (
	"context"
	"encoding/json"
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

func TestComplianceReportIsOperatorWideAndRejectsOwnerArgument(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int {
		return runMaintenance("shortner", "compliance", maintenanceOptions{args: []string{"report", "--owner", "prefix"}})
	})
	if code != 2 || !strings.Contains(stderr, "usage: --maintenance compliance report") {
		t.Fatalf("compliance usage result = (%d, %q), want operator-wide usage error", code, stderr)
	}
	_, stderr, code = captureOutput(t, func() int {
		return runMaintenance("shortner", "data", maintenanceOptions{args: []string{"export"}})
	})
	if code != 2 || !strings.Contains(stderr, "--owner <prefix>") {
		t.Fatalf("data usage result = (%d, %q), want required owner prefix", code, stderr)
	}
}

func TestDataExportWritesPrivateAPIShapeWithoutTokenSecrets(t *testing.T) {
	root := t.TempDir()
	dbDir, logsDir := filepath.Join(root, "data"), filepath.Join(root, "logs")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	h, err := db.Open(filepath.Join(dbDir, "server.db"), db.DefaultPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := db.CreateOwner(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	raw, tok, err := db.CreatePrimaryToken(context.Background(), h, ownerID, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateLinkAutoCodeOwned(context.Background(), h, ownerID, "https://example.test/target", nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(filepath.Join(dbDir, "server.db"))
	cfg.Server.BaseURL = "/shortner"
	output := filepath.Join(root, "export.json")
	code := runDataExport("shortner", paths.Paths{Logs: logsDir}, cfg, h, ownerID, output)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("runDataExport() = %d", code)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("export mode = %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), raw) || strings.Contains(string(data), tok.TokenHash) {
		t.Fatal("export exposed a token or token hash")
	}
	var parsed dataExport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Format != 1 || parsed.Owner.ID != ownerID || len(parsed.Tokens) != 1 || len(parsed.Resources) != 1 {
		t.Fatalf("export = %#v, want format 1, owner metadata, one token, one resource", parsed)
	}
	if !strings.HasSuffix(parsed.Resources[0].ShortURL, "/shortner/"+parsed.Resources[0].ShortCode) {
		t.Errorf("export short URL %q does not include configured base path", parsed.Resources[0].ShortURL)
	}
}

func TestDataDeleteRemovesOwnerResourcesAndLogsOnlyAnonymizedOwner(t *testing.T) {
	root := t.TempDir()
	dbDir, logsDir := filepath.Join(root, "data"), filepath.Join(root, "logs")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "server.db")
	h, err := db.Open(dbPath, db.DefaultPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := db.CreateOwner(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.CreatePrimaryToken(context.Background(), h, ownerID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateLinkAutoCodeOwned(context.Background(), h, ownerID, "https://example.test/target", nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(dbPath)
	p := paths.Paths{Logs: logsDir}
	_, stderr, code := captureOutput(t, func() int { return runDataDelete("shortner", p, cfg, h, ownerID, true) })
	if code != 0 {
		t.Fatalf("runDataDelete() = %d, stderr = %q", code, stderr)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM owners`,
		`SELECT COUNT(*) FROM links`,
		`SELECT COUNT(*) FROM api_tokens`,
	} {
		var count int
		if err := h.QueryRow(query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s = %d, want 0 after deletion", query, count)
		}
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(logsDir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ownerID) || !strings.Contains(string(data), `"anonymized_count":0`) || !strings.Contains(string(data), `"deleted_count":3`) {
		t.Fatalf("audit log should contain deletion counts and no original owner ID: %s", data)
	}
}

func TestDataDeleteHonorsErasureWhenRetentionStandardsOverlap(t *testing.T) {
	root := t.TempDir()
	dbDir, logsDir := filepath.Join(root, "data"), filepath.Join(root, "logs")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	h, err := db.Open(filepath.Join(dbDir, "server.db"), db.DefaultPool(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := db.CreateOwner(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateLinkAutoCodeOwned(context.Background(), h, ownerID, "https://example.test/target", nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default(filepath.Join(dbDir, "server.db"))
	cfg.Server.Compliance.GDPR = true
	cfg.Server.Compliance.HIPAA = true
	stdout, stderr, code := captureOutput(t, func() int {
		return runDataDelete("shortner", paths.Paths{Logs: logsDir}, cfg, h, ownerID, true)
	})
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Deleted 2 items; anonymized 0 retained items.") {
		t.Fatalf("runDataDelete() = (%d, %q, %q), want deletion despite overlapping standards", code, stdout, stderr)
	}
	var owners, links int
	if err := h.QueryRow(`SELECT COUNT(*) FROM owners`).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(`SELECT COUNT(*) FROM links`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if owners != 0 || links != 0 {
		t.Fatalf("rows after deletion = owners:%d links:%d, want both removed", owners, links)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
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

func TestMaintenanceHelpListsImplementedCommands(t *testing.T) {
	for _, cmd := range []string{"backup", "restore", "update", "mode", "setup", "pgp", "security", "secret", "token", "data", "compliance"} {
		if !strings.Contains(maintenanceHelp, cmd) {
			t.Errorf("maintenance help missing implemented command %q", cmd)
		}
	}
}
