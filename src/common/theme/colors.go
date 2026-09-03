// Package theme defines the single source of truth for this project's
// color palette, per AI.md PART 16 "Themes (NON-NEGOTIABLE - PROJECT-WIDE)"
// and IDEA.md "Frontend design reference" (Dracula scheme). The web
// frontend's CSS custom properties (src/server/static/css/common.css) use
// the same hex values as ThemePaletteDark/ThemePaletteLight; a future
// CLI/TUI can reuse TerminalPaletteDark/TerminalPaletteLight for the same
// semantic roles without duplicating the color decision.
//
// Field values and struct shapes here MUST match AI.md PART 16's
// "Unified Color Palette" and "CLI/TUI Color Mapping" sections literally
// — the spec's example hex/ANSI values are this project's actual values,
// not placeholders to be substituted.
package theme

// ThemePalette maps semantic UI roles to hex colors for the web frontend,
// Swagger, and GraphiQL. Field names match AI.md PART 16's Go struct.
type ThemePalette struct {
	Background string `json:"background"`
	Foreground string `json:"foreground"`
	Primary    string `json:"primary"`
	Secondary  string `json:"secondary"`
	Accent     string `json:"accent"`
	Success    string `json:"success"`
	Warning    string `json:"warning"`
	Error      string `json:"error"`
	Info       string `json:"info"`
	Surface    string `json:"surface"`
	SurfaceAlt string `json:"surface_alt"`
	Border     string `json:"border"`
	Muted      string `json:"muted"`
}

// ThemePaletteDark is the Dracula palette, per IDEA.md "Frontend design
// reference" and AI.md PART 16's "Unified Color Palette" (the spec's
// literal example hex values are themselves Dracula hex codes).
var ThemePaletteDark = ThemePalette{
	Background: "#282a36", Foreground: "#f8f8f2",
	Primary: "#bd93f9", Secondary: "#50fa7b", Accent: "#ff79c6",
	Success: "#50fa7b", Warning: "#ffb86c", Error: "#ff5555", Info: "#8be9fd",
	Surface: "#2b2d3a", SurfaceAlt: "#21222c", Border: "#44475a", Muted: "#6272a4",
}

// ThemePaletteLight is AI.md PART 16's literal light-theme example
// (GitHub-Light-based) — IDEA.md only mandates Dracula for the dark theme.
var ThemePaletteLight = ThemePalette{
	Background: "#ffffff", Foreground: "#1f2328",
	Primary: "#0969da", Secondary: "#1a7f37", Accent: "#8250df",
	Success: "#1a7f37", Warning: "#9a6700", Error: "#d1242f", Info: "#0969da",
	Surface: "#f6f8fa", SurfaceAlt: "#eff2f5", Border: "#d1d9e0", Muted: "#59636e",
}

// TerminalPalette holds ANSI 16-color indices (0-15) for CLI/TUI — never
// the literal hex ThemePalette. lipgloss.Color() and the ESC[38;5;{n}m
// escape both accept these indices directly. Field set matches AI.md
// PART 16's "CLI/TUI Color Mapping" struct exactly (no extra roles).
type TerminalPalette struct {
	Foreground string `json:"foreground"`
	Muted      string `json:"muted"`
	Primary    string `json:"primary"`
	Success    string `json:"success"`
	Warning    string `json:"warning"`
	Error      string `json:"error"`
	Info       string `json:"info"`
	Border     string `json:"border"`
}

// TerminalPaletteDark maps ThemePaletteDark's roles to the nearest ANSI
// 16-color indices, per AI.md PART 16's Role -> Dark ANSI table.
var TerminalPaletteDark = TerminalPalette{
	Foreground: "15", Muted: "7", Primary: "13",
	Success: "10", Warning: "11", Error: "9", Info: "12", Border: "13",
}

// TerminalPaletteLight maps ThemePaletteLight's roles to the nearest ANSI
// 16-color indices, per AI.md PART 16's Role -> Light ANSI table.
var TerminalPaletteLight = TerminalPalette{
	Foreground: "0", Muted: "8", Primary: "4",
	Success: "2", Warning: "3", Error: "1", Info: "4", Border: "4",
}
