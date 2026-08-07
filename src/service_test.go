package main

import (
	"strings"
	"testing"
)

func TestDetectServiceManagerContainer(t *testing.T) {
	// This assertion is only meaningful when the test process is actually
	// inside a container; otherwise it exercises the remaining detection
	// branches (systemd/launchd/runit/s6/sysv/rcd/manual) based on the
	// real host, which is inherently environment dependent. Either way
	// the call must not panic and must return one of the known values.
	got := detectServiceManager()
	known := map[string]bool{
		"container": true, "systemd": true, "launchd": true, "runit": true,
		"s6": true, "sysv": true, "rcd": true, "manual": true,
	}
	if !known[got] {
		t.Errorf("detectServiceManager() = %q, not a known value", got)
	}
}

func TestRunServiceHelp(t *testing.T) {
	for _, cmd := range []string{"", "help", "--help", "-h"} {
		t.Run("cmd="+cmd, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return runService("shortner", cmd) })
			if code != 0 {
				t.Errorf("code = %d, want 0", code)
			}
			if !strings.Contains(out, "Manage the shortner system service") {
				t.Errorf("output = %q, want service help text", out)
			}
			if !strings.Contains(out, "Detected service manager:") {
				t.Errorf("output = %q, want detected service manager line", out)
			}
		})
	}
}

func TestRunServiceActionsNotYetAvailable(t *testing.T) {
	for _, cmd := range []string{"start", "stop", "restart", "reload", "--install", "--uninstall", "--disable"} {
		t.Run(cmd, func(t *testing.T) {
			_, stderr, code := captureOutput(t, func() int { return runService("shortner", cmd) })
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			want := "--service " + cmd + " is not yet available"
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want to contain %q", stderr, want)
			}
		})
	}
}

func TestRunServiceUnknownCommand(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runService("shortner", "bogus") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --service command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}
