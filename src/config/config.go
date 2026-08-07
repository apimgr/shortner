// Package config loads and saves server.yml, the sole source of truth for
// operator configuration. See AI.md PART 5 for the authoritative schema.
package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// tokenAlphabet is the URL-safe base62 alphabet used for generated tokens.
const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// Config is the root of server.yml.
type Config struct {
	Server Server `yaml:"server"`
}

// Server holds the top-level `server:` block of server.yml.
type Server struct {
	// Token is the global operator token (tok_ prefix). Auto-generated on
	// first run if empty. See AI.md PART 11 "API Token Model".
	Token    string   `yaml:"token"`
	Listen   string   `yaml:"listen"`
	Port     string   `yaml:"port"`
	BaseURL  string   `yaml:"baseurl"`
	Database Database `yaml:"database"`
}

// Database holds the `server.database` block.
type Database struct {
	// Driver is "sqlite" (default, pure Go modernc.org/sqlite) or "libsql".
	Driver string `yaml:"driver"`
	URL    string `yaml:"url"`
}

// Default returns a Config populated with the framework defaults, using
// dbPath as the SQLite database location for the current OS/privilege
// level (see src/paths).
func Default(dbPath string) *Config {
	return &Config{
		Server: Server{
			Listen:  "0.0.0.0",
			Port:    "8090",
			BaseURL: "/",
			Database: Database{
				Driver: "sqlite",
				URL:    dbPath,
			},
		},
	}
}

// Load reads server.yml from path. If the file does not exist, it returns
// Default(dbPath) without writing anything — callers decide when to persist
// a first-run config via Save.
func Load(path, dbPath string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(dbPath), nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := Default(dbPath)
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to path as server.yml, creating parent directories as
// needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("config: create dir for %s: %w", path, err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// EnsureToken generates a fresh operator token (`tok_` + 32 URL-safe base62
// chars) when cfg.Server.Token is empty, per AI.md PART 11 "API Token
// Model". Returns true when a new token was generated.
func EnsureToken(cfg *Config) (bool, error) {
	if cfg.Server.Token != "" {
		return false, nil
	}
	tok, err := generateToken()
	if err != nil {
		return false, err
	}
	cfg.Server.Token = tok
	return true, nil
}

func generateToken() (string, error) {
	const n = 32
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("config: generate token: %w", err)
	}
	var b strings.Builder
	b.WriteString("tok_")
	for _, v := range buf {
		b.WriteByte(tokenAlphabet[int(v)%len(tokenAlphabet)])
	}
	return b.String(), nil
}

// ParseBool parses truthy/falsy strings from env vars, config values, CLI
// flags, and API params — the single boolean-parsing entry point required
// by AI.md PART 5 "Boolean Handling".
func ParseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off", "":
		return false, nil
	default:
		return strconv.ParseBool(s)
	}
}

// IsTruthy is ParseBool with parse errors treated as false.
func IsTruthy(s string) bool {
	v, err := ParseBool(s)
	return err == nil && v
}
