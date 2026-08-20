package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestCompletionScriptAllShells covers every shell from AI.md PART 32's
// supported list: completionScript must produce non-empty, panic-free
// output for each one.
func TestCompletionScriptAllShells(t *testing.T) {
	for _, shell := range supportedShells {
		t.Run(shell, func(t *testing.T) {
			got := completionScript("shortner-cli", shell)
			if got == "" {
				t.Fatalf("completionScript(%q) returned empty output", shell)
			}
		})
	}
}

// TestCompletionScriptMentionsBinaryName spot-checks that the generated
// script actually references the (possibly renamed) binary rather than a
// hardcoded name.
func TestCompletionScriptMentionsBinaryName(t *testing.T) {
	for _, shell := range supportedShells {
		t.Run(shell, func(t *testing.T) {
			got := completionScript("my-renamed-cli", shell)
			if !strings.Contains(got, "my-renamed-cli") && !strings.Contains(got, "my_renamed_cli") {
				t.Fatalf("completionScript(%q) does not mention the binary name:\n%s", shell, got)
			}
		})
	}
}

// TestResolveShellExplicit covers every supported shell name passed
// explicitly.
func TestResolveShellExplicit(t *testing.T) {
	for _, shell := range supportedShells {
		t.Run(shell, func(t *testing.T) {
			got, err := resolveShell(shell)
			if err != nil {
				t.Fatalf("resolveShell(%q): %v", shell, err)
			}
			if got != shell {
				t.Fatalf("resolveShell(%q) = %q", shell, got)
			}
		})
	}
}

// TestResolveShellUnknown covers the error path for an unsupported shell.
func TestResolveShellUnknown(t *testing.T) {
	_, err := resolveShell("csh")
	if err == nil {
		t.Fatal("want error for an unsupported shell")
	}
}

// TestResolveShellAutoDetect covers auto-detection via $SHELL and the error
// when $SHELL is unset or empty.
func TestResolveShellAutoDetect(t *testing.T) {
	t.Run("detected from $SHELL", func(t *testing.T) {
		t.Setenv("SHELL", "/usr/bin/zsh")
		got, err := resolveShell("")
		if err != nil {
			t.Fatalf("resolveShell(\"\"): %v", err)
		}
		if got != "zsh" {
			t.Fatalf("got %q, want zsh", got)
		}
	})

	t.Run("unsupported $SHELL basename", func(t *testing.T) {
		t.Setenv("SHELL", "/usr/bin/csh")
		_, err := resolveShell("")
		if err == nil {
			t.Fatal("want error for an unsupported $SHELL")
		}
	})

	t.Run("empty $SHELL cannot detect", func(t *testing.T) {
		t.Setenv("SHELL", "")
		_, err := resolveShell("")
		if err == nil {
			t.Fatal("want error when $SHELL is unset")
		}
	})
}

// TestInitSnippetAllShells covers every shell's init snippet: non-empty and
// mentions the binary name.
func TestInitSnippetAllShells(t *testing.T) {
	for _, shell := range supportedShells {
		t.Run(shell, func(t *testing.T) {
			got := initSnippet("shortner-cli", shell)
			if got == "" {
				t.Fatalf("initSnippet(%q) returned empty output", shell)
			}
			if !strings.Contains(got, "shortner-cli") {
				t.Fatalf("initSnippet(%q) does not mention the binary name: %q", shell, got)
			}
		})
	}
}

// TestRunShellDispatch covers the completions, init, help, and
// unknown-command branches of runShell.
func TestRunShellDispatch(t *testing.T) {
	t.Run("completions", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runShell(&out, &errOut, "shortner-cli", "completions", "bash")
		if code != ExitOK {
			t.Fatalf("code = %d, want ExitOK", code)
		}
		if out.Len() == 0 {
			t.Fatal("want completion script output")
		}
	})

	t.Run("completions unresolvable shell is a usage error", func(t *testing.T) {
		t.Setenv("SHELL", "")
		var out, errOut bytes.Buffer
		code := runShell(&out, &errOut, "shortner-cli", "completions", "")
		if code != ExitUsage {
			t.Fatalf("code = %d, want ExitUsage", code)
		}
		if errOut.Len() == 0 {
			t.Fatal("want an error message")
		}
	})

	t.Run("init", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runShell(&out, &errOut, "shortner-cli", "init", "fish")
		if code != ExitOK {
			t.Fatalf("code = %d, want ExitOK", code)
		}
		if !strings.Contains(out.String(), "shortner-cli") {
			t.Fatalf("out = %q", out.String())
		}
	})

	t.Run("init invalid shell is a usage error", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runShell(&out, &errOut, "shortner-cli", "init", "csh")
		if code != ExitUsage {
			t.Fatalf("code = %d, want ExitUsage", code)
		}
	})

	t.Run("bare shell prints help", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runShell(&out, &errOut, "shortner-cli", "", "")
		if code != ExitOK {
			t.Fatalf("code = %d, want ExitOK", code)
		}
		if !strings.Contains(out.String(), "shortner-cli") {
			t.Fatalf("out = %q", out.String())
		}
	})

	t.Run("help command", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runShell(&out, &errOut, "shortner-cli", "help", "")
		if code != ExitOK {
			t.Fatalf("code = %d, want ExitOK", code)
		}
	})

	t.Run("unknown command is a usage error", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := runShell(&out, &errOut, "shortner-cli", "bogus", "")
		if code != ExitUsage {
			t.Fatalf("code = %d, want ExitUsage", code)
		}
		if errOut.Len() == 0 {
			t.Fatal("want an error message")
		}
	})
}

// TestDetectShell covers the basename extraction and the unset-env case.
func TestDetectShell(t *testing.T) {
	t.Run("basename extracted from a path", func(t *testing.T) {
		t.Setenv("SHELL", "/bin/bash")
		if got := detectShell(); got != "bash" {
			t.Fatalf("detectShell() = %q, want bash", got)
		}
	})

	t.Run("empty when unset", func(t *testing.T) {
		t.Setenv("SHELL", "")
		if got := detectShell(); got != "" {
			t.Fatalf("detectShell() = %q, want empty", got)
		}
	})
}

// TestSanitize covers hyphen-to-underscore conversion used to build valid
// shell function identifiers.
func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"shortner-cli", "shortner_cli"},
		{"plain", "plain"},
		{"a-b-c", "a_b_c"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := sanitize(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
