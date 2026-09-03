package setup

import (
	"context"
	"strings"
	"testing"
)

// clearDisplayEnv resets every environment variable display.DetectDisplayEnv
// consults, so each subtest starts from a known-clean slate before setting
// only the variables it cares about.
func clearDisplayEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"SSH_CLIENT", "SSH_TTY", "MOSH", "STY", "TMUX",
		"WAYLAND_DISPLAY", "DISPLAY", "TERM", "__CFBundleIdentifier",
	} {
		t.Setenv(name, "")
	}
}

// TestSelectSetupModeSSH covers that a remote session always gets the TUI,
// even when a display is also present, per AI.md PART 32's stated priority
// order (SSH/mosh checked before HasDisplay).
func TestSelectSetupModeSSH(t *testing.T) {
	t.Run("SSH_CLIENT set", func(t *testing.T) {
		clearDisplayEnv(t)
		t.Setenv("SSH_CLIENT", "10.0.0.1 1 22")
		if got := SelectSetupMode(); got != ModeTUI {
			t.Fatalf("got %v, want ModeTUI", got)
		}
	})

	t.Run("SSH_TTY set", func(t *testing.T) {
		clearDisplayEnv(t)
		t.Setenv("SSH_TTY", "/dev/pts/0")
		if got := SelectSetupMode(); got != ModeTUI {
			t.Fatalf("got %v, want ModeTUI", got)
		}
	})

	t.Run("SSH plus a display still gets TUI", func(t *testing.T) {
		clearDisplayEnv(t)
		t.Setenv("SSH_CLIENT", "10.0.0.1 1 22")
		t.Setenv("DISPLAY", ":0")
		if got := SelectSetupMode(); got != ModeTUI {
			t.Fatalf("got %v, want ModeTUI (SSH takes priority over a display)", got)
		}
	})

	t.Run("MOSH set", func(t *testing.T) {
		clearDisplayEnv(t)
		t.Setenv("MOSH", "1")
		if got := SelectSetupMode(); got != ModeTUI {
			t.Fatalf("got %v, want ModeTUI", got)
		}
	})
}

// TestSelectSetupModeGUI covers that a local display (no SSH/mosh) selects
// the GUI mode.
func TestSelectSetupModeGUI(t *testing.T) {
	t.Run("Wayland display", func(t *testing.T) {
		clearDisplayEnv(t)
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		if got := SelectSetupMode(); got != ModeGUI {
			t.Fatalf("got %v, want ModeGUI", got)
		}
	})

	t.Run("X11 display", func(t *testing.T) {
		clearDisplayEnv(t)
		t.Setenv("DISPLAY", ":0")
		if got := SelectSetupMode(); got != ModeGUI {
			t.Fatalf("got %v, want ModeGUI", got)
		}
	})
}

// TestSelectSetupModeError covers the no-display, no-SSH, no-terminal case:
// this test process's stdout is not a TTY under the test runner, so with
// every display/remote signal cleared the wizard has nothing to run in.
func TestSelectSetupModeError(t *testing.T) {
	clearDisplayEnv(t)
	got := SelectSetupMode()
	if got != ModeError && got != ModeTUI {
		t.Fatalf("got %v, want ModeError (no display/SSH) or ModeTUI (if stdout is unexpectedly a TTY)", got)
	}
}

// TestRunModeErrorReturnsCancellationWithoutLaunchingTheWizard covers Run's
// ModeError branch: a plain error return with no bubbletea program started,
// so it is safe to call in a non-interactive test environment.
func TestRunModeErrorReturnsCancellationWithoutLaunchingTheWizard(t *testing.T) {
	clearDisplayEnv(t)
	if SelectSetupMode() != ModeError {
		t.Skip("this test environment has a TTY or display attached; ModeError is not reachable here")
	}

	result, err := Run(context.Background(), Options{BinaryName: "shortner-cli"})
	if err == nil {
		t.Fatal("want an error when no terminal or display is available")
	}
	if result != (Result{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if !strings.Contains(err.Error(), "cannot run setup") {
		t.Fatalf("err = %v, want a cannot-run-setup message", err)
	}
}

// TestModeConstants covers the Mode enum's declared ordering, since Result
// and Mode zero values are meaningful (ModeError is the zero value, by
// design, so an unset Mode never accidentally launches a UI).
func TestModeConstants(t *testing.T) {
	if ModeError != 0 {
		t.Fatalf("ModeError = %d, want 0 (the zero value, so unset never launches a UI)", ModeError)
	}
	if ModeTUI == ModeGUI {
		t.Fatal("ModeTUI and ModeGUI must be distinct")
	}
}
