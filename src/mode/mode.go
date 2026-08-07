// Package mode resolves the application's operational mode (production or
// development) and debug flag. See AI.md PART 6 for the authoritative rules.
package mode

import (
	"os"
	"runtime"
	"strings"

	"github.com/apimgr/shortner/src/common/color"
	"github.com/apimgr/shortner/src/config"
)

var (
	currentMode  = Production
	debugEnabled = false
)

// AppMode is the application's operational mode.
type AppMode int

const (
	// Production is the default, hardened runtime mode.
	Production AppMode = iota
	// Development relaxes caching and logging for local work.
	Development
)

// String returns the lowercase mode name.
func (m AppMode) String() string {
	switch m {
	case Development:
		return "development"
	default:
		return "production"
	}
}

// SetAppMode sets the application mode from a raw --mode/MODE value.
// "debug" is an alias for development mode + debug on; an explicit
// --debug flag or DEBUG env var applied after this still wins.
func SetAppMode(m string) {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "dev", "devel", "development":
		currentMode = Development
	case "debug":
		currentMode = Development
		SetDebugEnabled(true)
	default:
		currentMode = Production
	}
	updateAppModeProfilingSettings()
}

// SetDebugEnabled enables or disables debug mode.
func SetDebugEnabled(enabled bool) {
	debugEnabled = enabled
	updateAppModeProfilingSettings()
}

// updateAppModeProfilingSettings enables/disables runtime profiling based on
// the debug flag. Debug mode affects verbosity and diagnostics ONLY — it
// never disables authentication or security checks.
func updateAppModeProfilingSettings() {
	if debugEnabled {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
	} else {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(0)
	}
}

// GetCurrentAppMode returns the current application mode.
func GetCurrentAppMode() AppMode {
	return currentMode
}

// IsAppModeDev returns true if in development mode.
func IsAppModeDev() bool {
	return currentMode == Development
}

// IsAppModeProd returns true if in production mode.
func IsAppModeProd() bool {
	return currentMode == Production
}

// IsDebugEnabled returns true if debug mode is enabled (--debug or DEBUG=true).
func IsDebugEnabled() bool {
	return debugEnabled
}

// GetAppModeString returns the mode string with a debug suffix if enabled.
func GetAppModeString() string {
	s := currentMode.String()
	if debugEnabled {
		s += " [debugging]"
	}
	return s
}

// FromEnv sets mode and debug from environment variables.
// MODE=debug is an alias for development mode + debug on, but an
// explicitly set DEBUG env var (truthy OR falsy) always wins over the
// alias — MODE=debug DEBUG=false runs development mode with debug off.
// The --debug CLI flag (applied after this) wins over both.
func FromEnv() {
	if m := os.Getenv("MODE"); m != "" {
		SetAppMode(m)
	}
	// LookupEnv distinguishes "explicitly set" from "unset": an unset
	// DEBUG leaves the alias result alone; a set DEBUG overrides it.
	if v, set := os.LookupEnv("DEBUG"); set {
		SetDebugEnabled(config.IsTruthy(v))
	}
}

// Banner returns the console startup line for the current state. The
// leading emoji is omitted when NO_COLOR or TERM=dumb is set, per AI.md
// PART 8 "NO_COLOR Support".
func Banner() string {
	if !color.EmojiEnabled() {
		return "Running in mode: " + GetAppModeString()
	}
	icon := "🔒"
	if currentMode == Development {
		icon = "🔧"
	}
	return icon + " Running in mode: " + GetAppModeString()
}
