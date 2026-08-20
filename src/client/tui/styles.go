package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/apimgr/shortner/src/common/theme"
)

// TUIStyles holds lipgloss styles derived from a TerminalPalette. AI.md
// PART 32 requires the ANSI-mapped TerminalPalette here, never the literal
// hex ThemePalette.
type TUIStyles struct {
	Base     lipgloss.Style
	Title    lipgloss.Style
	Selected lipgloss.Style
	Error    lipgloss.Style
	Success  lipgloss.Style
	Warning  lipgloss.Style
	Muted    lipgloss.Style
	Border   lipgloss.Style
}

// StylesFromTerminalPalette builds the style set for a palette.
func StylesFromTerminalPalette(p theme.TerminalPalette) TUIStyles {
	return TUIStyles{
		Base:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.Foreground)),
		Title:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.Primary)).Bold(true),
		Selected: lipgloss.NewStyle().Reverse(true),
		Error:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.Error)),
		Success:  lipgloss.NewStyle().Foreground(lipgloss.Color(p.Success)),
		Warning:  lipgloss.NewStyle().Foreground(lipgloss.Color(p.Warning)),
		Muted:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.Muted)),
		Border:   lipgloss.NewStyle().BorderForeground(lipgloss.Color(p.Border)),
	}
}

// stylesForTheme resolves a cli.yml theme mode ("dark", "light", "auto") to
// the matching terminal palette and its styles.
func stylesForTheme(mode string) TUIStyles {
	switch mode {
	case "light":
		return StylesFromTerminalPalette(theme.TerminalPaletteLight)
	case "dark":
		return StylesFromTerminalPalette(theme.TerminalPaletteDark)
	default:
		if theme.IsSystemDarkTheme() {
			return StylesFromTerminalPalette(theme.TerminalPaletteDark)
		}
		return StylesFromTerminalPalette(theme.TerminalPaletteLight)
	}
}
