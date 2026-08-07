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
	if cfg.Server.Listen != "0.0.0.0" {
		t.Errorf("Listen = %q, want 0.0.0.0", cfg.Server.Listen)
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
	yamlContent := "server:\n  token: tok_existing\n  listen: 127.0.0.1\n  port: \"9999\"\n  baseurl: /short/\n  database:\n    driver: libsql\n    url: mydb\n"
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
