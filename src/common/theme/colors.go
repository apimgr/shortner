// Package theme defines the single source of truth for this project's
// color palette, per AI.md PART 16 "Themes (NON-NEGOTIABLE - PROJECT-WIDE)"
// and IDEA.md "Frontend design reference" (Dracula scheme). The web
// frontend's CSS custom properties (src/server/static/css/common.css) use
// the same hex values as ThemePaletteDark/ThemePaletteLight; a future
// CLI/TUI can reuse TerminalPaletteDark/TerminalPaletteLight for the same
// semantic roles without duplicating the color decision.
package theme

// ThemePalette maps semantic UI roles to hex colors for the web frontend.
type ThemePalette struct {
	Background string
	Foreground string
	Primary    string
	Secondary  string
	Accent     string
	Success    string
	Warning    string
	Error      string
	Info       string
	Surface    string
	SurfaceAlt string
	Border     string
	Muted      string
}

// ThemePaletteDark is the Dracula palette, per IDEA.md "Frontend design
// reference" and AI.md PART 16's CSS Variable Reference (the spec's
// generic --color-* example values are themselves Dracula hex codes).
var ThemePaletteDark = ThemePalette{
	Background: "#282a36",
	Foreground: "#f8f8f2",
	Primary:    "#bd93f9",
	Secondary:  "#6272a4",
	Accent:     "#ff79c6",
	Success:    "#50fa7b",
	Warning:    "#ffb86c",
	Error:      "#ff5555",
	Info:       "#8be9fd",
	Surface:    "#2b2d3a",
	SurfaceAlt: "#21222c",
	Border:     "#44475a",
	Muted:      "#6272a4",
}

// ThemePaletteLight is a GitHub-Light-based complement to the Dracula dark
// theme — IDEA.md only mandates Dracula for the primary/dark scheme, so the
// light variant follows AI.md PART 16's generic light-theme example.
var ThemePaletteLight = ThemePalette{
	Background: "#ffffff",
	Foreground: "#1f2328",
	Primary:    "#0969da",
	Secondary:  "#59636e",
	Accent:     "#8250df",
	Success:    "#1a7f37",
	Warning:    "#9a6700",
	Error:      "#d1242f",
	Info:       "#0969da",
	Surface:    "#ffffff",
	SurfaceAlt: "#f6f8fa",
	Border:     "#d1d9e0",
	Muted:      "#59636e",
}

// TerminalPalette maps the same semantic roles to ANSI 16-color indices
// ("0"-"15") for CLI/TUI reuse, per src/common/color's existing
// ColorEnabled(nil) gate (AI.md PART 8) — this package makes no separate
// NO_COLOR decision itself.
type TerminalPalette struct {
	Background string
	Foreground string
	Primary    string
	Secondary  string
	Accent     string
	Success    string
	Warning    string
	Error      string
	Info       string
	Surface    string
	SurfaceAlt string
	Border     string
	Muted      string
}

// TerminalPaletteDark maps the Dracula roles to their closest ANSI 16-color
// indices for dark-background terminals.
var TerminalPaletteDark = TerminalPalette{
	Background: "0",
	Foreground: "15",
	Primary:    "13",
	Secondary:  "8",
	Accent:     "5",
	Success:    "10",
	Warning:    "11",
	Error:      "9",
	Info:       "14",
	Surface:    "0",
	SurfaceAlt: "0",
	Border:     "8",
	Muted:      "8",
}

// TerminalPaletteLight maps the light-theme roles to their closest ANSI
// 16-color indices for light-background terminals.
var TerminalPaletteLight = TerminalPalette{
	Background: "15",
	Foreground: "0",
	Primary:    "4",
	Secondary:  "8",
	Accent:     "5",
	Success:    "2",
	Warning:    "3",
	Error:      "1",
	Info:       "4",
	Surface:    "15",
	SurfaceAlt: "7",
	Border:     "7",
	Muted:      "8",
}
