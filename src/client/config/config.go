// Package config implements the client's cli.yml schema, loading, saving,
// environment overrides, and the flag-to-config save rules from AI.md
// PART 32.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/apimgr/shortner/src/client/paths"
	serverconfig "github.com/apimgr/shortner/src/config"
)

// EnvPrefix is the uppercased project name used for every client
// environment variable, per AI.md PART 32's `{PROJECT_NAME}_*` convention.
const EnvPrefix = "SHORTNER"

// ServerSection holds connection settings.
type ServerSection struct {
	Primary    string `yaml:"primary"`
	APIVersion string `yaml:"api_version"`
	Timeout    string `yaml:"timeout"`
	Retry      int    `yaml:"retry"`
	RetryDelay string `yaml:"retry_delay"`
}

// AuthSection holds the ownership token. The server itself is an open API;
// a token authorizes edit/delete of the links it owns.
type AuthSection struct {
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
}

// OutputSection controls rendering of command results.
type OutputSection struct {
	Format  string `yaml:"format"`
	Color   string `yaml:"color"`
	Pager   string `yaml:"pager"`
	Quiet   bool   `yaml:"quiet"`
	Verbose bool   `yaml:"verbose"`
}

// TUISection controls the interactive terminal interface.
type TUISection struct {
	Enabled bool   `yaml:"enabled"`
	Theme   string `yaml:"theme"`
	Mouse   bool   `yaml:"mouse"`
	Unicode bool   `yaml:"unicode"`
}

// DisplaySection is the ONLY supported display-mode override. AI.md PART 32
// forbids --tui/--cli/--gui flags entirely.
type DisplaySection struct {
	Mode string `yaml:"mode"`
}

// UpdateSection controls the client's self-update behavior.
type UpdateSection struct {
	Auto          bool   `yaml:"auto"`
	CheckInterval string `yaml:"check_interval"`
	Channel       string `yaml:"channel"`
}

// LoggingSection controls cli.log.
type LoggingSection struct {
	Level    string `yaml:"level"`
	File     string `yaml:"file"`
	MaxSize  string `yaml:"max_size"`
	MaxFiles int    `yaml:"max_files"`
}

// CacheSection controls client-side response caching.
type CacheSection struct {
	Enabled bool   `yaml:"enabled"`
	TTL     string `yaml:"ttl"`
	MaxSize string `yaml:"max_size"`
}

// DefaultsSection holds per-flag defaults applied when a flag is omitted.
type DefaultsSection struct {
	Lang   string `yaml:"lang"`
	Output string `yaml:"output"`
	Limit  int    `yaml:"limit"`
}

// Config is the full cli.yml document.
type Config struct {
	Server   ServerSection   `yaml:"server"`
	Auth     AuthSection     `yaml:"auth"`
	Output   OutputSection   `yaml:"output"`
	TUI      TUISection      `yaml:"tui"`
	Display  DisplaySection  `yaml:"display"`
	Update   UpdateSection   `yaml:"update"`
	Logging  LoggingSection  `yaml:"logging"`
	Cache    CacheSection    `yaml:"cache"`
	Debug    bool            `yaml:"debug"`
	Defaults DefaultsSection `yaml:"defaults"`

	// path records where this config was loaded from; it is never serialized.
	path string `yaml:"-"`
}

// Default returns the compiled-in defaults from AI.md PART 32's cli.yml
// schema table.
func Default() Config {
	return Config{
		Server: ServerSection{
			Primary:    "",
			APIVersion: "v1",
			Timeout:    "30s",
			Retry:      3,
			RetryDelay: "1s",
		},
		Auth: AuthSection{},
		Output: OutputSection{
			Format: "table",
			Color:  "auto",
			Pager:  "auto",
		},
		TUI: TUISection{
			Enabled: true,
			Theme:   "dark",
			Mouse:   true,
			Unicode: true,
		},
		Display: DisplaySection{Mode: "auto"},
		Update: UpdateSection{
			Auto:          false,
			CheckInterval: "per_invocation",
			Channel:       "stable",
		},
		Logging: LoggingSection{
			Level:    "warn",
			MaxSize:  "10MB",
			MaxFiles: 5,
		},
		Cache: CacheSection{
			Enabled: true,
			TTL:     "5m",
			MaxSize: "100MB",
		},
		Defaults: DefaultsSection{
			Lang:   "auto",
			Output: "table",
			Limit:  20,
		},
	}
}

// Path reports the file this config was loaded from or will be saved to.
func (c *Config) Path() string { return c.path }

// SetPath records the file this config belongs to.
func (c *Config) SetPath(path string) { c.path = path }

// Load reads cli.yml from path, filling any missing field with its default.
// A missing file is not an error — the defaults are returned so the client
// works with zero configuration on first run.
func Load(path string) (Config, error) {
	cfg := Default()
	cfg.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.path = path
	cfg.normalize()
	return cfg, nil
}

// normalize restores defaults for any field a hand-edited file left blank.
func (c *Config) normalize() {
	def := Default()
	if c.Server.APIVersion == "" {
		c.Server.APIVersion = def.Server.APIVersion
	}
	if c.Server.Timeout == "" {
		c.Server.Timeout = def.Server.Timeout
	}
	if c.Server.RetryDelay == "" {
		c.Server.RetryDelay = def.Server.RetryDelay
	}
	if c.Server.Retry < 0 {
		c.Server.Retry = def.Server.Retry
	}
	if c.Output.Format == "" {
		c.Output.Format = def.Output.Format
	}
	if c.Output.Color == "" {
		c.Output.Color = def.Output.Color
	}
	if c.Output.Pager == "" {
		c.Output.Pager = def.Output.Pager
	}
	if c.TUI.Theme == "" {
		c.TUI.Theme = def.TUI.Theme
	}
	if c.Display.Mode == "" {
		c.Display.Mode = def.Display.Mode
	}
	if c.Update.CheckInterval == "" {
		c.Update.CheckInterval = def.Update.CheckInterval
	}
	if c.Update.Channel == "" {
		c.Update.Channel = def.Update.Channel
	}
	if c.Logging.Level == "" {
		c.Logging.Level = def.Logging.Level
	}
	if c.Logging.MaxSize == "" {
		c.Logging.MaxSize = def.Logging.MaxSize
	}
	if c.Logging.MaxFiles <= 0 {
		c.Logging.MaxFiles = def.Logging.MaxFiles
	}
	if c.Cache.TTL == "" {
		c.Cache.TTL = def.Cache.TTL
	}
	if c.Cache.MaxSize == "" {
		c.Cache.MaxSize = def.Cache.MaxSize
	}
	if c.Defaults.Lang == "" {
		c.Defaults.Lang = def.Defaults.Lang
	}
	if c.Defaults.Output == "" {
		c.Defaults.Output = def.Defaults.Output
	}
	if c.Defaults.Limit <= 0 {
		c.Defaults.Limit = def.Defaults.Limit
	}
}

// Save writes cli.yml with user-only permissions. The file carries the API
// token, so AI.md PART 32 mandates 0600 on Unix and a user-only ACL on
// Windows.
func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("config path is not set")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.path, data, 0o600); err != nil {
		return err
	}
	return paths.SecureFile(c.path)
}

// ApplyEnv applies the SHORTNER_* environment overrides. Environment values
// are session-only and are NEVER written back to cli.yml.
func (c *Config) ApplyEnv() {
	if v := os.Getenv(EnvPrefix + "_SERVER_PRIMARY"); v != "" {
		c.Server.Primary = v
	}
	if v := os.Getenv(EnvPrefix + "_TOKEN"); v != "" {
		c.Auth.Token = v
	}
	if v := os.Getenv(EnvPrefix + "_TOKEN_FILE"); v != "" {
		c.Auth.TokenFile = v
	}
	if v := os.Getenv(EnvPrefix + "_OUTPUT"); v != "" {
		c.Output.Format = v
	}
	if v := os.Getenv(EnvPrefix + "_LANG"); v != "" {
		c.Defaults.Lang = v
	}
	if v := os.Getenv(EnvPrefix + "_DEBUG"); v != "" {
		c.Debug = serverconfig.IsTruthy(v)
	}
	if os.Getenv("NO_COLOR") != "" {
		c.Output.Color = "no"
	}
}

// ResolveToken returns the effective token, reading auth.token_file when the
// inline token is empty. A token file keeps the credential out of cli.yml.
func (c *Config) ResolveToken() string {
	if c.Auth.Token != "" {
		return c.Auth.Token
	}
	if c.Auth.TokenFile == "" {
		return ""
	}
	data, err := os.ReadFile(c.Auth.TokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveIfEmptyOrInvalid implements AI.md PART 32's flag-to-config save rules.
// It returns the value to use for this session and whether the caller should
// persist it. A flag never overwrites an existing valid config value.
func SaveIfEmptyOrInvalid(current, flagValue string, validate func(string) bool) (value string, persist bool, warn bool) {
	if flagValue == "" {
		return current, false, false
	}
	if !validate(flagValue) {
		return current, false, true
	}
	if current == "" || !validate(current) {
		return flagValue, true, false
	}
	return flagValue, false, false
}

// ValidateServerURL reports whether a server URL is usable — it must carry
// an http or https scheme and a host.
func ValidateServerURL(value string) bool {
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	rest := value[strings.Index(value, "//")+2:]
	host := rest
	if idx := strings.IndexAny(rest, "/?#"); idx >= 0 {
		host = rest[:idx]
	}
	return host != ""
}

// ValidateToken reports whether a token looks like a usable bearer
// credential — non-empty and free of whitespace or control characters.
func ValidateToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidateOutputFormat reports whether an output format is supported.
func ValidateOutputFormat(value string) bool {
	switch strings.ToLower(value) {
	case "table", "json", "yaml", "plain", "csv":
		return true
	default:
		return false
	}
}

// ParseBool routes every client boolean through the project's single
// boolean parser, per AI.md PART 32's "NEVER strconv.ParseBool" rule.
func ParseBool(value string, defaultValue bool) bool {
	parsed, err := serverconfig.ParseBool(value, defaultValue)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// IsTruthy reports whether a string is one of the project's truthy values.
func IsTruthy(value string) bool {
	return serverconfig.IsTruthy(value)
}

// ParseInt parses a positive integer flag value, falling back to a default.
func ParseInt(value string, defaultValue int) int {
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}
