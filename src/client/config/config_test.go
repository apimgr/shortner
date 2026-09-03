package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDefaultMatchesSchema checks the compiled-in defaults against AI.md
// PART 32's cli.yml schema table so a stray edit is caught immediately.
func TestDefaultMatchesSchema(t *testing.T) {
	def := Default()
	if def.Server.APIVersion != "v1" || def.Server.Retry != 3 {
		t.Fatalf("unexpected server defaults: %+v", def.Server)
	}
	if def.Output.Format != "table" || def.Output.Color != "auto" {
		t.Fatalf("unexpected output defaults: %+v", def.Output)
	}
	if !def.TUI.Enabled || !def.TUI.Unicode {
		t.Fatalf("unexpected tui defaults: %+v", def.TUI)
	}
	if def.Display.Mode != "auto" {
		t.Fatalf("unexpected display default: %+v", def.Display)
	}
	if def.Defaults.Limit != 20 {
		t.Fatalf("unexpected defaults.limit: %d", def.Defaults.Limit)
	}
}

// TestLoadMissingFileReturnsDefaults ensures a first run works with zero
// configuration rather than erroring on a missing cli.yml.
func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.APIVersion != Default().Server.APIVersion {
		t.Fatalf("got %+v, want defaults", cfg)
	}
	if cfg.Path() != path {
		t.Fatalf("Path() = %q, want %q", cfg.Path(), path)
	}
}

// TestSaveLoadRoundTrip writes a config, reads it back, and checks that the
// values, file mode, and directory mode all match AI.md PART 32's
// requirements for a credential-bearing file.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "cli.yml")

	cfg := Default()
	cfg.SetPath(path)
	cfg.Server.Primary = "https://example.com"
	cfg.Auth.Token = "secret-token"
	cfg.Output.Format = "json"

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Server.Primary != "https://example.com" {
		t.Fatalf("Server.Primary = %q", loaded.Server.Primary)
	}
	if loaded.Auth.Token != "secret-token" {
		t.Fatalf("Auth.Token = %q", loaded.Auth.Token)
	}
	if loaded.Output.Format != "json" {
		t.Fatalf("Output.Format = %q", loaded.Output.Format)
	}

	if runtime.GOOS != "windows" {
		fileInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fileInfo.Mode().Perm(); perm != 0o600 {
			t.Fatalf("file mode = %v, want 0600", perm)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if perm := dirInfo.Mode().Perm(); perm != 0o700 {
			t.Fatalf("dir mode = %v, want 0700", perm)
		}
	}
}

// TestSaveNoPathIsAnError guards against a caller forgetting SetPath.
func TestSaveNoPathIsAnError(t *testing.T) {
	cfg := Default()
	if err := cfg.Save(); err == nil {
		t.Fatal("Save() with no path succeeded, want error")
	}
}

// TestLoadBlankFieldsFallBackToDefaults exercises normalize() against a
// hand-edited file that leaves several fields blank or invalid.
func TestLoadBlankFieldsFallBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yml")
	raw := "server:\n  primary: https://example.com\n  retry: -1\nlogging:\n  max_files: 0\ndefaults:\n  limit: -5\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := Default()
	if cfg.Server.Retry != def.Server.Retry {
		t.Fatalf("Server.Retry = %d, want default %d", cfg.Server.Retry, def.Server.Retry)
	}
	if cfg.Logging.MaxFiles != def.Logging.MaxFiles {
		t.Fatalf("Logging.MaxFiles = %d, want default %d", cfg.Logging.MaxFiles, def.Logging.MaxFiles)
	}
	if cfg.Defaults.Limit != def.Defaults.Limit {
		t.Fatalf("Defaults.Limit = %d, want default %d", cfg.Defaults.Limit, def.Defaults.Limit)
	}
	if cfg.Server.Primary != "https://example.com" {
		t.Fatalf("Server.Primary was clobbered: %q", cfg.Server.Primary)
	}
}

// TestLoadInvalidYAMLIsAnError covers the parse-failure branch.
func TestLoadInvalidYAMLIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yml")
	if err := os.WriteFile(path, []byte("server: [unterminated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() with malformed YAML succeeded, want error")
	}
}

// TestApplyEnv covers every SHORTNER_* override plus NO_COLOR, which is the
// "env" tier of the CLI flag > env > file > default precedence chain.
func TestApplyEnv(t *testing.T) {
	t.Setenv(EnvPrefix+"_SERVER_PRIMARY", "https://env.example.com")
	t.Setenv(EnvPrefix+"_TOKEN", "env-token")
	t.Setenv(EnvPrefix+"_TOKEN_FILE", "/tmp/does-not-matter")
	t.Setenv(EnvPrefix+"_OUTPUT", "yaml")
	t.Setenv(EnvPrefix+"_LANG", "fr")
	t.Setenv(EnvPrefix+"_DEBUG", "true")
	t.Setenv("NO_COLOR", "1")

	cfg := Default()
	cfg.Server.Primary = "https://file.example.com"
	cfg.ApplyEnv()

	if cfg.Server.Primary != "https://env.example.com" {
		t.Fatalf("Server.Primary = %q, env override did not win over file value", cfg.Server.Primary)
	}
	if cfg.Auth.Token != "env-token" {
		t.Fatalf("Auth.Token = %q", cfg.Auth.Token)
	}
	if cfg.Auth.TokenFile != "/tmp/does-not-matter" {
		t.Fatalf("Auth.TokenFile = %q", cfg.Auth.TokenFile)
	}
	if cfg.Output.Format != "yaml" {
		t.Fatalf("Output.Format = %q", cfg.Output.Format)
	}
	if cfg.Defaults.Lang != "fr" {
		t.Fatalf("Defaults.Lang = %q", cfg.Defaults.Lang)
	}
	if !cfg.Debug {
		t.Fatal("Debug = false, want true")
	}
	if cfg.Output.Color != "no" {
		t.Fatalf("Output.Color = %q, want %q from NO_COLOR", cfg.Output.Color, "no")
	}
}

// TestApplyEnvLeavesUnsetFieldsAlone ensures an env var that is not present
// never clobbers an existing file value — the "file wins over default" leg
// of the precedence chain.
func TestApplyEnvLeavesUnsetFieldsAlone(t *testing.T) {
	cfg := Default()
	cfg.Server.Primary = "https://file.example.com"
	cfg.ApplyEnv()
	if cfg.Server.Primary != "https://file.example.com" {
		t.Fatalf("Server.Primary = %q, want file value preserved", cfg.Server.Primary)
	}
}

// TestSaveIfEmptyOrInvalid covers all branches of AI.md PART 32's
// flag-to-config save rule table.
func TestSaveIfEmptyOrInvalid(t *testing.T) {
	cases := []struct {
		name        string
		current     string
		flagValue   string
		wantValue   string
		wantPersist bool
		wantWarn    bool
	}{
		{
			name:      "empty flag keeps current",
			current:   "https://current.example.com",
			flagValue: "",
			wantValue: "https://current.example.com",
		},
		{
			name:      "invalid flag keeps current and warns",
			current:   "https://current.example.com",
			flagValue: "not-a-url",
			wantValue: "https://current.example.com",
			wantWarn:  true,
		},
		{
			name:        "valid flag persists when current is empty",
			current:     "",
			flagValue:   "https://new.example.com",
			wantValue:   "https://new.example.com",
			wantPersist: true,
		},
		{
			name:        "valid flag persists when current is invalid",
			current:     "not-a-url",
			flagValue:   "https://new.example.com",
			wantValue:   "https://new.example.com",
			wantPersist: true,
		},
		{
			name:      "valid flag overrides a valid current without persisting",
			current:   "https://current.example.com",
			flagValue: "https://session-only.example.com",
			wantValue: "https://session-only.example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, persist, warn := SaveIfEmptyOrInvalid(tc.current, tc.flagValue, ValidateServerURL)
			if value != tc.wantValue {
				t.Errorf("value = %q, want %q", value, tc.wantValue)
			}
			if persist != tc.wantPersist {
				t.Errorf("persist = %v, want %v", persist, tc.wantPersist)
			}
			if warn != tc.wantWarn {
				t.Errorf("warn = %v, want %v", warn, tc.wantWarn)
			}
		})
	}
}

// TestValidateServerURL covers the boundary conditions of a usable server
// URL: scheme, host, empty, and query/path noise after the host.
func TestValidateServerURL(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"example.com", false},
		{"ftp://example.com", false},
		{"http://", false},
		{"http://example.com", true},
		{"https://example.com", true},
		{"HTTPS://Example.COM", true},
		{"https://example.com/", true},
		{"https://example.com/path?query=1", true},
		{"https://example.com:8080", true},
	}
	for _, tc := range cases {
		if got := ValidateServerURL(tc.value); got != tc.want {
			t.Errorf("ValidateServerURL(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// TestValidateToken covers empty, whitespace, and control-character token
// values, which would otherwise corrupt an Authorization header.
func TestValidateToken(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"tok_abc123", true},
		{"has space", false},
		{"has\ttab", false},
		{"has\nnewline", false},
		{"has\x7fdel", false},
	}
	for _, tc := range cases {
		if got := ValidateToken(tc.value); got != tc.want {
			t.Errorf("ValidateToken(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// TestValidateOutputFormat covers the supported and unsupported --output
// values.
func TestValidateOutputFormat(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"table", true},
		{"JSON", true},
		{"yaml", true},
		{"plain", true},
		{"csv", true},
		{"xml", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := ValidateOutputFormat(tc.value); got != tc.want {
			t.Errorf("ValidateOutputFormat(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// TestResolveToken covers the inline-token, token-file, and neither-set
// cases.
func TestResolveToken(t *testing.T) {
	t.Run("inline token wins", func(t *testing.T) {
		cfg := Default()
		cfg.Auth.Token = "inline"
		cfg.Auth.TokenFile = filepath.Join(t.TempDir(), "missing")
		if got := cfg.ResolveToken(); got != "inline" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("token file used when inline is empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Default()
		cfg.Auth.TokenFile = path
		if got := cfg.ResolveToken(); got != "from-file" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("neither set", func(t *testing.T) {
		cfg := Default()
		if got := cfg.ResolveToken(); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("missing token file", func(t *testing.T) {
		cfg := Default()
		cfg.Auth.TokenFile = filepath.Join(t.TempDir(), "missing")
		if got := cfg.ResolveToken(); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

// TestParseBoolIsTruthy sanity-checks the single boolean parser is actually
// wired through.
func TestParseBoolIsTruthy(t *testing.T) {
	if !ParseBool("yes", false) {
		t.Fatal("ParseBool(\"yes\", false) = false")
	}
	if ParseBool("no", true) {
		t.Fatal("ParseBool(\"no\", true) = true")
	}
	if !ParseBool("garbage", true) {
		t.Fatal("ParseBool with unparseable input should fall back to default")
	}
	if !IsTruthy("1") {
		t.Fatal("IsTruthy(\"1\") = false")
	}
}

// TestParseInt covers the empty, invalid, negative, and valid cases.
func TestParseInt(t *testing.T) {
	cases := []struct {
		value string
		def   int
		want  int
	}{
		{"", 20, 20},
		{"notanumber", 20, 20},
		{"-5", 20, 20},
		{"0", 20, 20},
		{"50", 20, 50},
	}
	for _, tc := range cases {
		if got := ParseInt(tc.value, tc.def); got != tc.want {
			t.Errorf("ParseInt(%q, %d) = %d, want %d", tc.value, tc.def, got, tc.want)
		}
	}
}
