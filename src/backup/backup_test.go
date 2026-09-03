package backup

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// testEnv is a throwaway server layout: a config file, a real SQLite
// database, a custom template, a theme, an SSL cert, and a data file.
type testEnv struct {
	root       string
	configDir  string
	dataDir    string
	backupDir  string
	configFile string
	dbPath     string
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	root := t.TempDir()

	env := testEnv{
		root:      root,
		configDir: filepath.Join(root, "config"),
		dataDir:   filepath.Join(root, "data"),
		backupDir: filepath.Join(root, "backups"),
	}
	env.configFile = filepath.Join(env.configDir, "server.yml")
	env.dbPath = filepath.Join(root, "db", "server.db")

	for _, dir := range []string{
		env.configDir,
		env.dataDir,
		env.backupDir,
		filepath.Dir(env.dbPath),
		filepath.Join(env.configDir, "template"),
		filepath.Join(env.configDir, "theme"),
		filepath.Join(env.configDir, "ssl"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	writeFile(t, env.configFile, "server:\n  port: 8080\n")
	writeFile(t, filepath.Join(env.configDir, "template", "welcome.tmpl"), "hello\n")
	writeFile(t, filepath.Join(env.configDir, "theme", "dark.css"), ":root{}\n")
	writeFile(t, filepath.Join(env.configDir, "ssl", "cert.pem"), "-----BEGIN CERTIFICATE-----\n")
	writeFile(t, filepath.Join(env.dataDir, "notes.txt"), "some data\n")

	handle, err := sql.Open("sqlite", "file:"+env.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := handle.Exec("CREATE TABLE links (id INTEGER PRIMARY KEY, slug TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := handle.Exec("INSERT INTO links (slug) VALUES ('abc')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	return env
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (e testEnv) options() Options {
	return Options{
		Dir:        e.backupDir,
		Prefix:     "shortner",
		Kind:       KindFull,
		ConfigFile: e.configFile,
		DBPath:     e.dbPath,
		ConfigDir:  e.configDir,
		DataDir:    e.dataDir,
		AppVersion: "1.2.3",
		Now:        time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
	}
}

func TestCreateAndVerifyRoundTrip(t *testing.T) {
	env := newTestEnv(t)

	res, err := Create(env.options())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Name != "shortner_backup_2025-01-15.tar.gz" {
		t.Errorf("Name = %q, want the dated full-backup name", res.Name)
	}
	if res.Size == 0 {
		t.Error("Size = 0, want a non-empty archive")
	}
	if res.Encrypted {
		t.Error("Encrypted = true, want false for an unencrypted backup")
	}
	if res.Manifest.Version != ManifestVersion {
		t.Errorf("manifest version = %q, want %q", res.Manifest.Version, ManifestVersion)
	}
	if res.Manifest.AppVersion != "1.2.3" {
		t.Errorf("manifest app_version = %q, want 1.2.3", res.Manifest.AppVersion)
	}

	// PART 21 "Backup Contents": server.yml, server.db, template/ and
	// theme/ are always included; ssl/ and data/ only with their flags.
	for _, want := range []string{"server.yml", "server.db", "template/welcome.tmpl", "theme/dark.css"} {
		if _, ok := res.Manifest.Files[want]; !ok {
			t.Errorf("manifest is missing %q", want)
		}
	}
	for _, unwanted := range []string{"ssl/cert.pem", "data/notes.txt"} {
		if _, ok := res.Manifest.Files[unwanted]; ok {
			t.Errorf("manifest contains %q without its opt-in flag", unwanted)
		}
	}

	// Create already verifies; verifying again must still pass.
	if err := Verify(res.Path, ""); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestCreateWithOptionalContents(t *testing.T) {
	env := newTestEnv(t)
	opts := env.options()
	opts.IncludeSSL = true
	opts.IncludeData = true

	res, err := Create(opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, want := range []string{"ssl/cert.pem", "data/notes.txt"} {
		if _, ok := res.Manifest.Files[want]; !ok {
			t.Errorf("manifest is missing opt-in content %q", want)
		}
	}
}

func TestCreateEncryptedRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	opts := env.options()
	opts.Encrypt = true
	opts.Password = "hunter2hunter2"

	res, err := Create(opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasSuffix(res.Name, ".tar.gz.enc") {
		t.Errorf("Name = %q, want a .tar.gz.enc suffix", res.Name)
	}
	if res.Manifest.EncryptionMethod != EncryptionMethod {
		t.Errorf("encryption_method = %q, want %q", res.Manifest.EncryptionMethod, EncryptionMethod)
	}
	if err := Verify(res.Path, "hunter2hunter2"); err != nil {
		t.Fatalf("Verify with the right password: %v", err)
	}

	var verr *VerificationError
	err = Verify(res.Path, "wrong-password")
	if !errors.As(err, &verr) || verr.Check != CheckDecrypt {
		t.Fatalf("Verify with the wrong password = %v, want a decrypt-test failure", err)
	}
}

func TestCreateComplianceRequiresEncryption(t *testing.T) {
	env := newTestEnv(t)
	opts := env.options()
	opts.Compliance = true

	if _, err := Create(opts); !errors.Is(err, ErrComplianceEncryptionRequired) {
		t.Fatalf("Create in compliance mode without encryption = %v, want ErrComplianceEncryptionRequired", err)
	}
}

func TestCreateEncryptRequiresPassword(t *testing.T) {
	env := newTestEnv(t)
	opts := env.options()
	opts.Encrypt = true

	if _, err := Create(opts); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("Create with encryption and no password = %v, want ErrPasswordRequired", err)
	}
}

func TestCreateDeletesArchiveWhenVerificationFails(t *testing.T) {
	env := newTestEnv(t)
	// A truncated database file is structurally invalid, so the mandatory
	// database-integrity check must fail and the archive must be deleted.
	writeFile(t, env.dbPath, "this is not a sqlite database")

	opts := env.options()
	_, err := Create(opts)
	if err == nil {
		t.Fatal("Create succeeded with a corrupt database")
	}
	var verr *VerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("Create error = %v, want a VerificationError", err)
	}

	entries, readErr := os.ReadDir(env.backupDir)
	if readErr != nil {
		t.Fatalf("read backup dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("backup dir holds %d file(s), want the failed backup deleted", len(entries))
	}
}

func TestCreateIncrementalCarriesOnlyChanges(t *testing.T) {
	env := newTestEnv(t)

	full, err := Create(env.options())
	if err != nil {
		t.Fatalf("Create full: %v", err)
	}

	writeFile(t, filepath.Join(env.configDir, "theme", "dark.css"), ":root{--fg:#fff}\n")

	incOpts := env.options()
	incOpts.Kind = KindDaily
	incOpts.Base = &full.Manifest
	inc, err := Create(incOpts)
	if err != nil {
		t.Fatalf("Create incremental: %v", err)
	}
	if inc.Name != "shortner-daily.tar.gz" {
		t.Errorf("incremental name = %q, want shortner-daily.tar.gz", inc.Name)
	}
	if _, ok := inc.Manifest.Files["theme/dark.css"]; !ok {
		t.Error("incremental is missing the changed theme file")
	}
	if _, ok := inc.Manifest.Files["template/welcome.tmpl"]; ok {
		t.Error("incremental contains an unchanged file")
	}
	if inc.Manifest.BaseChecksum != full.Manifest.Checksum {
		t.Errorf("base_checksum = %q, want the full backup's checksum", inc.Manifest.BaseChecksum)
	}
}

func TestCreateNamedBackup(t *testing.T) {
	env := newTestEnv(t)
	opts := env.options()
	opts.Kind = KindManual
	opts.Name = "nightly"

	res, err := Create(opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Name != "nightly.tar.gz" {
		t.Errorf("Name = %q, want nightly.tar.gz", res.Name)
	}
	if filepath.Dir(res.Path) != env.backupDir {
		t.Errorf("Path = %q, want it inside the backup dir", res.Path)
	}
}

func TestFileName(t *testing.T) {
	when := time.Date(2025, 1, 15, 10, 30, 45, 0, time.UTC)
	cases := []struct {
		kind      Kind
		encrypted bool
		want      string
	}{
		{KindFull, false, "shortner_backup_2025-01-15.tar.gz"},
		{KindFull, true, "shortner_backup_2025-01-15.tar.gz.enc"},
		{KindManual, false, "shortner_backup_2025-01-15_103045.tar.gz"},
		{KindDaily, false, "shortner-daily.tar.gz"},
		{KindHourly, true, "shortner-hourly.tar.gz.enc"},
	}
	for _, c := range cases {
		if got := FileName("shortner", c.kind, when, c.encrypted); got != c.want {
			t.Errorf("FileName(%s, enc=%t) = %q, want %q", c.kind, c.encrypted, got, c.want)
		}
	}
}

func TestNormalizeName(t *testing.T) {
	cases := []struct {
		in        string
		encrypted bool
		want      string
	}{
		{"nightly", false, "nightly.tar.gz"},
		{"nightly", true, "nightly.tar.gz.enc"},
		{"nightly.tar.gz", true, "nightly.tar.gz.enc"},
		{"nightly.tar.gz.enc", false, "nightly.tar.gz"},
		{"nightly.tgz", false, "nightly.tar.gz"},
	}
	for _, c := range cases {
		if got := NormalizeName(c.in, c.encrypted); got != c.want {
			t.Errorf("NormalizeName(%q, %t) = %q, want %q", c.in, c.encrypted, got, c.want)
		}
	}
}

func TestVerifyMissingFile(t *testing.T) {
	var verr *VerificationError
	err := Verify(filepath.Join(t.TempDir(), "nope.tar.gz"), "")
	if !errors.As(err, &verr) || verr.Check != CheckFileExists {
		t.Fatalf("Verify of a missing file = %v, want a file-exists failure", err)
	}
}

func TestVerifyEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.tar.gz")
	writeFile(t, path, "")

	var verr *VerificationError
	err := Verify(path, "")
	if !errors.As(err, &verr) || verr.Check != CheckSizeNonZero {
		t.Fatalf("Verify of an empty file = %v, want a size failure", err)
	}
}

func TestVerifyGarbageIsAFormatFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.tar.gz")
	writeFile(t, path, "definitely not gzip")

	var verr *VerificationError
	err := Verify(path, "")
	if !errors.As(err, &verr) || verr.Check != CheckFormat {
		t.Fatalf("Verify of garbage = %v, want a format failure", err)
	}
}

func TestLoadManifest(t *testing.T) {
	env := newTestEnv(t)
	res, err := Create(env.options())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	m, err := LoadManifest(res.Path, "")
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Checksum != res.Manifest.Checksum {
		t.Errorf("checksum = %q, want %q", m.Checksum, res.Manifest.Checksum)
	}
	if m.CreatedBy != "operator" {
		t.Errorf("created_by = %q, want operator", m.CreatedBy)
	}
}

func TestVerifyForRestoreWarnsOnVersionMismatch(t *testing.T) {
	env := newTestEnv(t)
	res, err := Create(env.options())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, warnings, err := VerifyForRestore(res.Path, "", "9.9.9")
	if err != nil {
		t.Fatalf("VerifyForRestore: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "1.2.3") {
		t.Fatalf("warnings = %v, want one version-mismatch warning", warnings)
	}

	_, warnings, err = VerifyForRestore(res.Path, "", "1.2.3")
	if err != nil {
		t.Fatalf("VerifyForRestore (matching version): %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none for a matching version", warnings)
	}
}
