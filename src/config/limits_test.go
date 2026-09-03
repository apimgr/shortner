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
