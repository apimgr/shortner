package tui

import (
	"os"
	"strings"

	"github.com/apimgr/shortner/src/common/display"
)

// Symbols is the icon set used across the TUI. AI.md PART 32 keeps a Unicode
// set and an ASCII fallback with identical fields so a view never has to
// branch on which one it holds.
type Symbols struct {
	Success string
	Error   string
	Warning string
	Info    string
	Arrow   string
	Check   string
	Cross   string
	Bullet  string
	Spinner []string
}

// TUISymbols are Unicode symbols that work across modern terminals.
var TUISymbols = Symbols{
	Success: "✓",
	Error:   "✗",
	Warning: "⚠",
	Info:    "ℹ",
	Arrow:   "→",
	Check:   "☑",
	Cross:   "☒",
	Bullet:  "•",
	Spinner: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
}

// TUISymbolsASCII is the fallback for terminals without Unicode support.
var TUISymbolsASCII = Symbols{
	Success: "[OK]",
	Error:   "[ERR]",
	Warning: "[WARN]",
	Info:    "[INFO]",
	Arrow:   "->",
	Check:   "[x]",
	Cross:   "[ ]",
	Bullet:  "*",
	Spinner: []string{"|", "/", "-", "\\"},
}

// GetTUISymbols picks the symbol set for the detected display environment.
func GetTUISymbols(env display.DisplayEnv) Symbols {
	if supportsUnicode(env) {
		return TUISymbols
	}
	return TUISymbolsASCII
}

// supportsUnicode reports whether the terminal can render the Unicode symbol
// set. A dumb or unset TERM, or a non-UTF-8 locale, forces the ASCII set.
func supportsUnicode(env display.DisplayEnv) bool {
	if env.IsDumbTerminal() {
		return false
	}
	if env.TerminalType == "" {
		return false
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.ToUpper(os.Getenv(name))
		if value == "" {
			continue
		}
		return strings.Contains(value, "UTF-8") || strings.Contains(value, "UTF8")
	}
	return false
}
