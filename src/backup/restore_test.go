package backup

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthorizeMatrix(t *testing.T) {
	// AI.md PART 21 "Restore Authorization", row for row.
	cases := []struct {
		name string
		env  AuthEnv
		want Authorization
	}{
		{"empty database", AuthEnv{DatabaseEmpty: true}, AuthAllowed},
		{"empty database as root", AuthEnv{DatabaseEmpty: true, IsRoot: true}, AuthAllowed},
		{"root", AuthEnv{IsRoot: true}, AuthConfirm},
		{"root with readable config", AuthEnv{IsRoot: true, ConfigReadable: true}, AuthConfirm},
		{"service user", AuthEnv{ConfigReadable: true}, AuthToken},
		{"random user", AuthEnv{}, AuthDenied},
	}
	for _, c := range cases {
		if got := Authorize(c.env); got != c.want {
			t.Errorf("%s: Authorize = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDatabaseEmpty(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "absent.db")
	if !DatabaseEmpty(missing) {
		t.Error("a missing database should count as empty")
	}

	zero := filepath.Join(dir, "zero.db")
	if err := os.WriteFile(zero, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !DatabaseEmpty(zero) {
		t.Error("a zero-length database should count as empty")
	}

	populated := filepath.Join(dir, "full.db")
	handle, err := sql.Open("sqlite", "file:"+populated)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := handle.Exec("CREATE TABLE links (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	handle.Close()
	if DatabaseEmpty(populated) {
		t.Error("a database with application tables should not count as empty")
	}

	// An unreadable/corrupt database must NOT be treated as disposable.
	corrupt := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("not sqlite at all"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if DatabaseEmpty(corrupt) {
		t.Error("an unreadable database must not be reported as empty")
	}
}

func TestConfigReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !ConfigReadable(path) {
		t.Error("ConfigReadable = false for a readable file")
	}
	if ConfigReadable(filepath.Join(dir, "absent.yml")) {
		t.Error("ConfigReadable = true for a missing file")
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	opts := env.options()
	opts.IncludeSSL = true
	opts.IncludeData = true

	res, err := Create(opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Destroy the live state the backup captured.
	writeFile(t, env.configFile, "server:\n  port: 9999\n")
	if err := os.Remove(filepath.Join(env.configDir, "theme", "dark.css")); err != nil {
		t.Fatalf("remove theme: %v", err)
	}
	if err := os.Remove(filepath.Join(env.dataDir, "notes.txt")); err != nil {
		t.Fatalf("remove data: %v", err)
	}

	restored, err := Restore(RestoreOptions{
		Path:       res.Path,
		ConfigFile: env.configFile,
		DBPath:     env.dbPath,
		ConfigDir:  env.configDir,
		DataDir:    env.dataDir,
		AppVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(restored.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", restored.Warnings)
	}
	if len(restored.Restored) != len(res.Manifest.Files) {
		t.Errorf("restored %d file(s), want %d", len(restored.Restored), len(res.Manifest.Files))
	}

	got, err := os.ReadFile(env.configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != "server:\n  port: 8080\n" {
		t.Errorf("config = %q, want the backed-up contents", got)
	}
	for _, path := range []string{
		filepath.Join(env.configDir, "theme", "dark.css"),
		filepath.Join(env.configDir, "ssl", "cert.pem"),
		filepath.Join(env.dataDir, "notes.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was not restored: %v", path, err)
		}
	}

	// The restored database must still be a working SQLite file.
	handle, err := sql.Open("sqlite", "file:"+env.dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer handle.Close()
	var slug string
	if err := handle.QueryRow("SELECT slug FROM links LIMIT 1").Scan(&slug); err != nil {
		t.Fatalf("query restored db: %v", err)
	}
	if slug != "abc" {
		t.Errorf("slug = %q, want abc", slug)
	}
}

func TestRestoreEncrypted(t *testing.T) {
	env := newTestEnv(t)
	opts := env.options()
	opts.Encrypt = true
	opts.Password = "a-very-good-password"

	res, err := Create(opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeFile(t, env.configFile, "broken\n")

	if _, err := Restore(RestoreOptions{
		Path:       res.Path,
		Password:   "wrong",
		ConfigFile: env.configFile,
		DBPath:     env.dbPath,
		ConfigDir:  env.configDir,
		DataDir:    env.dataDir,
	}); err == nil {
		t.Fatal("Restore succeeded with the wrong password")
	}
	if got, _ := os.ReadFile(env.configFile); string(got) != "broken\n" {
		t.Error("a failed restore modified the live config")
	}

	if _, err := Restore(RestoreOptions{
		Path:       res.Path,
		Password:   "a-very-good-password",
		ConfigFile: env.configFile,
		DBPath:     env.dbPath,
		ConfigDir:  env.configDir,
		DataDir:    env.dataDir,
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got, _ := os.ReadFile(env.configFile); string(got) != "server:\n  port: 8080\n" {
		t.Errorf("config = %q, want the backed-up contents", got)
	}
}

func TestDestinationFor(t *testing.T) {
	opts := RestoreOptions{
		ConfigFile: "/etc/app/server.yml",
		DBPath:     "/var/lib/app/db/server.db",
		ConfigDir:  "/etc/app",
		DataDir:    "/var/lib/app",
	}
	cases := map[string]string{
		"server.yml":            "/etc/app/server.yml",
		"server.db":             "/var/lib/app/db/server.db",
		"template/welcome.tmpl": "/etc/app/template/welcome.tmpl",
		"theme/dark.css":        "/etc/app/theme/dark.css",
		"ssl/cert.pem":          "/etc/app/ssl/cert.pem",
		"data/notes.txt":        "/var/lib/app/notes.txt",
		// Unknown top-level entries are skipped, never guessed at.
		"manifest.json": "",
		"mystery/x":     "",
	}
	for name, want := range cases {
		got, err := opts.destinationFor(name)
		if err != nil {
			t.Errorf("destinationFor(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("destinationFor(%q) = %q, want %q", name, got, want)
		}
	}

	// Path traversal must be rejected outright.
	if _, err := opts.destinationFor("../../etc/passwd"); err == nil {
		t.Error("destinationFor accepted a traversal path")
	}
}

func TestRestoreMissingFile(t *testing.T) {
	env := newTestEnv(t)
	if _, err := Restore(RestoreOptions{
		Path:       filepath.Join(env.backupDir, "absent.tar.gz"),
		ConfigFile: env.configFile,
		DBPath:     env.dbPath,
		ConfigDir:  env.configDir,
		DataDir:    env.dataDir,
	}); err == nil {
		t.Fatal("Restore succeeded for a missing archive")
	}
}
