package main

import (
	"strings"
	"testing"
)

func TestRunMaintenanceHelp(t *testing.T) {
	for _, cmd := range []string{"", "help", "--help", "-h"} {
		t.Run("cmd="+cmd, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return runMaintenance("shortner", cmd, maintenanceOptions{}) })
			if code != 0 {
				t.Errorf("code = %d, want 0", code)
			}
			if !strings.Contains(out, "Perform server maintenance operations") {
				t.Errorf("output = %q, want maintenance help text", out)
			}
		})
	}
}

func TestRunMaintenanceKnownActionsNotYetAvailable(t *testing.T) {
	for cmd, part := range maintenanceReadDeps {
		t.Run(cmd, func(t *testing.T) {
			_, stderr, code := captureOutput(t, func() int { return runMaintenance("shortner", cmd, maintenanceOptions{}) })
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			wantMsg := "--maintenance " + cmd + " is not yet available"
			if !strings.Contains(stderr, wantMsg) {
				t.Errorf("stderr = %q, want to contain %q", stderr, wantMsg)
			}
			if !strings.Contains(stderr, part) {
				t.Errorf("stderr = %q, want to reference AI.md %q", stderr, part)
			}
		})
	}
}

func TestRunMaintenanceUnknownCommand(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runMaintenance("shortner", "bogus", maintenanceOptions{}) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --maintenance command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestMaintenanceReadDepsCoversEveryHelpCommand(t *testing.T) {
	// Regression guard: every command listed in maintenanceHelp's Commands
	// section must have a corresponding entry in maintenanceReadDeps, or
	// runMaintenance would wrongly report it as "unknown".
	// backup and restore are implemented (AI.md PART 21) and therefore
	// dispatch directly rather than through maintenanceReadDeps.
	implemented := map[string]bool{"backup": true, "restore": true}
	for _, cmd := range []string{"backup", "restore", "update", "mode", "setup", "pgp", "secret", "token", "data", "compliance"} {
		if implemented[cmd] {
			continue
		}
		if _, ok := maintenanceReadDeps[cmd]; !ok {
			t.Errorf("maintenanceReadDeps missing entry for documented command %q", cmd)
		}
	}
}
