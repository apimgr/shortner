package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default("/tmp/db")
	if cfg.Server.Listen != "[::]" {
		t.Errorf("Listen = %q, want [::]", cfg.Server.Listen)
	}
	if cfg.Server.Port != "8090" {
		t.Errorf("Port = %q, want 8090", cfg.Server.Port)
	}
	if cfg.Server.BaseURL != "/" {
		t.Errorf("BaseURL = %q, want /", cfg.Server.BaseURL)
	}
	if cfg.Server.Database.Driver != "sqlite" {
		t.Errorf("Driver = %q, want sqlite", cfg.Server.Database.Driver)
	}
	if cfg.Server.Database.URL != "/tmp/db" {
		t.Errorf("Database.URL = %q, want /tmp/db", cfg.Server.Database.URL)
	}
	if cfg.Server.Token != "" {
		t.Errorf("Token = %q, want empty", cfg.Server.Token)
	}
	if !reflect.DeepEqual(cfg.Server.Contact.Antispam, ContactAntispam{
		Honeypot: true, MinFillSeconds: 3, MaxAgeSeconds: 43200, ReviewMinSeconds: 2, ReviewMaxSeconds: 900,
		PerIP: 5, PerEmail: 3, Global: 60, Window: "1h",
		MaxLinks: 10, RejectMarkup: true, Strikes: 3, BanHours: 24, DuplicateWindowHours: 24,
	}) {
		t.Errorf("Contact.Antispam = %+v, want spec defaults", cfg.Server.Contact.Antispam)
	}
}

func TestLogKindInheritsDefaults(t *testing.T) {
	logs := Default("/db").Server.Logs
	access, ok := logs.EffectiveLogKind("access")
	if !ok || access.Format != "apache" || access.Rotate != "monthly" || access.Keep != "none" {
		t.Fatalf("effective access log = %+v, ok=%v", access, ok)
	}
	server, ok := logs.EffectiveLogKind("server")
	if !ok || server.Rotate != "weekly,50MB" || server.Keep != "none" {
		t.Fatalf("effective server log = %+v, ok=%v", server, ok)
	}
	if _, ok := logs.EffectiveLogKind("unknown"); ok {
		t.Fatal("unknown log kind should not resolve")
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist", "server.yml")

	cfg, err := Load(path, "/db/path")
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	want := Default("/db/path")
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want default %+v", cfg, want)
	}
}

func TestLoadParsesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	yamlContent := "server:\n  token: tok_existing\n  address: 127.0.0.1\n  port: \"9999\"\n  baseurl: /short/\n  database:\n    driver: libsql\n    url: mydb\n"
	if err := os.WriteFile(path, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	cfg, err := Load(path, "/unused")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Token != "tok_existing" {
		t.Errorf("Token = %q, want tok_existing", cfg.Server.Token)
	}
	if cfg.Server.Listen != "127.0.0.1" {
		t.Errorf("Listen = %q, want 127.0.0.1", cfg.Server.Listen)
	}
	if cfg.Server.Port != "9999" {
		t.Errorf("Port = %q, want 9999", cfg.Server.Port)
	}
	if cfg.Server.Database.Driver != "libsql" {
		t.Errorf("Driver = %q, want libsql", cfg.Server.Database.Driver)
	}
}

func TestLoadMergesDropInsInFilenameOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("server.yml", "server:\n  address: 127.0.0.1\n  limits:\n    max_body_size: 100\n")
	if err := os.Mkdir(filepath.Join(dir, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	write("yaml.d/10-first.yml", "server:\n  limits:\n    max_body_size: 200\n    read_timeout: 300\n")
	write("yaml.d/20-last.yaml", "server:\n  port: '9000'\n  limits:\n    max_body_size: 400\n")
	write("yaml.d/30-ignore.bak", "not: [valid")
	if err := os.Mkdir(filepath.Join(dir, "yaml.d", "40-directory.yml"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "/db")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1" || cfg.Server.Port != "9000" || cfg.Server.Limits.MaxBodySize != "400" || cfg.Server.Limits.ReadTimeout != "300" {
		t.Fatalf("merged config = address %q port %q limits %+v", cfg.Server.Listen, cfg.Server.Port, cfg.Server.Limits)
	}
}

func TestLoadRejectsDuplicateAndNullDropInValues(t *testing.T) {
	for name, content := range map[string]string{
		"duplicate": "server:\n  port: '1'\n  port: '2'\n",
		"null":      "server:\n  port: null\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "yaml.d"), 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "server.yml")
			if err := os.WriteFile(path, []byte("server:\n  port: '9000'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "yaml.d", "10-invalid.yml"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path, "/db"); err == nil {
				t.Fatal("Load() error = nil, want invalid drop-in error")
			}
		})
	}
}

func TestLoadUsesDropInsWhenServerFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yaml.d", "10-port.yml"), []byte("server:\n  port: '9123'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "/db")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != "9123" {
		t.Fatalf("port = %q, want 9123", cfg.Server.Port)
	}
}

func TestLoadDoesNotMigrateDropInCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  address: 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yaml.d", "10-token.yml"), []byte("server:\n  token: tok_dropin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "/db")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Token != "tok_dropin" {
		t.Fatalf("token = %q, want drop-in value", cfg.Server.Token)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets.yml")); !os.IsNotExist(err) {
		t.Fatalf("secrets.yml stat error = %v, want file not created", err)
	}
}

func TestPeekServerPortReadsOnlyPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	content := "server:\n  address: 127.0.0.1\n  port: \"443,8443\"\n  token: tok_secret\nweb:\n  unknown_setting: ignored\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	address, got, err := PeekServerSettings(path)
	if err != nil || got != "443,8443" || address != "127.0.0.1" {
		t.Fatalf("PeekServerSettings() = %q, %q, %v", address, got, err)
	}
	missingAddress, missingPort, err := PeekServerSettings(filepath.Join(t.TempDir(), "missing.yml"))
	if err != nil || missingAddress != "" || missingPort != "" {
		t.Fatalf("PeekServerSettings(missing) = %q, %q, %v", missingAddress, missingPort, err)
	}
}

func TestLoadSupportsLegacyListenKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  listen: 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "unused")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1" {
		t.Fatalf("legacy server.listen = %q, want 127.0.0.1", cfg.Server.Listen)
	}
}

func TestValidServerAddress(t *testing.T) {
	for _, address := range []string{"0.0.0.0", "127.0.0.1", "[::]", "localhost", "server.example.org"} {
		if !ValidServerAddress(address) {
			t.Errorf("ValidServerAddress(%q) = false", address)
		}
	}
	for _, address := range []string{"", "bad address", "127.0.0.1:8080", "http://example.org", "-invalid.example"} {
		if ValidServerAddress(address) {
			t.Errorf("ValidServerAddress(%q) = true", address)
		}
	}
}

// TestLoadPartialSchedulerOverridePreservesOtherDefaults confirms that an
// operator server.yml overriding only one AI.md PART 18 built-in task keeps
// the other 11 default tasks (schedule/enabled) intact — yaml.v3's map
// decode merges into the pre-populated Default() map key-by-key rather
// than replacing the whole map, so a partial `scheduler.tasks:` block
// never silently drops the rest.
func TestLoadPartialSchedulerOverridePreservesOtherDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	yamlContent := "server:\n  scheduler:\n    tasks:\n      backup_hourly:\n        schedule: \"@hourly\"\n        enabled: true\n"
	if err := os.WriteFile(path, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	cfg, err := Load(path, "/unused")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.Server.Scheduler.Tasks["backup_hourly"]; !got.Enabled {
		t.Errorf("backup_hourly.Enabled = %v, want true (operator override)", got.Enabled)
	}
	want := Default("/unused").Server.Scheduler.Tasks
	for id, def := range want {
		if id == "backup_hourly" {
			continue
		}
		got, ok := cfg.Server.Scheduler.Tasks[id]
		if !ok {
			t.Errorf("task %q missing after partial override — default was silently dropped", id)
			continue
		}
		if got.Schedule != def.Schedule || got.Enabled != def.Enabled {
			t.Errorf("task %q = %+v, want default %+v", id, got, def)
		}
	}
	if len(cfg.Server.Scheduler.Tasks) != len(want) {
		t.Errorf("len(Tasks) = %d, want %d (all 12 built-in tasks present)", len(cfg.Server.Scheduler.Tasks), len(want))
	}
}

func TestLoadInvalidYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server: [this is not valid: yaml"), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	if _, err := Load(path, "/db"); err == nil {
		t.Fatal("Load() error = nil, want parse error")
	}
}

func TestLoadUnreadableFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	// A path whose parent segment is a file (not a directory) always fails
	// os.ReadFile with something other than IsNotExist.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}
	path := filepath.Join(blocker, "server.yml")

	if _, err := Load(path, "/db"); err == nil {
		t.Fatal("Load() error = nil, want read error")
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "server.yml")

	cfg := Default("/db")
	cfg.Server.Token = "tok_abc123"
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file perm = %v, want 0600", info.Mode().Perm())
	}

	reloaded, err := Load(path, "/db")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.Server.Token != "tok_abc123" {
		t.Errorf("Token = %q, want tok_abc123", reloaded.Server.Token)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") || strings.HasSuffix(string(data), "\n\n") || !strings.Contains(string(data), "\n  database:\n    driver:") {
		t.Fatalf("saved YAML must have one trailing newline and two-space nesting; got suffix %q", string(data[len(data)-min(len(data), 100):]))
	}
	if strings.Contains(string(data), "tok_abc123") {
		t.Fatal("server.yml contains the operator token")
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "nested", "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretData), "tok_abc123") {
		t.Fatal("secrets.yml does not contain the operator token")
	}
}

func TestLoadMigratesLegacyCredentialsToSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	legacy := "server:\n  token: tok_legacy\n  security:\n    encryption_key: legacy-key\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "/db")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Token != "tok_legacy" || cfg.Server.Security.EncryptionKey != "legacy-key" {
		t.Fatalf("loaded credentials = token %q, key %q", cfg.Server.Token, cfg.Server.Security.EncryptionKey)
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverData), "tok_legacy") || strings.Contains(string(serverData), "legacy-key") {
		t.Fatalf("legacy credentials remain in server.yml after migration: %s", serverData)
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretData), "tok_legacy") || !strings.Contains(string(secretData), "legacy-key") {
		t.Fatal("migrated credentials are missing from secrets.yml")
	}
}

func TestEnforcePrivatePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(filepath.Join(dir, "html"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "html", "about.html"), []byte("<p>about</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.yml"), []byte("server: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnforcePrivatePaths(dir); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		dir:                                      0o700,
		filepath.Join(dir, "server.yml"):         0o600,
		filepath.Join(dir, "html"):               0o700,
		filepath.Join(dir, "html", "about.html"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode for %s = %o, want %o", path, got, want)
		}
	}
}

func TestSaveInvalidPathReturnsError(t *testing.T) {
	dir := t.TempDir()
	// Make the intended parent directory a file so MkdirAll fails.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}
	path := filepath.Join(blocker, "sub", "server.yml")

	if err := Save(path, Default("/db")); err == nil {
		t.Fatal("Save() error = nil, want mkdir error")
	}
}

func TestEnsureToken(t *testing.T) {
	t.Run("generates when empty", func(t *testing.T) {
		cfg := Default("/db")
		generated, err := EnsureToken(cfg)
		if err != nil {
			t.Fatalf("EnsureToken() error = %v", err)
		}
		if !generated {
			t.Error("generated = false, want true")
		}
		if !strings.HasPrefix(cfg.Server.Token, "tok_") {
			t.Errorf("Token = %q, want tok_ prefix", cfg.Server.Token)
		}
		if len(cfg.Server.Token) != len("tok_")+32 {
			t.Errorf("Token length = %d, want %d", len(cfg.Server.Token), len("tok_")+32)
		}
	})

	t.Run("leaves existing token alone", func(t *testing.T) {
		cfg := Default("/db")
		cfg.Server.Token = "tok_preexisting"
		generated, err := EnsureToken(cfg)
		if err != nil {
			t.Fatalf("EnsureToken() error = %v", err)
		}
		if generated {
			t.Error("generated = true, want false")
		}
		if cfg.Server.Token != "tok_preexisting" {
			t.Errorf("Token = %q, want unchanged", cfg.Server.Token)
		}
	})

	t.Run("tokens are unique across calls", func(t *testing.T) {
		a := Default("/db")
		b := Default("/db")
		if _, err := EnsureToken(a); err != nil {
			t.Fatalf("EnsureToken() error = %v", err)
		}
		if _, err := EnsureToken(b); err != nil {
			t.Fatalf("EnsureToken() error = %v", err)
		}
		if a.Server.Token == b.Server.Token {
			t.Errorf("expected unique tokens, both = %q", a.Server.Token)
		}
	})
}

func TestParseBool(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		defaultVal bool
		want       bool
		wantErr    bool
	}{
		{"1", "1", false, true, false},
		{"true", "true", false, true, false},
		{"t", "t", false, true, false},
		{"yes", "yes", false, true, false},
		{"y", "y", false, true, false},
		{"on", "on", false, true, false},
		{"enable", "enable", false, true, false},
		{"enabled", "enabled", false, true, false},
		{"uppercase TRUE", "TRUE", false, true, false},
		{"whitespace padded", "  yes  ", false, true, false},
		{"0", "0", true, false, false},
		{"false", "false", true, false, false},
		{"f", "f", true, false, false},
		{"no", "no", true, false, false},
		{"n", "n", true, false, false},
		{"off", "off", true, false, false},
		{"disable", "disable", true, false, false},
		{"disabled", "disabled", true, false, false},
		{"empty string uses default true", "", true, true, false},
		{"empty string uses default false", "", false, false, false},
		{"invalid value errors", "maybe", false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBool(tt.in, tt.defaultVal)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseBool(%q, %v) error = %v, wantErr %v", tt.in, tt.defaultVal, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseBool(%q, %v) = %v, want %v", tt.in, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestMustParseBool(t *testing.T) {
	if got := MustParseBool("yes", false); got != true {
		t.Errorf("MustParseBool(%q) = %v, want true", "yes", got)
	}
	if got := MustParseBool("", true); got != true {
		t.Errorf("MustParseBool(empty, true) = %v, want true", got)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("MustParseBool(invalid) did not panic")
		}
	}()
	MustParseBool("maybe", false)
}

func TestIsTruthy(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"yes", true},
		{"1", true},
		{"false", false},
		{"", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := IsTruthy(tt.in); got != tt.want {
				t.Errorf("IsTruthy(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsFalsy(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"false", true},
		{"no", true},
		{"0", true},
		{"true", false},
		{"", false},
		{"garbage", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := IsFalsy(tt.in); got != tt.want {
				t.Errorf("IsFalsy(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
