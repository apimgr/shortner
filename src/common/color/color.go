// Package color implements the NO_COLOR standard and the --color flag
// shared by every binary (server, client). See AI.md PART 8 "NO_COLOR
// Support (ALL Binaries)". It builds on src/common/display's terminal/
// TERM=dumb detection instead of duplicating it.
package color

import (
	"fmt"
	"os"
	"strings"

	"github.com/apimgr/shortner/src/common/display"
)

// ParseFlag parses a --color {auto|yes|no} value into a *bool suitable for
// Enabled: nil means "auto-detect", non-nil forces color on/off. An empty
// string (flag not given) is treated as "auto".
func ParseFlag(value string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return nil, nil
	case "yes":
		v := true
		return &v, nil
	case "no":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("invalid --color value %q (want auto, yes, or no)", value)
	}
}

// Enabled reports whether ANSI color output should be used. Priority
// (highest to lowest): forceColor (the parsed --color flag), NO_COLOR,
// then TTY/TERM auto-detection via display.CanUseANSI. See AI.md PART 8
// "NO_COLOR Support" priority table (config-file override is not yet
// wired — server.yml has no output.color setting until PART 12).
func Enabled(forceColor *bool) bool {
	if forceColor != nil {
		return *forceColor
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	env := display.DetectDisplayEnv()
	return display.CanUseANSI(&env)
}

// EmojiEnabled reports whether emoji output should be used. NO_COLOR and
// TERM=dumb both disable emoji, matching the "plain output" intent behind
// both settings.
func EmojiEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return true
}
