// Package mode resolves the application's operational mode (production or
// development) and debug flag. See AI.md PART 6 for the authoritative rules.
package mode

import (
	"os"
	"strconv"
	"strings"
)

// Mode is the application's operational mode.
type Mode string

const (
	// Production is the default, hardened runtime mode.
	Production Mode = "production"
	// Development relaxes caching and logging for local work.
	Development Mode = "development"
)

// State is the resolved mode + debug flag pair for a single process run.
type State struct {
	Mode  Mode
	Debug bool
}

// Resolve determines the operational mode and debug flag from, in priority
// order, CLI flags, then environment variables, then defaults.
// modeFlag/debugFlag are the raw `--mode`/`--debug` flag values; pass ""
// for modeFlag and nil for debugFlag when the flag was not set on the CLI.
func Resolve(modeFlag string, debugFlag *bool) State {
	rawMode := modeFlag
	if rawMode == "" {
		rawMode = os.Getenv("MODE")
	}

	resolved := Production
	debug := false

	switch strings.ToLower(strings.TrimSpace(rawMode)) {
	case "dev", "devel", "development":
		resolved = Development
	case "debug":
		resolved = Development
		debug = true
	case "prod", "production", "":
		resolved = Production
	default:
		resolved = Production
	}

	if debugFlag != nil {
		debug = *debugFlag
	} else if v, ok := os.LookupEnv("DEBUG"); ok {
		if parsed, err := strconv.ParseBool(v); err == nil {
			debug = parsed
		}
	}

	return State{Mode: resolved, Debug: debug}
}

// Banner returns the console startup line for the resolved state.
func (s State) Banner() string {
	icon := "🔒"
	if s.Mode == Development {
		icon = "🔧"
	}
	if s.Debug {
		return icon + " Running in mode: " + string(s.Mode) + " [debugging]"
	}
	return icon + " Running in mode: " + string(s.Mode)
}
