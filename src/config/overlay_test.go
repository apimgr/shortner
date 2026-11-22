package config

import (
	"strings"
	"testing"
)

func TestTorStartupTimeoutDefaultsAndValidation(t *testing.T) {
	defaults := DefaultTor()
	if defaults.StartupTimeout != 60 {
		t.Fatalf("DefaultTor().StartupTimeout = %d, want 60", defaults.StartupTimeout)
	}
	if got := (Tor{StartupTimeout: 9}).Normalized().StartupTimeout; got != 60 {
		t.Errorf("invalid startup timeout normalized to %d, want 60", got)
	}
	warnings := ValidateTor(Tor{StartupTimeout: 301})
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "server.tor.startup_timeout") {
		t.Errorf("ValidateTor() warnings = %v, want startup_timeout warning", warnings)
	}
}

func TestApplyI2PEnvHasPriorityOverConfig(t *testing.T) {
	t.Run("environment true overrides explicit config false", func(t *testing.T) {
		t.Setenv("I2P_ENABLED", "true")
		cfg := Default("")
		cfg.Server.I2P.Enabled = false
		cfg.Server.I2P.EnabledExplicit = true
		if warnings := ApplyI2PEnv(cfg); len(warnings) != 0 {
			t.Fatalf("ApplyI2PEnv() warnings = %v", warnings)
		}
		if !cfg.Server.I2P.Enabled {
			t.Fatal("I2P_ENABLED=true did not override explicit config false")
		}
	})
	t.Run("runtime false disables config true", func(t *testing.T) {
		t.Setenv("I2P_ENABLED", "false")
		cfg := Default("")
		cfg.Server.I2P.Enabled = true
		cfg.Server.I2P.EnabledExplicit = true
		ApplyI2PEnv(cfg)
		if cfg.Server.I2P.Enabled {
			t.Fatal("I2P_ENABLED=false did not disable I2P")
		}
	})
	t.Run("environment enables when config is not explicit", func(t *testing.T) {
		t.Setenv("I2P_ENABLED", "true")
		cfg := Default("")
		ApplyI2PEnv(cfg)
		if !cfg.Server.I2P.Enabled {
			t.Fatal("I2P_ENABLED=true did not enable I2P")
		}
	})
}
