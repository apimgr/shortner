package config

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		def     time.Duration
		want    time.Duration
		wantErr bool
	}{
		{"empty uses default", "", 30 * time.Second, 30 * time.Second, false},
		{"seconds", "45s", 30 * time.Second, 45 * time.Second, false},
		{"minutes", "2m", 30 * time.Second, 2 * time.Minute, false},
		{"invalid falls back to default", "notaduration", 30 * time.Second, 30 * time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.in, tt.def)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDuration(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateDatabaseCacheDurations(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Database.Driver = "libsql"
	cfg.Server.Database.Cache.Enabled = true
	cfg.Server.Database.Cache.SyncInterval = "0s"
	cfg.Server.Database.Cache.MaxAge = "invalid"
	warnings := Validate(cfg)
	if cfg.Server.Database.Cache.SyncInterval != "30s" || cfg.Server.Database.Cache.MaxAge != "1h" {
		t.Fatalf("database cache durations = %+v", cfg.Server.Database.Cache)
	}
	if !anyContains(warnings, "server.database.cache.sync_interval") || !anyContains(warnings, "server.database.cache.max_age") {
		t.Fatalf("Validate() warnings = %v, want cache duration warnings", warnings)
	}
}

func TestValidateMaintenanceSelfHealingAndCleanup(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Maintenance.SelfHealing.RetryInterval = "0s"
	cfg.Server.Maintenance.SelfHealing.MaxAttempts = -1
	cfg.Server.Maintenance.Cleanup.DiskThreshold = 101
	cfg.Server.Maintenance.Cleanup.LogRetentionDays = -1
	warnings := Validate(cfg)
	if cfg.Server.Maintenance.SelfHealing.RetryInterval != "30s" || cfg.Server.Maintenance.SelfHealing.MaxAttempts != 0 {
		t.Fatalf("self-healing config = %+v", cfg.Server.Maintenance.SelfHealing)
	}
	if cfg.Server.Maintenance.Cleanup.DiskThreshold != 90 || cfg.Server.Maintenance.Cleanup.LogRetentionDays != 7 {
		t.Fatalf("cleanup config = %+v", cfg.Server.Maintenance.Cleanup)
	}
	for _, name := range []string{"retry_interval", "max_attempts", "disk_threshold", "log_retention_days"} {
		if !anyContains(warnings, name) {
			t.Errorf("Validate() warnings = %v, missing %q", warnings, name)
		}
	}
}

func TestValidateDisablesDatabaseCacheForSQLite(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Database.Cache.Enabled = true
	warnings := Validate(cfg)
	if cfg.Server.Database.Cache.Enabled {
		t.Fatal("Validate() left cache enabled with the SQLite driver")
	}
	if !anyContains(warnings, "server.database.cache.enabled") {
		t.Fatalf("Validate() warnings = %v, want SQLite cache incompatibility warning", warnings)
	}
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		def     int64
		want    int64
		wantErr bool
	}{
		{"empty uses default", "", 10, 10, false},
		{"plain bytes", "100", 10, 100, false},
		{"kilobytes", "512KB", 10, 512 << 10, false},
		{"megabytes", "10MB", 10, 10 << 20, false},
		{"gigabytes", "1GB", 10, 1 << 30, false},
		{"lowercase suffix", "5mb", 10, 5 << 20, false},
		{"negative rejected", "-5", 10, 10, true},
		{"garbage rejected", "notasize", 10, 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSize(tt.in, tt.def)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSize(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseSize(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidateReplacesInvalidValues(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Mode = "staging"
	cfg.Server.Port = "not-a-port"
	cfg.Server.Limits.MaxBodySize = "bogus"
	cfg.Server.Limits.ReadTimeout = "bogus"
	cfg.Server.Compression.Level = 99
	cfg.Server.Compression.Types = nil
	cfg.Server.RateLimit.Read.Requests = 0
	cfg.Server.RateLimit.GlobalBurst = -1
	cfg.Server.Cache.Type = "bogus"

	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want several")
	}

	def := Default("/db")
	if cfg.Server.Port != def.Server.Port {
		t.Errorf("Port = %q, want default %q", cfg.Server.Port, def.Server.Port)
	}
	if cfg.Server.Mode != def.Server.Mode {
		t.Errorf("Mode = %q, want default %q", cfg.Server.Mode, def.Server.Mode)
	}
	if cfg.Server.Limits.MaxBodySize != def.Server.Limits.MaxBodySize {
		t.Errorf("MaxBodySize = %q, want default %q", cfg.Server.Limits.MaxBodySize, def.Server.Limits.MaxBodySize)
	}
	if cfg.Server.Limits.ReadTimeout != def.Server.Limits.ReadTimeout {
		t.Errorf("ReadTimeout = %q, want default %q", cfg.Server.Limits.ReadTimeout, def.Server.Limits.ReadTimeout)
	}
	if cfg.Server.Compression.Level != def.Server.Compression.Level {
		t.Errorf("Compression.Level = %d, want default %d", cfg.Server.Compression.Level, def.Server.Compression.Level)
	}
	if len(cfg.Server.Compression.Types) == 0 {
		t.Error("Compression.Types = empty, want default types")
	}
	if cfg.Server.RateLimit.Read.Requests != def.Server.RateLimit.Read.Requests {
		t.Errorf("RateLimit.Read.Requests = %d, want default %d", cfg.Server.RateLimit.Read.Requests, def.Server.RateLimit.Read.Requests)
	}
	if cfg.Server.RateLimit.GlobalBurst != def.Server.RateLimit.GlobalBurst {
		t.Errorf("RateLimit.GlobalBurst = %d, want default %d", cfg.Server.RateLimit.GlobalBurst, def.Server.RateLimit.GlobalBurst)
	}
	if cfg.Server.Cache.Type != def.Server.Cache.Type {
		t.Errorf("Cache.Type = %q, want default %q", cfg.Server.Cache.Type, def.Server.Cache.Type)
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	cfg := Default("/db")
	if warnings := Validate(cfg); len(warnings) != 0 {
		t.Errorf("Validate(default config) = %v, want no warnings", warnings)
	}
}

func TestValidatePortSettings(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{value: "8090", valid: true},
		{value: "0", valid: false},
		{value: "80,443", valid: true},
		{value: "8090,8443", valid: true},
		{value: "", valid: false},
		{value: "65536", valid: false},
		{value: "0,443", valid: false},
		{value: "80,0", valid: false},
		{value: "80,443,8080", valid: false},
		{value: "http,443", valid: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			cfg := Default("/db")
			cfg.Server.Port = test.value
			warnings := Validate(cfg)
			gotValid := cfg.Server.Port == test.value && len(warnings) == 0
			if gotValid != test.valid {
				t.Errorf("Validate(port %q) = valid %v (normalized %q, warnings %v), want %v", test.value, gotValid, cfg.Server.Port, warnings, test.valid)
			}
		})
	}
}

func TestValidateAcceptsCacheTypeNone(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Cache.Type = "none"
	if warnings := Validate(cfg); len(warnings) != 0 {
		t.Errorf("Validate() = %v, want no warnings for cache.type=none", warnings)
	}
	if cfg.Server.Cache.Type != "none" {
		t.Errorf("Cache.Type = %q, want unchanged \"none\"", cfg.Server.Cache.Type)
	}
}

func TestValidateSEOVerification(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.SEO.Verification.Google = "valid_Code-123"
	cfg.Server.SEO.Verification.Bing = "not-hex"
	cfg.Server.SEO.Verification.Custom = []SEOCustomMetaTag{
		{Name: "valid_tag", Content: "code"},
		{Name: "bad tag", Content: "code"},
	}
	warnings := Validate(cfg)
	if len(warnings) != 2 {
		t.Fatalf("Validate() warnings = %v, want invalid provider and custom tag warnings", warnings)
	}
	if cfg.Server.SEO.Verification.Google != "valid_Code-123" || cfg.Server.SEO.Verification.Bing != "" {
		t.Errorf("provider values = %+v, want valid Google retained and Bing omitted", cfg.Server.SEO.Verification)
	}
	if len(cfg.Server.SEO.Verification.Custom) != 1 || cfg.Server.SEO.Verification.Custom[0].Name != "valid_tag" {
		t.Errorf("custom verification tags = %+v, want only valid entry", cfg.Server.SEO.Verification.Custom)
	}
}

func TestValidateBrandingAdjustsAccentForEachTheme(t *testing.T) {
	cfg := Default("")
	cfg.Web.UI.Accent = "#888888"
	warnings := ValidateBranding(cfg)
	if cfg.Web.UI.Accent != "#888888" || len(warnings) != 0 {
		t.Fatalf("accent validation = %q, %v", cfg.Web.UI.Accent, warnings)
	}
	for theme, background := range map[string]string{"dark": "#282a36", "light": "#ffffff"} {
		adjusted := AccentForTheme(cfg.Web.UI.Accent, theme)
		if accentContrast(adjusted, background) < 4.5 {
			t.Errorf("%s accent %s does not meet 4.5:1 contrast against %s", theme, adjusted, background)
		}
	}
	if AccentForTheme("invalid", "dark") != "" {
		t.Fatal("invalid accent produced a theme color")
	}
}
