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
