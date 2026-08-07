// Package config — duration/size parsing and the config validation rule.
// See AI.md PART 12 "Config Validation Rule": "If config setting is
// invalid, warn and replace with default. Never fail startup."
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDuration parses a Go duration string ("30s", "5m", "1h"). An empty
// string returns def. An invalid string returns def and a non-nil error so
// callers can warn and continue, per the Config Validation Rule.
func ParseDuration(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def, fmt.Errorf("config: invalid duration %q: %w", s, err)
	}
	return d, nil
}

// sizeUnits maps case-insensitive byte-size suffixes to their multiplier.
// Longer suffixes are checked first so "MB" is not misread as "B".
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"B", 1},
}

// ParseSize parses a human byte-size string ("10MB", "512KB", "100") into
// bytes. An empty string returns def. An invalid string returns def and a
// non-nil error, per the Config Validation Rule.
func ParseSize(s string, def int64) (int64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return def, nil
	}
	upper := strings.ToUpper(trimmed)
	for _, u := range sizeUnits {
		if strings.HasSuffix(upper, u.suffix) {
			numPart := strings.TrimSpace(upper[:len(upper)-len(u.suffix)])
			n, err := strconv.ParseInt(numPart, 10, 64)
			if err != nil || n < 0 {
				return def, fmt.Errorf("config: invalid size %q", s)
			}
			return n * u.mult, nil
		}
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || n < 0 {
		return def, fmt.Errorf("config: invalid size %q", s)
	}
	return n, nil
}

// Validate checks every AI.md PART 12 config value, replacing invalid
// entries with their framework default in place and returning one warning
// message per replacement. Per the "Config Validation Rule": invalid
// config never fails startup — it is logged and defaulted.
func Validate(cfg *Config) []string {
	var warnings []string
	defaults := Default("")

	if _, err := strconv.Atoi(cfg.Server.Port); err != nil || !validPort(cfg.Server.Port) {
		warnings = append(warnings, fmt.Sprintf("invalid server.port %q, using default %q", cfg.Server.Port, defaults.Server.Port))
		cfg.Server.Port = defaults.Server.Port
	}

	if _, err := ParseSize(cfg.Server.Limits.MaxBodySize, 0); err != nil {
		warnings = append(warnings, fmt.Sprintf("invalid server.limits.max_body_size %q, using default %q", cfg.Server.Limits.MaxBodySize, defaults.Server.Limits.MaxBodySize))
		cfg.Server.Limits.MaxBodySize = defaults.Server.Limits.MaxBodySize
	}
	for name, field := range map[string]*string{
		"read_timeout":  &cfg.Server.Limits.ReadTimeout,
		"write_timeout": &cfg.Server.Limits.WriteTimeout,
		"idle_timeout":  &cfg.Server.Limits.IdleTimeout,
	} {
		if d, err := ParseDuration(*field, 0); err != nil || d <= 0 {
			def := defaultTimeout(name, defaults)
			warnings = append(warnings, fmt.Sprintf("invalid server.limits.%s %q, using default %q", name, *field, def))
			*field = def
		}
	}

	if cfg.Server.Compression.Level < 1 || cfg.Server.Compression.Level > 9 {
		warnings = append(warnings, fmt.Sprintf("invalid server.compression.level %d, using default %d", cfg.Server.Compression.Level, defaults.Server.Compression.Level))
		cfg.Server.Compression.Level = defaults.Server.Compression.Level
	}
	if len(cfg.Server.Compression.Types) == 0 {
		cfg.Server.Compression.Types = defaults.Server.Compression.Types
	}

	for name, class := range map[string]*RateLimitClass{
		"read":   &cfg.Server.RateLimit.Read,
		"write":  &cfg.Server.RateLimit.Write,
		"health": &cfg.Server.RateLimit.Health,
	} {
		def := defaultRateLimitClass(name, defaults)
		if class.Requests <= 0 {
			warnings = append(warnings, fmt.Sprintf("invalid server.rate_limit.%s.requests %d, using default %d", name, class.Requests, def.Requests))
			class.Requests = def.Requests
		}
		if class.Window <= 0 {
			warnings = append(warnings, fmt.Sprintf("invalid server.rate_limit.%s.window %d, using default %d", name, class.Window, def.Window))
			class.Window = def.Window
		}
	}
	if cfg.Server.RateLimit.GlobalBurst <= 0 {
		warnings = append(warnings, fmt.Sprintf("invalid server.rate_limit.global_burst %d, using default %d", cfg.Server.RateLimit.GlobalBurst, defaults.Server.RateLimit.GlobalBurst))
		cfg.Server.RateLimit.GlobalBurst = defaults.Server.RateLimit.GlobalBurst
	}

	switch cfg.Server.Cache.Type {
	case "", "none", "memory", "valkey", "redis":
		if cfg.Server.Cache.Type == "" {
			cfg.Server.Cache.Type = defaults.Server.Cache.Type
		}
	default:
		warnings = append(warnings, fmt.Sprintf("invalid server.cache.type %q, using default %q", cfg.Server.Cache.Type, defaults.Server.Cache.Type))
		cfg.Server.Cache.Type = defaults.Server.Cache.Type
	}

	return warnings
}

// validPort reports whether s parses to a TCP port in [1, 65535].
func validPort(s string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil && n >= 1 && n <= 65535
}

// defaultTimeout returns the framework default for one of the three
// server.limits.*_timeout fields, by name.
func defaultTimeout(name string, defaults *Config) string {
	switch name {
	case "read_timeout":
		return defaults.Server.Limits.ReadTimeout
	case "write_timeout":
		return defaults.Server.Limits.WriteTimeout
	default:
		return defaults.Server.Limits.IdleTimeout
	}
}

// defaultRateLimitClass returns the framework default for one of the three
// server.rate_limit.* classes, by name.
func defaultRateLimitClass(name string, defaults *Config) RateLimitClass {
	switch name {
	case "read":
		return defaults.Server.RateLimit.Read
	case "write":
		return defaults.Server.RateLimit.Write
	default:
		return defaults.Server.RateLimit.Health
	}
}
