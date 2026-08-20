package tui

import "github.com/apimgr/shortner/src/common/terminal"

// LayoutConfig provides TUI-specific layout settings for a SizeMode, per
// AI.md PART 32 "TUI Responsive Layout".
type LayoutConfig struct {
	ShowBorders    bool
	ShowHeader     bool
	ShowFooter     bool
	ShowSidebar    bool
	SidebarWidth   int
	MaxColumns     int
	TruncateAt     int
	UseAbbrev      bool
	VerticalScroll bool
	MultiPane      bool
	TileLayout     bool
}

// layoutConfigs is the SizeMode -> LayoutConfig table from AI.md PART 32.
var layoutConfigs = map[terminal.SizeMode]LayoutConfig{
	terminal.SizeModeMicro: {
		ShowBorders:    false,
		ShowHeader:     false,
		ShowFooter:     false,
		ShowSidebar:    false,
		MaxColumns:     2,
		TruncateAt:     30,
		UseAbbrev:      true,
		VerticalScroll: true,
	},
	terminal.SizeModeMinimal: {
		ShowBorders:    false,
		ShowHeader:     true,
		ShowFooter:     true,
		ShowSidebar:    false,
		MaxColumns:     3,
		TruncateAt:     40,
		UseAbbrev:      true,
		VerticalScroll: true,
	},
	terminal.SizeModeCompact: {
		ShowBorders:    true,
		ShowHeader:     true,
		ShowFooter:     true,
		ShowSidebar:    false,
		MaxColumns:     4,
		TruncateAt:     60,
		UseAbbrev:      false,
		VerticalScroll: true,
	},
	terminal.SizeModeStandard: {
		ShowBorders:    true,
		ShowHeader:     true,
		ShowFooter:     true,
		ShowSidebar:    false,
		MaxColumns:     6,
		TruncateAt:     80,
		UseAbbrev:      false,
		VerticalScroll: true,
	},
	terminal.SizeModeWide: {
		ShowBorders:    true,
		ShowHeader:     true,
		ShowFooter:     true,
		ShowSidebar:    true,
		SidebarWidth:   30,
		MaxColumns:     8,
		TruncateAt:     120,
		UseAbbrev:      false,
		VerticalScroll: true,
	},
	terminal.SizeModeUltrawide: {
		ShowBorders:  true,
		ShowHeader:   true,
		ShowFooter:   true,
		ShowSidebar:  true,
		SidebarWidth: 40,
		MaxColumns:   12,
		TruncateAt:   200,
		UseAbbrev:    false,
		// Full content is visible at this width, so nothing scrolls.
		VerticalScroll: false,
		MultiPane:      true,
	},
	terminal.SizeModeMassive: {
		ShowBorders:  true,
		ShowHeader:   true,
		ShowFooter:   true,
		ShowSidebar:  true,
		SidebarWidth: 50,
		MaxColumns:   20,
		// Zero means never truncate.
		TruncateAt:     0,
		UseAbbrev:      false,
		VerticalScroll: false,
		MultiPane:      true,
		TileLayout:     true,
	},
}

// GetLayoutConfig returns the layout config for a SizeMode.
func GetLayoutConfig(mode terminal.SizeMode) LayoutConfig {
	return layoutConfigs[mode]
}

// Consistent spacing units, from AI.md PART 32 "Spacing and Alignment".
const (
	// SpaceXS is micro spacing.
	SpaceXS = 1
	// SpaceS is small spacing.
	SpaceS = 2
	// SpaceM is medium spacing.
	SpaceM = 4
	// SpaceL is large spacing.
	SpaceL = 6
	// SpaceXL is extra large spacing.
	SpaceXL = 8
)

// GetSpacingForMode returns the spacing unit for a terminal size mode.
func GetSpacingForMode(mode terminal.SizeMode) int {
	switch mode {
	case terminal.SizeModeMicro, terminal.SizeModeMinimal:
		return SpaceXS
	case terminal.SizeModeCompact:
		return SpaceS
	case terminal.SizeModeStandard:
		return SpaceM
	case terminal.SizeModeWide:
		return SpaceL
	default:
		return SpaceXL
	}
}

// modeForSize maps a live width/height from a resize event to a SizeMode,
// using the same breakpoints as terminal.GetTerminalSize.
func modeForSize(cols, rows int) terminal.SizeMode {
	switch {
	case cols < 40 || rows < 10:
		return terminal.SizeModeMicro
	case cols < 60 || rows < 16:
		return terminal.SizeModeMinimal
	case cols < 80 || rows < 24:
		return terminal.SizeModeCompact
	case cols < 120 || rows < 40:
		return terminal.SizeModeStandard
	case cols < 200 || rows < 60:
		return terminal.SizeModeWide
	case cols < 400 || rows < 80:
		return terminal.SizeModeUltrawide
	default:
		return terminal.SizeModeMassive
	}
}
