package config

import (
	"strings"
	"testing"
)

func TestParseRetentionSize(t *testing.T) {
	cases := []struct {
		in       string
		disabled bool
		percent  int
		bytes    int64
		wantErr  bool
	}{
		{in: "10%", percent: 10},
		{in: " 100% ", percent: 100},
		{in: "50G", bytes: 50 * 1024 * 1024 * 1024},
		// Every AI.md PART 5 falsy value disables the cap.
		{in: "", disabled: true},
		{in: "0", disabled: true},
		{in: "false", disabled: true},
		{in: "no", disabled: true},
		{in: "never", disabled: true},
		{in: "disable", disabled: true},
		{in: "disabled", disabled: true},
		{in: "off", disabled: true},
		// Out-of-range and unparseable values are errors so the caller can
		// warn and fall back to the default.
		{in: "0%", wantErr: true},
		{in: "101%", wantErr: true},
		{in: "abc%", wantErr: true},
		{in: "banana", wantErr: true},
		{in: "-5G", wantErr: true},
	}

	for _, c := range cases {
		got, err := ParseRetentionSize(c.in, 4096)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseRetentionSize(%q) = %+v, want an error", c.in, got)
				continue
			}
			if got.Bytes != 4096 {
				t.Errorf("ParseRetentionSize(%q) fallback = %d, want the supplied default", c.in, got.Bytes)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRetentionSize(%q): %v", c.in, err)
			continue
		}
		if got.Disabled != c.disabled || got.Percent != c.percent || got.Bytes != c.bytes {
			t.Errorf("ParseRetentionSize(%q) = %+v, want disabled=%t percent=%d bytes=%d",
				c.in, got, c.disabled, c.percent, c.bytes)
		}
	}
}

// warningFor returns the first warning mentioning key, or "".
func warningFor(warnings []string, key string) string {
	for _, w := range warnings {
		if strings.Contains(w, key) {
			return w
		}
	}
	return ""
}

func TestValidateBackupDefaultsAreClean(t *testing.T) {
	cfg := Default("")
	defaults := Default("")
	if warnings := validateBackup(cfg, defaults); len(warnings) != 0 {
		t.Errorf("validateBackup on the defaults warned: %v", warnings)
	}
}

func TestValidateBackupReplacesInvalidValues(t *testing.T) {
	cfg := Default("")
	defaults := Default("")

	cfg.Server.Backup.Retention.MaxBackups = 0
	cfg.Server.Backup.Retention.KeepWeekly = -1
	cfg.Server.Backup.Retention.KeepMonthly = -12
	cfg.Server.Backup.Retention.KeepYearly = -2
	cfg.Server.Backup.Retention.MaxTotalSize = "banana"
	cfg.Server.Backup.DiskThreshold = 0

	warnings := validateBackup(cfg, defaults)

	dr := defaults.Server.Backup.Retention
	r := cfg.Server.Backup.Retention
	if r.MaxBackups != dr.MaxBackups || r.KeepWeekly != dr.KeepWeekly ||
		r.KeepMonthly != dr.KeepMonthly || r.KeepYearly != dr.KeepYearly {
		t.Errorf("retention = %+v, want the defaults %+v", r, dr)
	}
	if r.MaxTotalSize != dr.MaxTotalSize {
		t.Errorf("max_total_size = %q, want the default %q", r.MaxTotalSize, dr.MaxTotalSize)
	}
	if cfg.Server.Backup.DiskThreshold != defaults.Server.Backup.DiskThreshold {
		t.Errorf("disk_threshold = %d, want the default %d",
			cfg.Server.Backup.DiskThreshold, defaults.Server.Backup.DiskThreshold)
	}

	for _, key := range []string{
		"server.backup.retention.max_backups",
		"server.backup.retention.keep_weekly",
		"server.backup.retention.keep_monthly",
		"server.backup.retention.keep_yearly",
		"server.backup.retention.max_total_size",
		"server.backup.disk_threshold",
	} {
		w := warningFor(warnings, key)
		if w == "" {
			t.Errorf("no warning for %s (got %v)", key, warnings)
			continue
		}
		if !strings.Contains(w, "using default") {
			t.Errorf("warning for %s = %q, want an \"using default\" message", key, w)
		}
	}
}

func TestValidateBackupWarnsAboveRecommendedThresholds(t *testing.T) {
	// AI.md PART 21 "Warning Thresholds (accept but warn)": values above
	// the recommendation are kept as written, only warned about.
	cfg := Default("")
	defaults := Default("")

	cfg.Server.Backup.Retention.MaxBackups = 30
	cfg.Server.Backup.Retention.KeepWeekly = 12
	cfg.Server.Backup.Retention.KeepMonthly = 24
	cfg.Server.Backup.Retention.KeepYearly = 5

	warnings := validateBackup(cfg, defaults)

	r := cfg.Server.Backup.Retention
	if r.MaxBackups != 30 || r.KeepWeekly != 12 || r.KeepMonthly != 24 || r.KeepYearly != 5 {
		t.Fatalf("retention = %+v, want the operator's values kept verbatim", r)
	}

	cases := map[string]string{
		"server.backup.retention.max_backups":  "30 days of daily backups",
		"server.backup.retention.keep_weekly":  "12 weeks of weekly backups",
		"server.backup.retention.keep_monthly": "2 years of monthly backups",
		"server.backup.retention.keep_yearly":  "5 years of yearly backups",
	}
	for key, unit := range cases {
		w := warningFor(warnings, key)
		if w == "" {
			t.Errorf("no warning for %s (got %v)", key, warnings)
			continue
		}
		if !strings.Contains(w, "exceeds recommended") || !strings.Contains(w, unit) {
			t.Errorf("warning for %s = %q, want \"exceeds recommended\" and %q", key, w, unit)
		}
	}
}

func TestValidateBackupDiskThresholdRange(t *testing.T) {
	defaults := Default("")
	for _, threshold := range []int{0, -1, 101} {
		cfg := Default("")
		cfg.Server.Backup.DiskThreshold = threshold
		warnings := validateBackup(cfg, defaults)
		if warningFor(warnings, "server.backup.disk_threshold") == "" {
			t.Errorf("disk_threshold %d produced no warning", threshold)
		}
		if cfg.Server.Backup.DiskThreshold != defaults.Server.Backup.DiskThreshold {
			t.Errorf("disk_threshold %d was not reset to the default", threshold)
		}
	}

	// The boundaries themselves are valid.
	for _, threshold := range []int{1, 100} {
		cfg := Default("")
		cfg.Server.Backup.DiskThreshold = threshold
		if w := warningFor(validateBackup(cfg, defaults), "server.backup.disk_threshold"); w != "" {
			t.Errorf("disk_threshold %d warned: %q", threshold, w)
		}
	}
}
