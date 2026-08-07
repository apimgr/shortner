// Package display detects the runtime display environment (GUI/TUI/CLI/
// headless) shared by the server and CLI binaries. See AI.md PART 7
// "Display Environment Detection".
package display

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// DisplayMode is the UI display mode (NOT the application mode from
// src/mode).
type DisplayMode int

const (
	// DisplayModeHeadless means no display, no TTY.
	DisplayModeHeadless DisplayMode = iota
	// DisplayModeCLI means command-line only (piped or command provided).
	DisplayModeCLI
	// DisplayModeTUI means terminal UI (interactive terminal).
	DisplayModeTUI
	// DisplayModeGUI means native graphical UI.
	DisplayModeGUI
)

// DisplayEnv is the detected display environment.
type DisplayEnv struct {
	Mode DisplayMode
	// HasDisplay reports X11, Wayland, Windows, or macOS display access.
	HasDisplay bool
	// DisplayType is "x11", "wayland", "windows", "windows-rdp", "macos", or "none".
	DisplayType string
	// IsTerminal reports whether stdout is a TTY.
	IsTerminal bool
	// IsSSH reports whether running over SSH.
	IsSSH bool
	// IsMosh reports whether running over mosh.
	IsMosh bool
	// IsScreen reports whether running in screen/tmux.
	IsScreen bool
	// TerminalType is the TERM value.
	TerminalType string
	// Cols is the terminal column count (0 if no terminal).
	Cols int
	// Rows is the terminal row count (0 if no terminal).
	Rows int
}

// DetectDisplayEnv auto-detects the display environment.
func DetectDisplayEnv() DisplayEnv {
	env := DisplayEnv{}

	// Terminal detection.
	env.IsTerminal = term.IsTerminal(int(os.Stdout.Fd()))
	if env.IsTerminal {
		env.Cols, env.Rows, _ = term.GetSize(int(os.Stdout.Fd()))
	}
	env.TerminalType = os.Getenv("TERM")

	// Remote session detection.
	env.IsSSH = os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != ""
	env.IsMosh = os.Getenv("MOSH") != "" || strings.Contains(os.Getenv("TERM"), "mosh")
	env.IsScreen = os.Getenv("STY") != "" || os.Getenv("TMUX") != ""

	// Platform-specific display detection.
	env.detectPlatformDisplay()

	// Auto-detect display mode.
	env.Mode = env.autoDetectDisplayMode()

	return env
}

// autoDetectDisplayMode determines the display mode from the environment.
func (e *DisplayEnv) autoDetectDisplayMode() DisplayMode {
	if !e.IsTerminal && !e.HasDisplay {
		return DisplayModeHeadless
	}
	// TERM=dumb: force CLI mode (no TUI, no ANSI escapes).
	if e.TerminalType == "dumb" {
		return DisplayModeCLI
	}
	if e.HasDisplay && !e.IsSSH && !e.IsMosh {
		return DisplayModeGUI
	}
	if e.IsTerminal {
		return DisplayModeTUI
	}
	return DisplayModeCLI
}

// IsDumbTerminal reports whether running in a dumb terminal (no ANSI support).
func (e *DisplayEnv) IsDumbTerminal() bool {
	return e.TerminalType == "dumb"
}

// IsAutoDetectDisplayModeGUI reports whether the detected mode is GUI.
func (e DisplayEnv) IsAutoDetectDisplayModeGUI() bool { return e.Mode == DisplayModeGUI }

// IsAutoDetectDisplayModeTUI reports whether the detected mode is TUI.
func (e DisplayEnv) IsAutoDetectDisplayModeTUI() bool { return e.Mode == DisplayModeTUI }

// IsAutoDetectDisplayModeCLI reports whether the detected mode is CLI.
func (e DisplayEnv) IsAutoDetectDisplayModeCLI() bool { return e.Mode == DisplayModeCLI }

// IsAutoDetectDisplayModeHeadless reports whether the detected mode is headless.
func (e DisplayEnv) IsAutoDetectDisplayModeHeadless() bool { return e.Mode == DisplayModeHeadless }

// CanUseANSI reports whether ANSI escapes (colors, cursor control, clearing)
// may be used. NO_COLOR users typically want plain output, so it is
// respected here too, alongside TERM=dumb. See AI.md PART 7 "TERM=dumb
// Handling".
func CanUseANSI(env *DisplayEnv) bool {
	if env.IsDumbTerminal() {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		// Plain output requested.
		return false
	}
	return env.IsTerminal
}
