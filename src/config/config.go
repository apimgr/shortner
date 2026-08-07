// Package config loads and saves server.yml, the sole source of truth for
// operator configuration. See AI.md PART 5 for the authoritative schema.
package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
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
	Token          string         `yaml:"token"`
	Listen         string         `yaml:"listen"`
	Port           string         `yaml:"port"`
	BaseURL        string         `yaml:"baseurl"`
	Database       Database       `yaml:"database"`
	Limits         Limits         `yaml:"limits"`
	Compression    Compression    `yaml:"compression"`
	TrustedProxies TrustedProxies `yaml:"trusted_proxies"`
	RateLimit      RateLimit      `yaml:"rate_limit"`
	Cache          CacheConfig    `yaml:"cache"`
	Healthz        Healthz        `yaml:"healthz"`
	TLS            TLS            `yaml:"tls"`
}

// TLS holds `server.tls`, per AI.md PART 15 "Built-in Let's Encrypt
// Support". DNSCredentials is stored as plaintext YAML for now — AES-256-
// GCM encryption at rest (spec: "credentials_encrypted") depends on an app
// secret-encryption primitive this codebase does not have yet (tracked in
// TODO.AI.md).
type TLS struct {
	// Enabled turns on HTTPS/ACME certificate handling for the resolved
	// FQDN. When false (default), the server is HTTP-only.
	Enabled bool `yaml:"enabled"`
	// DNSProvider selects a DNS-01 provider (e.g. "cloudflare", "route53")
	// for wildcard certificate issuance. DNS-01 issuance itself is not
	// implemented yet — see TODO.AI.md; HTTP-01/TLS-ALPN-01 via the
	// certificate lookup order + ACME fallback in src/certmgr are.
	DNSProvider string `yaml:"dns_provider"`
	// DNSCredentials holds the provider-specific credential fields (e.g.
	// api_token, access_key_id), per AI.md PART 15 "Provider Credential
	// Storage".
	DNSCredentials map[string]string `yaml:"dns_credentials"`
}

// Database holds the `server.database` block.
type Database struct {
	// Driver is "sqlite" (default, pure Go modernc.org/sqlite) or "libsql".
	Driver string `yaml:"driver"`
	URL    string `yaml:"url"`
}

// Limits holds `server.limits`, per AI.md PART 12 "Request Limits".
// Durations and sizes are stored as their raw YAML strings ("30s",
// "10MB") and parsed on demand via ParseDuration/ParseSize so an invalid
// value can be replaced with a default (Validate) instead of failing
// startup.
type Limits struct {
	MaxBodySize  string `yaml:"max_body_size"`
	ReadTimeout  string `yaml:"read_timeout"`
	WriteTimeout string `yaml:"write_timeout"`
	IdleTimeout  string `yaml:"idle_timeout"`
}

// Compression holds `server.compression`, per AI.md PART 12 "Response
// Compression".
type Compression struct {
	Enabled bool     `yaml:"enabled"`
	Level   int      `yaml:"level"`
	Types   []string `yaml:"types"`
}

// TrustedProxies holds `server.trusted_proxies`, per AI.md PART 12
// "Trusted Proxies". Private ranges are always trusted regardless of this
// list; Additional extends the trust gate with public IPs/CIDRs/hostnames
// of upstream proxies.
type TrustedProxies struct {
	Additional []string `yaml:"additional"`
}

// RateLimit holds `server.rate_limit`, per AI.md PART 12 "Rate Limiting".
type RateLimit struct {
	Enabled     bool           `yaml:"enabled"`
	Read        RateLimitClass `yaml:"read"`
	Write       RateLimitClass `yaml:"write"`
	Health      RateLimitClass `yaml:"health"`
	GlobalBurst int            `yaml:"global_burst"`
}

// RateLimitClass is one per-minute-per-IP limit tier (read/write/health).
type RateLimitClass struct {
	Requests int `yaml:"requests"`
	Window   int `yaml:"window"`
}

// CacheConfig holds `server.cache`, per AI.md PART 12 "Cache
// Configuration". Only the "memory" (in-process, default) driver is
// implemented — "valkey"/"redis" depend on a client dependency not yet
// added (tracked in TODO.AI.md).
type CacheConfig struct {
	Type          string `yaml:"type"`
	URL           string `yaml:"url"`
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	DB            int    `yaml:"db"`
	TLS           bool   `yaml:"tls"`
	TLSSkipVerify bool   `yaml:"tls_skip_verify"`
	PoolSize      int    `yaml:"pool_size"`
	MinIdle       int    `yaml:"min_idle"`
	Timeout       string `yaml:"timeout"`
	Prefix        string `yaml:"prefix"`
	TTL           string `yaml:"ttl"`
}

// Healthz holds `server.healthz`, per AI.md PART 13 "Health Checks".
type Healthz struct {
	Root HealthzRoot `yaml:"root"`
}

// HealthzRoot controls the optional `/healthz` root alias.
type HealthzRoot struct {
	Enabled bool `yaml:"enabled"`
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
			Limits: Limits{
				MaxBodySize:  "10MB",
				ReadTimeout:  "30s",
				WriteTimeout: "30s",
				IdleTimeout:  "120s",
			},
			Compression: Compression{
				Enabled: true,
				Level:   5,
				Types: []string{
					"text/html",
					"text/css",
					"text/javascript",
					"application/json",
					"application/xml",
				},
			},
			RateLimit: RateLimit{
				Enabled:     true,
				Read:        RateLimitClass{Requests: 120, Window: 60},
				Write:       RateLimitClass{Requests: 10, Window: 60},
				Health:      RateLimitClass{Requests: 120, Window: 60},
				GlobalBurst: 240,
			},
			Cache: CacheConfig{
				Type:     "memory",
				Host:     "localhost",
				Port:     6379,
				PoolSize: 10,
				MinIdle:  2,
				Timeout:  "5s",
				Prefix:   "shortner:",
				TTL:      "1h",
			},
			TLS: TLS{
				Enabled: false,
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
