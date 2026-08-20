package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/apimgr/shortner/src/common/theme"
)

// TUITheme defines lipgloss colors for TUI rendering. The values match
// ThemePalette from src/common/theme (AI.md PART 16), so the terminal
// interface and the server frontend show the same palette.
type TUITheme struct {
	Name       string
	Background lipgloss.Color
	Foreground lipgloss.Color
	Primary    lipgloss.Color
	Secondary  lipgloss.Color
	Accent     lipgloss.Color
	Error      lipgloss.Color
	Success    lipgloss.Color
	Warning    lipgloss.Color
	Muted      lipgloss.Color
}

// TUIThemeDark is the default theme and matches ThemePaletteDark.
var TUIThemeDark = TUITheme{
	Name:       "dark",
	Background: lipgloss.Color("#282a36"),
	Foreground: lipgloss.Color("#f8f8f2"),
	Primary:    lipgloss.Color("#bd93f9"),
	Secondary:  lipgloss.Color("#6272a4"),
	Accent:     lipgloss.Color("#8be9fd"),
	Error:      lipgloss.Color("#ff5555"),
	Success:    lipgloss.Color("#50fa7b"),
	Warning:    lipgloss.Color("#f1fa8c"),
	Muted:      lipgloss.Color("#44475a"),
}

// TUIThemeLight matches ThemePaletteLight.
var TUIThemeLight = TUITheme{
	Name:       "light",
	Background: lipgloss.Color("#ffffff"),
	Foreground: lipgloss.Color("#282a36"),
	Primary:    lipgloss.Color("#6c5ce7"),
	Secondary:  lipgloss.Color("#636e72"),
	Accent:     lipgloss.Color("#0984e3"),
	Error:      lipgloss.Color("#d63031"),
	Success:    lipgloss.Color("#00b894"),
	Warning:    lipgloss.Color("#fdcb6e"),
	Muted:      lipgloss.Color("#dfe6e9"),
}

// CurrentTUITheme is the active theme, set from cli.yml's `tui.theme`.
var CurrentTUITheme = TUIThemeDark

// ResolveTUITheme maps a configured theme name to a palette. Anything other
// than an explicit "light" or "dark" is treated as "auto", which follows the
// terminal's own background.
func ResolveTUITheme(mode string) TUITheme {
	switch mode {
	case "light":
		return TUIThemeLight
	case "dark":
		return TUIThemeDark
	default:
		if theme.IsSystemDarkTheme() {
			return TUIThemeDark
		}
		return TUIThemeLight
	}
}

// SetTUITheme applies the configured theme for the rest of the process.
func SetTUITheme(mode string) TUITheme {
	CurrentTUITheme = ResolveTUITheme(mode)
	return CurrentTUITheme
}
