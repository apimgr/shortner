package main

import (
	"strings"
	"testing"
)

func TestRunUpdateHelp(t *testing.T) {
	for _, cmd := range []string{"help", "--help", "-h"} {
		t.Run(cmd, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return runUpdate("shortner", cmd, "") })
			if code != 0 {
				t.Errorf("code = %d, want 0", code)
			}
			if !strings.Contains(out, "Check for and perform self-updates") {
				t.Errorf("output = %q, want update help text", out)
			}
		})
	}
}

func TestRunUpdateCheckAndYes(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"", "--update check is not yet available"},
		{"check", "--update check is not yet available"},
		{"yes", "--update yes is not yet available"},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", tt.cmd, "") })
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr = %q, want %q", stderr, tt.want)
			}
		})
	}
}

func TestRunUpdateBranch(t *testing.T) {
	for _, ch := range []string{"stable", "beta", "daily"} {
		t.Run(ch, func(t *testing.T) {
			_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", "branch", ch) })
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			want := "--update branch " + ch + " is not yet available"
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want %q", stderr, want)
			}
		})
	}
}

func TestRunUpdateBranchMissingArg(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", "branch", "") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "requires stable, beta, or daily") {
		t.Errorf("stderr = %q, want requires-arg message", stderr)
	}
}

func TestRunUpdateBranchInvalidArg(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", "branch", "nightly") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "requires stable, beta, or daily") {
		t.Errorf("stderr = %q, want requires-arg message for invalid branch", stderr)
	}
}

func TestRunUpdateUnknownCommand(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", "bogus", "") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --update command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestDefaultLabel(t *testing.T) {
	if got := defaultLabel(""); got != "check" {
		t.Errorf("defaultLabel(\"\") = %q, want check", got)
	}
	if got := defaultLabel("yes"); got != "yes" {
		t.Errorf("defaultLabel(yes) = %q, want yes", got)
	}
}
