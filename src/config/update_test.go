package config

import (
	"strings"
	"testing"
)

func TestUpdateDefaults(t *testing.T) {
	// AI.md PART 22 "Update Configuration": stable channel, auto-install
	// off (installing is always an explicit operator decision), no defer
	// window.
	u := Default("").Server.Update
	if u.Branch != "stable" {
		t.Errorf("branch = %q, want stable", u.Branch)
	}
	if u.AutoInstall {
		t.Error("auto_install = true, want false by default")
	}
	if u.DeferDays != 0 {
		t.Errorf("defer_days = %d, want 0", u.DeferDays)
	}
}

func TestValidateUpdateBranch(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		want     string
		wantWarn bool
	}{
		{"stable", "stable", "stable", false},
		{"beta", "beta", "beta", false},
		{"daily", "daily", "daily", false},
		{"empty defaults silently", "", "stable", false},
		{"unknown channel warns", "nightly", "stable", true},
		{"wrong case warns", "Stable", "stable", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default("")
			cfg.Server.Update.Branch = tt.branch
			warnings := Validate(cfg)

			if got := cfg.Server.Update.Branch; got != tt.want {
				t.Errorf("branch = %q, want %q", got, tt.want)
			}
			got := updateWarning(warnings, "server.update.branch")
			if tt.wantWarn && got == "" {
				t.Errorf("warnings = %v, want one about server.update.branch", warnings)
			}
			if !tt.wantWarn && got != "" {
				t.Errorf("unexpected warning %q", got)
			}
		})
	}
}

func TestValidateUpdateDeferDays(t *testing.T) {
	tests := []struct {
		name     string
		days     int
		want     int
		wantWarn bool
	}{
		{"no window", 0, 0, false},
		{"thirty days", 30, 30, false},
		{"upper bound", 365, 365, false},
		{"negative", -1, 0, true},
		{"above a year", 366, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default("")
			cfg.Server.Update.DeferDays = tt.days
			warnings := Validate(cfg)

			if got := cfg.Server.Update.DeferDays; got != tt.want {
				t.Errorf("defer_days = %d, want %d", got, tt.want)
			}
			got := updateWarning(warnings, "server.update.defer_days")
			if tt.wantWarn && got == "" {
				t.Errorf("warnings = %v, want one about server.update.defer_days", warnings)
			}
			if !tt.wantWarn && got != "" {
				t.Errorf("unexpected warning %q", got)
			}
		})
	}
}

// updateWarning returns the first warning mentioning key, or "".
func updateWarning(warnings []string, key string) string {
	for _, w := range warnings {
		if strings.Contains(w, key) {
			return w
		}
	}
	return ""
}
