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
// Longer suffixes are checked first so "MB" is not misread as "B". The
// single-letter forms exist because AI.md PART 21 writes the backup size
// cap as "50G".
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"TB", 1 << 40},
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"T", 1 << 40},
	{"G", 1 << 30},
	{"M", 1 << 20},
	{"K", 1 << 10},
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

	warnings = append(warnings, validateBackup(cfg, defaults)...)
	warnings = append(warnings, validateUpdate(cfg, defaults)...)

	return warnings
}

// validateUpdate applies AI.md PART 22 "Update Configuration" to
// `server.update`: the branch must name one of the three release channels
// and defer_days must fall in the documented 0-365 window. Both follow the
// PART 12 Config Validation Rule — warn and replace, never fail startup.
func validateUpdate(cfg *Config, defaults *Config) []string {
	var warnings []string
	u := &cfg.Server.Update
	du := defaults.Server.Update

	switch u.Branch {
	case "stable", "beta", "daily":
	case "":
		u.Branch = du.Branch
	default:
		warnings = append(warnings, fmt.Sprintf("invalid server.update.branch %q, using default %q", u.Branch, du.Branch))
		u.Branch = du.Branch
	}

	if u.DeferDays < 0 || u.DeferDays > 365 {
		warnings = append(warnings, fmt.Sprintf("invalid server.update.defer_days %d (valid range 0-365), using default %d", u.DeferDays, du.DeferDays))
		u.DeferDays = du.DeferDays
	}

	return warnings
}

// backupRetentionField describes one `server.backup.retention` counter for
// the shared validate-then-warn loop below.
type backupRetentionField struct {
	name  string
	field *int
	def   int
	// min is the lowest accepted value (1 for max_backups, which AI.md
	// PART 21 documents as "≥1"; 0 for the optional tiers).
	min int
	// warnAbove is the AI.md PART 21 "Warning Thresholds" recommendation;
	// values above it are accepted but warned about.
	warnAbove int
	// unit renders the parenthetical in the "exceeds recommended" warning.
	unit func(n int) string
}

// validateBackup applies AI.md PART 21 "Validation (warn, don't error -
// server must start)" and "Warning Thresholds (accept but warn)" to
// `server.backup`. Invalid values are replaced with their default in
// place; out-of-recommendation values are kept as written.
func validateBackup(cfg *Config, defaults *Config) []string {
	var warnings []string
	r := &cfg.Server.Backup.Retention
	dr := defaults.Server.Backup.Retention

	fields := []backupRetentionField{
		{"max_backups", &r.MaxBackups, dr.MaxBackups, 1, 7, func(n int) string {
			return fmt.Sprintf("%d days of daily backups", n)
		}},
		{"keep_weekly", &r.KeepWeekly, dr.KeepWeekly, 0, 8, func(n int) string {
			return fmt.Sprintf("%d weeks of weekly backups", n)
		}},
		{"keep_monthly", &r.KeepMonthly, dr.KeepMonthly, 0, 12, func(n int) string {
			if n%12 == 0 {
				return fmt.Sprintf("%d years of monthly backups", n/12)
			}
			return fmt.Sprintf("%d months of monthly backups", n)
		}},
		{"keep_yearly", &r.KeepYearly, dr.KeepYearly, 0, 2, func(n int) string {
			return fmt.Sprintf("%d years of yearly backups", n)
		}},
	}

	for _, f := range fields {
		if *f.field < f.min {
			warnings = append(warnings, fmt.Sprintf("server.backup.retention.%s: %d invalid, using default %d", f.name, *f.field, f.def))
			*f.field = f.def
			continue
		}
		if *f.field > f.warnAbove {
			warnings = append(warnings, fmt.Sprintf("server.backup.retention.%s: %d exceeds recommended %d (%s)", f.name, *f.field, f.warnAbove, f.unit(*f.field)))
		}
	}

	if _, err := ParseRetentionSize(r.MaxTotalSize, 0); err != nil {
		warnings = append(warnings, fmt.Sprintf("server.backup.retention.max_total_size: %q invalid, using default %q", r.MaxTotalSize, dr.MaxTotalSize))
		r.MaxTotalSize = dr.MaxTotalSize
	}

	threshold := &cfg.Server.Backup.DiskThreshold
	if *threshold < 1 || *threshold > 100 {
		warnings = append(warnings, fmt.Sprintf("server.backup.disk_threshold: %d invalid, using default %d", *threshold, defaults.Server.Backup.DiskThreshold))
		*threshold = defaults.Server.Backup.DiskThreshold
	}

	return warnings
}

// RetentionSize is a parsed `server.backup.retention.max_total_size`.
// Exactly one of Percent/Bytes is meaningful; Disabled wins over both.
type RetentionSize struct {
	Disabled bool
	// Percent is a whole percentage of the backup volume's total size
	// (e.g. 10 for "10%").
	Percent int
	// Bytes is an absolute cap (e.g. "50G" -> 50 * 1000^3).
	Bytes int64
}

// ParseRetentionSize parses AI.md PART 21's `max_total_size` value: a
// percentage of the backup volume ("10%"), an absolute size ("50G"), or
// any of the PART 5 falsy values (0, no, false, off, disable, disabled,
// ...), which disable the cap. def is returned alongside the error when
// the value is unparseable, so callers can warn-and-default per PART 21
// "Validation (warn, don't error)".
func ParseRetentionSize(s string, def int64) (RetentionSize, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || IsFalsy(trimmed) {
		return RetentionSize{Disabled: true}, nil
	}

	if strings.HasSuffix(trimmed, "%") {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(trimmed, "%")))
		if err != nil || n <= 0 || n > 100 {
			return RetentionSize{Bytes: def}, fmt.Errorf("config: invalid max_total_size %q", s)
		}
		return RetentionSize{Percent: n}, nil
	}

	n, err := ParseSize(trimmed, 0)
	if err != nil || n <= 0 {
		return RetentionSize{Bytes: def}, fmt.Errorf("config: invalid max_total_size %q", s)
	}
	return RetentionSize{Bytes: n}, nil
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
