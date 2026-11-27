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
	if cfg.Server.TLS.MinVersion != "TLS1.2" {
		t.Errorf("TLS.MinVersion = %q, want TLS1.2", cfg.Server.TLS.MinVersion)
	}
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
	if cfg.Server.Maintenance.SelfHealing.RetryInterval != "30s" || cfg.Server.Maintenance.SelfHealing.MaxAttempts != 0 || cfg.Server.Maintenance.Cleanup.DiskThreshold != 90 || cfg.Server.Maintenance.Cleanup.LogRetentionDays != 7 || !cfg.Server.Maintenance.Notify.OnEnter || !cfg.Server.Maintenance.Notify.OnExit {
		t.Errorf("Maintenance = %+v, want AI.md maintenance defaults", cfg.Server.Maintenance)
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

func TestValidateTLSSettings(t *testing.T) {
	cfg := Default("")
	cfg.Server.TLS.MinVersion = "TLS1.0"
	cfg.Server.TLS.Cert = "cert.pem"
	warnings := Validate(cfg)
	if len(warnings) == 0 || cfg.Server.TLS.MinVersion != "TLS1.2" || cfg.Server.TLS.Cert != "" || cfg.Server.TLS.Key != "" {
		t.Fatalf("Validate TLS = warnings %v, TLS %+v", warnings, cfg.Server.TLS)
	}
}

func TestDatabaseCacheDefaultsDisabled(t *testing.T) {
	cfg := Default("server.db")
	if cfg.Server.Database.Cache.Enabled {
		t.Fatal("database cache must be disabled by default")
	}
	if cfg.Server.Database.Cache.SyncInterval != "30s" || !cfg.Server.Database.Cache.OfflineMode || cfg.Server.Database.Cache.MaxAge != "1h" {
		t.Fatalf("cache defaults = %#v", cfg.Server.Database.Cache)
	}
}

func TestLoadMigratesLegacyServerTLSBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	legacy := "server:\n  tls:\n    enabled: true\n    cert: cert.pem\n    key: private.pem\n    min_version: TLS1.3\n    dns_provider: cloudflare\n    dns_credentials:\n      CF_DNS_API_TOKEN: old-value\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAreaFiles(path); err != nil {
		t.Fatal(err)
	}
	if err := MigrateSecretsFiles(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "old-value") || !strings.Contains(string(secretData), "old-value") {
		t.Fatalf("legacy DNS credential not migrated to secrets.yml; server=%s secrets=%s", data, secretData)
	}
	if !cfg.Server.TLS.Enabled || cfg.Server.TLS.Cert != "cert.pem" || cfg.Server.TLS.Key != "private.pem" || cfg.Server.TLS.MinVersion != "TLS1.3" || cfg.Server.TLS.LetsEncrypt.DNSProvider != "cloudflare" || cfg.Server.TLS.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"] != "old-value" {
		t.Fatalf("legacy TLS settings were not preserved: %+v; migrated file %s", cfg.Server.TLS, data)
	}
}

func TestLocalizedPageBodyResolveFallback(t *testing.T) {
	body := LocalizedPageBody{
		Values: map[string]string{"fr": "premier", "default": "fallback", "es": "hola"},
		Order:  []string{"fr", "default", "es"},
		Has:    true,
	}
	for _, test := range []struct{ language, want string }{
		{"es", "hola"}, {"de", "fallback"},
	} {
		got, ok := body.Resolve(test.language)
		if !ok || got != test.want {
			t.Errorf("Resolve(%q) = (%q,%t), want (%q,true)", test.language, got, ok, test.want)
		}
	}
	delete(body.Values, "default")
	got, ok := body.Resolve("de")
	if !ok || got != "premier" {
		t.Errorf("Resolve() without default = (%q,%t), want (premier,true)", got, ok)
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
	yamlContent := "server:\n  token: existing-token\n  address: 127.0.0.1\n  port: \"9999\"\n  baseurl: /short/\n  database:\n    driver: libsql\n    url: mydb\n"
	if err := os.WriteFile(path, []byte(yamlContent), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	cfg, err := Load(path, "/unused")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Token != "existing-token" {
		t.Errorf("Token = %q, want existing-token", cfg.Server.Token)
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

func TestLoadAllowsDisablingDatabaseCacheOfflineMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	content := "server:\n  database:\n    cache:\n      offline_mode: false\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, "/unused")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Database.Cache.OfflineMode {
		t.Fatal("explicit offline_mode: false was replaced by the default")
	}
}

func TestLoadParsesNumericServerPortAsString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  port: 8090\n"), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	cfg, err := Load(path, "/unused")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != "8090" {
		t.Errorf("Port = %q, want decimal string 8090", cfg.Server.Port)
	}
}

func TestMigrateAreaFilesPreservesValuesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	legacy := "server:\n  address: 127.0.0.1\n  port: '9000'\n  branding:\n    title: Example\n  logs:\n    level: debug\ndatabase:\n  type: libsql\n  primary:\n    url: https://db.example\nweb:\n  ui:\n    theme: light\n  robots:\n    enabled: true\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAreaFiles(path); err != nil {
		t.Fatal(err)
	}
	for file, contains := range map[string]string{
		"server.yml":          "address: 127.0.0.1",
		"yaml.d/branding.yml": "title: Example",
		"yaml.d/web.yml":      "robots:",
	} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || !strings.Contains(string(data), contains) {
			t.Fatalf("%s = %q, err %v; want %q", file, data, err, contains)
		}
	}
	serverData, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(serverData), "driver: libsql") || !strings.Contains(string(serverData), "url: https://db.example") {
		t.Fatalf("server.yml database migration = %q, err %v", serverData, err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || string(backup) != legacy {
		t.Fatalf("backup = %q, err %v; want original", backup, err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateAreaFiles(path); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil || string(first) != string(second) {
		t.Fatalf("second migration changed server.yml: err %v", err)
	}
}

func TestMigrateTopLevelCLISettingsToMaintenanceArea(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("cli:\n  binary_download:\n    dir: /srv/shortner/cli\n    require_auth: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAreaFiles(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "yaml.d", "maintenance.yml"))
	if err != nil || !strings.Contains(string(data), "cli:") || !strings.Contains(string(data), "require_auth: true") {
		t.Fatalf("maintenance.yml = %q, err %v", data, err)
	}
	cfg, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CLI.BinaryDownload.Dir != "/srv/shortner/cli" || !cfg.CLI.BinaryDownload.RequireAuth {
		t.Fatalf("CLI config = %+v", cfg.CLI.BinaryDownload)
	}
}

func TestMigrateAreaFilesRenamesLegacyKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	legacy := "server:\n  title: Legacy title\n  pages:\n    about:\n      content: Legacy about\n  logging:\n    level: debug\n  schedule:\n    tasks:\n      cleanup:\n        enabled: true\n        schedule: '@daily'\n        old_key: remove\nweb:\n  ui:\n    favicon: old.ico\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAreaFiles(path); err != nil {
		t.Fatal(err)
	}
	branding, err := os.ReadFile(filepath.Join(dir, "yaml.d", "branding.yml"))
	if err != nil || !strings.Contains(string(branding), "Legacy title") || !strings.Contains(string(branding), "Legacy about") || !strings.Contains(string(branding), "old.ico") {
		t.Fatalf("branding migration = %q, err %v", branding, err)
	}
	logs, err := os.ReadFile(filepath.Join(dir, "yaml.d", "logs.yml"))
	if err != nil || !strings.Contains(string(logs), "level: debug") {
		t.Fatalf("logs migration = %q, err %v", logs, err)
	}
	maintenance, err := os.ReadFile(filepath.Join(dir, "yaml.d", "maintenance.yml"))
	if err != nil || strings.Contains(string(maintenance), "old_key") || !strings.Contains(string(maintenance), "@daily") {
		t.Fatalf("maintenance migration = %q, err %v", maintenance, err)
	}
}

func TestLegacyAppSecretMigrationIsLossless(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	legacy := "server:\n  security:\n    installation_secret: old-installation-secret\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateAreaFiles(path); err != nil {
		t.Fatal(err)
	}
	secrets, err := ReadLegacyAppSecrets(path)
	if err != nil || secrets["installation_secret"] != "old-installation-secret" {
		t.Fatalf("ReadLegacyAppSecrets() = %v, err %v", secrets, err)
	}
	if err := RemoveLegacyAppSecrets(path); err != nil {
		t.Fatal(err)
	}
	remaining, err := ReadLegacyAppSecrets(path)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("legacy secrets remain: %v, err %v", remaining, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "yaml.d", "security.yml.bak")); err != nil {
		t.Fatalf("migration backup missing: %v", err)
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

func TestLoadIgnoresDuplicateAndNullDropInValues(t *testing.T) {
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
			cfg, err := Load(path, "/db")
			if err != nil {
				t.Fatalf("Load() error = %v, want invalid drop-in ignored", err)
			}
			if cfg.Server.Port != "9000" {
				t.Fatalf("port = %q, want server.yml value after invalid drop-in was ignored", cfg.Server.Port)
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

func TestLoadMigratesDropInCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  address: 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "yaml.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yaml.d", "10-token.yml"), []byte("server:\n  token: dropin-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateSecretsFiles(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "/db")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Token != "dropin-token" {
		t.Fatalf("token = %q, want drop-in value", cfg.Server.Token)
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil || !strings.Contains(string(secretData), "dropin-token") {
		t.Fatalf("secrets.yml = %q, err %v; want migrated token", secretData, err)
	}
	dropInData, err := os.ReadFile(filepath.Join(dir, "yaml.d", "10-token.yml"))
	if err != nil || strings.Contains(string(dropInData), "dropin-token") {
		t.Fatalf("drop-in still contains secret: %q, err %v", dropInData, err)
	}
}

func TestMigrateConnectionStringCredentialsIntoSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	urlValue := "libsql://shortner.example.test?authToken=very-secret-token"
	if err := os.WriteFile(path, []byte("server:\n  database:\n    driver: libsql\n    url: "+urlValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateSecretsFiles(path); err != nil {
		t.Fatal(err)
	}
	serverData, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(serverData), "very-secret-token") {
		t.Fatalf("server.yml contains DB credential: %q, err %v", serverData, err)
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil || !strings.Contains(string(secretData), "very-secret-token") {
		t.Fatalf("secrets.yml missing DB credential: %q, err %v", secretData, err)
	}
	cfg, err := Load(path, "/tmp/server.db")
	if err != nil || cfg.Server.Database.URL != urlValue || cfg.Server.Database.Driver != "libsql" {
		t.Fatalf("Load() database = %#v, err %v", cfg.Server.Database, err)
	}
}

func TestPeekServerPortReadsOnlyPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	content := "server:\n  address: 127.0.0.1\n  port: \"443,8443\"\n  token: test-token\nweb:\n  unknown_setting: ignored\n"
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

func TestPeekServerStartupSettingsMergesDaemonizeDropIns(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  port: \"8090\"\n  daemonize: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropDir := filepath.Join(root, "yaml.d")
	if err := os.Mkdir(dropDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "10-runtime.yml"), []byte("server:\n  daemonize: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	address, port, daemonize, err := PeekServerStartupSettings(path)
	if err != nil || address != "" || port != "8090" || !daemonize {
		t.Fatalf("PeekServerStartupSettings() = (%q, %q, %t, %v)", address, port, daemonize, err)
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

func TestLoadInvalidYAMLFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server: [this is not valid: yaml"), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	cfg, err := Load(path, "/db")
	if err != nil || cfg.Server.Port != Default("/db").Server.Port {
		t.Fatalf("Load() = %+v, %v; want default configuration", cfg, err)
	}
}

func TestApplyConnectionEnvironmentPrecedenceAndSchemes(t *testing.T) {
	t.Setenv("DATABASE_URL", "libsql://user:secret@db.example.test?authToken=token")
	t.Setenv("DATABASE_DRIVER", "sqlite")
	t.Setenv("CACHE_URL", "rediss://:secret@cache.example.test:6379/0")
	cfg := Default("/tmp/server.db")
	warnings := ApplyConnectionEnvironment(cfg)
	if cfg.Server.Database.URL != "libsql://user:secret@db.example.test?authToken=token" || cfg.Server.Database.Driver != "libsql" {
		t.Fatalf("database env override = %#v", cfg.Server.Database)
	}
	if cfg.Server.Cache.Type != "redis" || !cfg.Server.Cache.TLS || !cfg.Server.Cache.TLSSkipVerify {
		t.Fatalf("cache URL mapping = %#v", cfg.Server.Cache)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "DATABASE_DRIVER") || strings.Contains(warnings[0], "secret") {
		t.Fatalf("connection warnings = %q", warnings)
	}
}

func TestConnectionEnvironmentSeedsOnlyUnsetConnections(t *testing.T) {
	t.Setenv("DATABASE_URL", "libsql://first.example.test?authToken=secret")
	t.Setenv("CACHE_URL", "valkey://cache.example.test:6379/0")
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  port: '8091'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	warnings := ApplyConnectionEnvironment(cfg)
	if cfg.Server.Database.URL != "libsql://first.example.test?authToken=secret" {
		t.Fatalf("first-run database URL was not seeded: %q", cfg.Server.Database.URL)
	}
	if cfg.Server.Cache.URL != "valkey://cache.example.test:6379/0" || !HasSeededConnection(cfg) {
		t.Fatalf("first-run cache URL was not seeded: %#v", cfg.Server.Cache)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %q", warnings)
	}

	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "first.example.test") || !strings.Contains(string(saved), "cache.example.test") {
		t.Fatalf("saved config contains wrong seeded URL state: %s", saved)
	}
	secrets, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secrets), "first.example.test") || !strings.Contains(string(secrets), "authToken=secret") {
		t.Fatalf("database URL secret was not migrated safely: %s", secrets)
	}

	if err := os.Remove(filepath.Join(dir, "secrets.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("server:\n  database:\n    url: libsql://saved.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	warnings = ApplyConnectionEnvironment(cfg)
	if cfg.Server.Database.URL != "libsql://saved.example.test" || len(warnings) != 1 || warnings[0] != "DATABASE_URL differs from the saved database connection and was ignored" {
		t.Fatalf("saved database URL precedence = %q, warnings %q", cfg.Server.Database.URL, warnings)
	}
}

func TestConnectionEnvironmentExpansionAndValidation(t *testing.T) {
	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_PASSWORD", "p@ss")
	cfg := Default("/tmp/server.db")
	cfg.Server.Database.URL = "libsql://${DB_HOST}?authToken=${DB_PASSWORD}"
	ApplyConnectionEnvironment(cfg)
	if cfg.Server.Database.URL != "libsql://db.internal?authToken=p@ss" {
		t.Fatalf("expanded database URL = %q", cfg.Server.Database.URL)
	}
	if !connectionHasCredentials(cfg.Server.Database.URL) {
		t.Fatal("database credential URL was not recognized as secret")
	}
	if connectionScheme(`C:\\data\\server.db`) != "" {
		t.Fatalf("Windows path parsed as a URL scheme: %q", connectionScheme(`C:\\data\\server.db`))
	}
	cfg.Server.Database.URL = "jdbc:postgresql://db.example.test/app"
	warnings := ValidateConnectionSettings(cfg)
	if len(warnings) == 0 || !strings.Contains(warnings[0], "server.database.url") || strings.Contains(warnings[0], "postgresql://") {
		t.Fatalf("invalid URL warning = %q", warnings)
	}
}

func TestLoadUnreadableFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	// A path whose parent segment is a file (not a directory) always fails
	// os.ReadFile with something other than IsNotExist.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}
	path := filepath.Join(blocker, "server.yml")

	cfg, err := Load(path, "/db")
	if err != nil || cfg.Server.Port != Default("/db").Server.Port {
		t.Fatalf("Load() = %+v, %v; want default configuration", cfg, err)
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "server.yml")

	cfg := Default("/db")
	cfg.Server.Token = "config-token"
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
	if reloaded.Server.Token != "config-token" {
		t.Errorf("Token = %q, want config-token", reloaded.Server.Token)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") || strings.HasSuffix(string(data), "\n\n") || !strings.Contains(string(data), "\n  database:\n    driver:") {
		t.Fatalf("saved YAML must have one trailing newline and two-space nesting; got suffix %q", string(data[len(data)-min(len(data), 100):]))
	}
	if strings.Contains(string(data), "config-token") {
		t.Fatal("server.yml contains the operator token")
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "nested", "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretData), "config-token") {
		t.Fatal("secrets.yml does not contain the operator token")
	}
}

func TestSaveI2PEnabledPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		enabled      bool
		explicit     bool
		wantReloaded bool
		wantExplicit bool
	}{
		{name: "computed container default is not persisted", enabled: true, wantReloaded: false},
		{name: "explicitly disabled remains disabled", explicit: true, wantExplicit: true},
		{name: "explicitly enabled remains enabled", enabled: true, explicit: true, wantReloaded: true, wantExplicit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "server.yml")
			cfg := Default("/db")
			cfg.Server.I2P.Enabled = tt.enabled
			cfg.Server.I2P.EnabledExplicit = tt.explicit
			if err := Save(path, cfg); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			reloaded, err := Load(path, "/db")
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if reloaded.Server.I2P.Enabled != tt.wantReloaded {
				t.Errorf("reloaded enabled = %v, want %v", reloaded.Server.I2P.Enabled, tt.wantReloaded)
			}
			if reloaded.Server.I2P.EnabledExplicit != tt.wantExplicit {
				t.Errorf("reloaded explicit = %v, want %v", reloaded.Server.I2P.EnabledExplicit, tt.wantExplicit)
			}
		})
	}
}

func TestBrandingDropInAndGeneratedServerConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	cfg := Default("/db")
	cfg.Server.Branding.Title = "must live in branding.yml"
	cfg.Server.SEO.Keywords = []string{"must", "live", "in", "branding"}
	cfg.Server.SEO.Sitemap.MaxURLs = 123
	cfg.Web.UI.Theme = "light"
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"branding:", "keywords:", "theme: light"} {
		if strings.Contains(string(serverData), forbidden) {
			t.Errorf("generated server.yml contains branding setting %q", forbidden)
		}
	}
	if strings.Contains(string(serverData), "max_urls: 123") {
		t.Fatal("server.yml contains a setting assigned to the branding area file")
	}
	branding := "server:\n  branding:\n    title: Operator Title\n    description: Operator description\n  seo:\n    keywords: [short links]\n    sitemap:\n      max_urls: 123\nweb:\n  ui:\n    theme: light\n"
	if err := os.WriteFile(filepath.Join(dir, "yaml.d", "branding.yml"), []byte(branding), 0o600); err != nil {
		t.Fatal(err)
	}
	brandingData, err := os.ReadFile(filepath.Join(dir, "yaml.d", "branding.yml"))
	if err != nil || !strings.Contains(string(brandingData), "max_urls: 123") {
		t.Fatalf("branding.yml = %q, err %v; want SEO endpoint settings", brandingData, err)
	}
	reloaded, err := Load(path, "/db")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.Server.Branding.Title != "Operator Title" || reloaded.Server.Branding.Description != "Operator description" {
		t.Errorf("loaded branding = %+v", reloaded.Server.Branding)
	}
	if strings.Join(reloaded.Server.SEO.Keywords, ",") != "short links" || reloaded.Server.SEO.Sitemap.MaxURLs != 123 {
		t.Errorf("loaded seo = %+v", reloaded.Server.SEO)
	}
	if reloaded.Web.UI.Theme != "light" {
		t.Errorf("loaded web.ui.theme = %q, want light", reloaded.Web.UI.Theme)
	}
}

func TestLoadMigratesLegacyCredentialsToSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	legacy := "server:\n  token: legacy-token\n  security:\n    encryption_key: legacy-key\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "/db")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Token != "legacy-token" || cfg.Server.Security.EncryptionKey != "legacy-key" {
		t.Fatalf("loaded credentials = token %q, key %q", cfg.Server.Token, cfg.Server.Security.EncryptionKey)
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverData), "legacy-token") || strings.Contains(string(serverData), "legacy-key") {
		t.Fatalf("legacy credentials remain in server.yml after migration: %s", serverData)
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretData), "legacy-token") || !strings.Contains(string(secretData), "legacy-key") {
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
		if !strings.HasPrefix(cfg.Server.Token, "adm_") {
			t.Errorf("Token = %q, want adm_ prefix", cfg.Server.Token)
		}
		if len(cfg.Server.Token) != len("adm_")+32 {
			t.Errorf("Token length = %d, want %d", len(cfg.Server.Token), len("adm_")+32)
		}
	})

	t.Run("leaves existing token alone", func(t *testing.T) {
		cfg := Default("/db")
		cfg.Server.Token = "adm_" + strings.Repeat("A", 32)
		generated, err := EnsureToken(cfg)
		if err != nil {
			t.Fatalf("EnsureToken() error = %v", err)
		}
		if generated {
			t.Error("generated = true, want false")
		}
		if cfg.Server.Token != "adm_"+strings.Repeat("A", 32) {
			t.Errorf("Token = %q, want unchanged", cfg.Server.Token)
		}
	})

	t.Run("replaces legacy token format", func(t *testing.T) {
		cfg := Default("/db")
		cfg.Server.Token = "legacy-token"
		generated, err := EnsureToken(cfg)
		if err != nil || !generated || !validGeneratedToken(cfg.Server.Token, "adm") {
			t.Fatalf("EnsureToken() = (%v, %v), token format valid = %v", generated, err, validGeneratedToken(cfg.Server.Token, "adm"))
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

func TestEnsureMetricsTokensAndSaveSecrets(t *testing.T) {
	cfg := Default("")
	generated, err := EnsureMetricsTokens(cfg)
	if err != nil || !generated {
		t.Fatalf("EnsureMetricsTokens() = %v, %v; want generated", generated, err)
	}
	values := []string{cfg.Server.Metrics.Auth.Tokens.Prometheus, cfg.Server.Metrics.Auth.Tokens.Grafana, cfg.Server.Metrics.Auth.Tokens.Loki}
	for _, value := range values {
		if !strings.HasPrefix(value, "mtr_") || len(value) != 36 {
			t.Errorf("generated token %q does not use the required token format", value)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if !strings.Contains(string(secretData), value) {
			t.Errorf("secrets.yml does not contain generated token %q", value)
		}
		if strings.Contains(string(serverData), value) {
			t.Errorf("server.yml contains generated token %q", value)
		}
	}
}

func TestCVEAPIKeyIsSavedOnlyInSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	cfg := Default("")
	cfg.Data.CVE.APIKey = "nvd-secret-key"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	secretData, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverData), "nvd-secret-key") {
		t.Fatal("server.yml contains the NVD API key")
	}
	if !strings.Contains(string(secretData), "nvd-secret-key") {
		t.Fatal("secrets.yml does not contain the NVD API key")
	}
	loaded, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Data.CVE.APIKey != "nvd-secret-key" {
		t.Fatalf("loaded API key = %q, want secret value", loaded.Data.CVE.APIKey)
	}
}

func TestLoadRemovesLegacyCVEKeyWhenSecretAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("data:\n  cve:\n    api_key: plaintext-old-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.yml"), []byte("data:\n  cve:\n    api_key: secret-file-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Data.CVE.APIKey != "secret-file-key" {
		t.Fatalf("loaded API key = %q, want secrets.yml value", loaded.Data.CVE.APIKey)
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverData), "plaintext-old-key") || strings.Contains(string(serverData), "api_key") {
		t.Fatalf("server.yml retained plaintext CVE key: %s", serverData)
	}
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

func TestDefaultPrivacyDisclosureMatchesSpec(t *testing.T) {
	privacy := Default("").Server.Privacy
	if !privacy.Data.StoredOnServer {
		t.Fatal("stored_on_server default = false, want true")
	}
	if len(privacy.Data.Sharing) != 3 {
		t.Fatalf("sharing defaults = %d entries, want analytics, email, user_initiated", len(privacy.Data.Sharing))
	}
	if !privacy.Retention.ExportAvailable || !privacy.Retention.DeletionAvailable {
		t.Fatal("privacy export and deletion availability must default to true")
	}
}
