// Package terminal provides terminal size detection and responsive
// breakpoints shared by the server and CLI binaries. See AI.md PART 7
// "Terminal Package".
package terminal

import (
	"os"

	"golang.org/x/term"
)

// SizeMode is a responsive breakpoint derived from the terminal's
// dimensions.
type SizeMode int

const (
	// SizeModeMicro is <40 cols or <10 rows.
	SizeModeMicro SizeMode = iota
	// SizeModeMinimal is 40-59 cols or 10-15 rows.
	SizeModeMinimal
	// SizeModeCompact is 60-79 cols or 16-23 rows.
	SizeModeCompact
	// SizeModeStandard is 80-119 cols and 24-39 rows.
	SizeModeStandard
	// SizeModeWide is 120-199 cols and 40-59 rows.
	SizeModeWide
	// SizeModeUltrawide is 200-399 cols and 60-79 rows.
	SizeModeUltrawide
	// SizeModeMassive is 400+ cols and 80+ rows.
	SizeModeMassive
)

// TerminalSize is the detected terminal dimensions and breakpoint.
type TerminalSize struct {
	Cols int
	Rows int
	Mode SizeMode
}

// GetTerminalSize returns the current terminal size and breakpoint,
// falling back to 80x24 when the size cannot be determined (no TTY).
func GetTerminalSize() TerminalSize {
	cols, rows, _ := term.GetSize(int(os.Stdout.Fd()))
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	return TerminalSize{
		Cols: cols,
		Rows: rows,
		Mode: calculateMode(cols, rows),
	}
}

// calculateMode maps columns/rows to a SizeMode breakpoint.
func calculateMode(cols, rows int) SizeMode {
	switch {
	case cols < 40 || rows < 10:
		return SizeModeMicro
	case cols < 60 || rows < 16:
		return SizeModeMinimal
	case cols < 80 || rows < 24:
		return SizeModeCompact
	case cols < 120 || rows < 40:
		return SizeModeStandard
	case cols < 200 || rows < 60:
		return SizeModeWide
	case cols < 400 || rows < 80:
		return SizeModeUltrawide
	default:
		return SizeModeMassive
	}
}

// ShowASCIIArt reports whether the current size mode is large enough for
// ASCII art banners.
func (s SizeMode) ShowASCIIArt() bool { return s >= SizeModeStandard }

// ShowBorders reports whether the current size mode is large enough for
// box-drawing borders.
func (s SizeMode) ShowBorders() bool { return s >= SizeModeCompact }

// ShowSidebar reports whether the current size mode is large enough for a
// sidebar layout.
func (s SizeMode) ShowSidebar() bool { return s >= SizeModeWide }

// ShowIcons reports whether the current size mode is large enough for
// icons/symbols.
func (s SizeMode) ShowIcons() bool { return s >= SizeModeMinimal }
