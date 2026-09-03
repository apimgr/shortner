// Spinner and progress helpers that fall back to plain text on TERM=dumb
// terminals. See AI.md PART 7 "TERM=dumb Handling" -> "Implementation".
package display

import (
	"fmt"
	"strings"
)

// Spinner is a progress indicator that can be started and stopped. It has
// two implementations: TextSpinner (dumb terminals) and ANSISpinner
// (normal terminals).
type Spinner interface {
	// Start prints the initial spinner state.
	Start()
	// Stop clears the spinner and prints a final message.
	Stop(final string)
}

// TextSpinner is the TERM=dumb fallback: it prints the message once with
// no animation and no cursor control, per AI.md PART 7's "No spinners -
// Use `Processing...` / `Done.` instead" rule.
type TextSpinner struct {
	message string
}

// Start prints "{message}..." with no ANSI escapes.
func (s *TextSpinner) Start() {
	fmt.Printf("%s...\n", s.message)
}

// Stop prints the final message on its own line.
func (s *TextSpinner) Stop(final string) {
	if final == "" {
		final = "Done."
	}
	fmt.Println(final)
}

// ANSISpinner is the normal-terminal spinner. It has no consumer yet (the
// CLI binary is PART 32), so it stays intentionally minimal: a single
// static frame plus cursor-hide/show, not a full animation loop — a
// future TUI consumer can drive Start()/Stop() from its own ticker.
type ANSISpinner struct {
	message string
}

// Start prints the message with a spinner glyph and hides the cursor.
func (s *ANSISpinner) Start() {
	fmt.Printf("\x1b[?25l\r%c %s", spinnerFrames[0], s.message)
}

// Stop clears the spinner line, shows the cursor, and prints the final
// message.
func (s *ANSISpinner) Stop(final string) {
	if final == "" {
		final = "Done."
	}
	fmt.Printf("\r\x1b[K%s\x1b[?25h\n", final)
}

// spinnerFrames are the Braille-dot animation frames an interactive
// consumer can cycle through; ANSISpinner.Start prints only the first
// frame since no consumer yet drives an animation loop.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// NewSpinner returns a Spinner appropriate for env: a TextSpinner on
// TERM=dumb terminals or when NO_COLOR is set, an ANSISpinner otherwise.
// See AI.md PART 7 "Implementation"; gated on CanUseANSI rather than
// IsDumbTerminal alone so NO_COLOR is also respected, per this package's
// existing convention.
func NewSpinner(env *DisplayEnv, message string) Spinner {
	if !CanUseANSI(env) {
		return &TextSpinner{message: message}
	}
	return &ANSISpinner{message: message}
}

// ShowProgress prints percent-complete progress, falling back to plain
// "N% complete" text on TERM=dumb terminals or when NO_COLOR is set, and
// an ANSI progress bar with cursor control otherwise. See AI.md PART 7
// "Implementation"; gated on CanUseANSI rather than IsDumbTerminal alone
// so NO_COLOR is also respected, per this package's existing convention.
func ShowProgress(env *DisplayEnv, percent int) {
	if !CanUseANSI(env) {
		fmt.Printf("%d%% complete\n", percent)
		return
	}
	fmt.Printf("\r[%-50s] %d%%", strings.Repeat("=", percent/2), percent)
}
