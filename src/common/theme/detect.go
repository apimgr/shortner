// System theme detection and mode resolution for the CLI/TUI. See AI.md
// PART 7 "Theme Package" usage example.
package theme

import (
	"os"
	"strconv"
	"strings"
)

// GetThemePalette resolves a configured theme mode ("light", "dark",
// "auto") to a concrete ThemePalette. Unknown values fall back to dark,
// matching AI.md PART 7's literal `default: return ThemePaletteDark`.
func GetThemePalette(themeMode string) ThemePalette {
	switch themeMode {
	case "light":
		return ThemePaletteLight
	case "auto":
		if IsSystemDarkTheme() {
			return ThemePaletteDark
		}
		return ThemePaletteLight
	default:
		return ThemePaletteDark
	}
}

// IsSystemDarkTheme is a best-effort check for whether the surrounding
// terminal/OS is using a dark color scheme.
//
// LIMITATION: there is no portable, dependency-free way to query the
// actual OS/terminal theme from Go (macOS needs a Cocoa/AppleScript call,
// Windows needs a registry read, Linux/BSD terminals expose nothing
// standard). Per AI.md CLAUDE.md rule "do NOT guess a made-up OS API",
// this only uses the one environment convention terminals actually
// support — the COLORFGBG variable set by rxvt, xterm, and several
// terminal emulators as "{foreground};{background}" ANSI color indices —
// and otherwise defaults to dark (this project's primary theme, per
// IDEA.md). No consumer exists yet (the CLI binary is PART 32); when one
// is built for a platform with a real API (e.g. macOS
// AppleInterfaceStyle, Windows AppsUseLightTheme registry value), this
// function should gain build-tagged platform-specific detection instead
// of this heuristic.
func IsSystemDarkTheme() bool {
	colorFgBg := os.Getenv("COLORFGBG")
	if colorFgBg == "" {
		// Undetectable: default to dark, this project's primary theme.
		return true
	}

	parts := strings.Split(colorFgBg, ";")
	bg := parts[len(parts)-1]
	bgIndex, err := strconv.Atoi(bg)
	if err != nil {
		return true
	}

	// ANSI color indices 0-6 and 8 are dark backgrounds; 7 and 9-15 are
	// light/bright backgrounds. This mirrors the convention most terminal
	// emulators follow when populating COLORFGBG.
	switch bgIndex {
	case 7, 15:
		return false
	default:
		return true
	}
}
