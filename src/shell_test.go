package main

import (
	"strings"
	"testing"
)

func TestDetectShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")
	if got := detectShell(); got != "zsh" {
		t.Errorf("detectShell() = %q, want zsh", got)
	}
	t.Setenv("SHELL", "")
	// filepath.Base("") returns "." (its documented behavior for an empty
	// path), not "" — detectShell inherits that quirk since it never
	// special-cases the empty-env case itself.
	if got := detectShell(); got != "." {
		t.Errorf("detectShell() = %q, want %q", got, ".")
	}
}

func TestRunShellHelp(t *testing.T) {
	for _, cmd := range []string{"", "help", "--help", "-h"} {
		t.Run("cmd="+cmd, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return runShell("shortner", cmd, "") })
			if code != 0 {
				t.Errorf("runShell(%q) code = %d, want 0", cmd, code)
			}
			if !strings.Contains(out, "Shell integration") {
				t.Errorf("runShell(%q) output = %q, want shell help text", cmd, out)
			}
		})
	}
}

func TestRunShellUnknownCommand(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return runShell("shortner", "bogus", "") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --shell command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestRunShellCompletionsExplicitShell(t *testing.T) {
	tests := []struct {
		shell string
		want  string
	}{
		{"bash", "bash completion"},
		{"zsh", "zsh completion"},
		{"fish", "fish completion"},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return runShell("shortner", "completions", tt.shell) })
			if code != 0 {
				t.Errorf("code = %d, want 0", code)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("output = %q, want to contain %q", out, tt.want)
			}
		})
	}
}

func TestRunShellInitUsesSameScriptAsCompletions(t *testing.T) {
	outInit, _, _ := captureOutput(t, func() int { return runShell("shortner", "init", "bash") })
	outCompletions, _, _ := captureOutput(t, func() int { return runShell("shortner", "completions", "bash") })
	if outInit != outCompletions {
		t.Errorf("init and completions output differ:\ninit=%q\ncompletions=%q", outInit, outCompletions)
	}
}

func TestRunShellCompletionsDetectsFromEnv(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	out, _, code := captureOutput(t, func() int { return runShell("shortner", "completions", "") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "bash completion") {
		t.Errorf("output = %q, want bash completion (detected from $SHELL)", out)
	}
}

// TestCompletionScriptEmptyShellName covers completionScript's own "could
// not detect" branch directly. In practice runShell never passes an empty
// shellName through to completionScript — filepath.Base("") (what
// detectShell() falls back to for an unset $SHELL) returns "." rather than
// "" — so this branch is otherwise unreachable via the CLI; it is still
// real defensive code worth covering as a unit, since completionScript is
// exported to the package's other callers too.
func TestCompletionScriptEmptyShellName(t *testing.T) {
	_, err := completionScript("shortner", "")
	if err == nil {
		t.Fatal("completionScript() error = nil, want could-not-detect error")
	}
	if !strings.Contains(err.Error(), "could not detect shell") {
		t.Errorf("error = %q, want could-not-detect message", err.Error())
	}
}

func TestCompletionScriptUnsupportedShell(t *testing.T) {
	_, err := completionScript("shortner", "powershell")
	if err == nil {
		t.Fatal("completionScript() error = nil, want unsupported-shell error")
	}
	if !strings.Contains(err.Error(), "unsupported shell") {
		t.Errorf("error = %q, want unsupported shell message", err.Error())
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("shortner-cli"); got != "shortner_cli" {
		t.Errorf("sanitize() = %q, want shortner_cli", got)
	}
	if got := sanitize("shortner"); got != "shortner" {
		t.Errorf("sanitize() = %q, want unchanged", got)
	}
}

func TestCompletionScriptsContainAllFlags(t *testing.T) {
	// bash/zsh embed shellFlags verbatim; fish generates its own
	// per-flag lines from a separate table (see fishCompletion) and
	// doesn't reuse "-h"/"-v" as standalone tokens, so it is checked
	// with the long-form subset only.
	for _, shellName := range []string{"bash", "zsh"} {
		script, err := completionScript("shortner", shellName)
		if err != nil {
			t.Fatalf("completionScript(%q) error = %v", shellName, err)
		}
		for _, flag := range shellFlags {
			if !strings.Contains(script, flag) {
				t.Errorf("%s completion missing flag %q", shellName, flag)
			}
		}
	}

	fishScript, err := completionScript("shortner", "fish")
	if err != nil {
		t.Fatalf("completionScript(fish) error = %v", err)
	}
	for _, name := range []string{"help", "version", "shell", "mode", "service", "maintenance", "update"} {
		if !strings.Contains(fishScript, "-l "+name) {
			t.Errorf("fish completion missing flag %q", name)
		}
	}
}
