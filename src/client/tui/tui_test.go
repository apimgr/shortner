package tui

import (
	"testing"

	"github.com/apimgr/shortner/src/common/display"
	"github.com/apimgr/shortner/src/common/terminal"
	"github.com/apimgr/shortner/src/common/theme"
)

// allSizeModes lists every terminal.SizeMode, smallest to largest.
var allSizeModes = []terminal.SizeMode{
	terminal.SizeModeMicro,
	terminal.SizeModeMinimal,
	terminal.SizeModeCompact,
	terminal.SizeModeStandard,
	terminal.SizeModeWide,
	terminal.SizeModeUltrawide,
	terminal.SizeModeMassive,
}

// TestGetLayoutConfigAllModes covers every SizeMode and spot-checks the
// AI.md PART 32 progression: sidebars only appear at Wide+, and Massive
// never truncates.
func TestGetLayoutConfigAllModes(t *testing.T) {
	for _, mode := range allSizeModes {
		cfg := GetLayoutConfig(mode)
		if cfg.MaxColumns <= 0 {
			t.Errorf("mode %v: MaxColumns = %d, want > 0", mode, cfg.MaxColumns)
		}
	}

	if GetLayoutConfig(terminal.SizeModeMicro).ShowSidebar {
		t.Error("Micro should never show a sidebar")
	}
	if !GetLayoutConfig(terminal.SizeModeWide).ShowSidebar {
		t.Error("Wide should show a sidebar")
	}
	if !GetLayoutConfig(terminal.SizeModeMassive).ShowSidebar {
		t.Error("Massive should show a sidebar")
	}
	if GetLayoutConfig(terminal.SizeModeMassive).TruncateAt != 0 {
		t.Error("Massive should never truncate (TruncateAt = 0)")
	}
	if GetLayoutConfig(terminal.SizeModeMassive).VerticalScroll {
		t.Error("Massive should not need vertical scrolling")
	}
	if !GetLayoutConfig(terminal.SizeModeMicro).VerticalScroll {
		t.Error("Micro should need vertical scrolling")
	}
}

// TestGetLayoutConfigUnknownModeIsZeroValue covers a SizeMode value with no
// table entry.
func TestGetLayoutConfigUnknownModeIsZeroValue(t *testing.T) {
	cfg := GetLayoutConfig(terminal.SizeMode(999))
	if cfg != (LayoutConfig{}) {
		t.Errorf("unknown mode = %+v, want zero value", cfg)
	}
}

// TestGetSpacingForMode covers every SizeMode's spacing unit, confirming the
// progression is monotonically non-decreasing.
func TestGetSpacingForMode(t *testing.T) {
	prev := 0
	for _, mode := range allSizeModes {
		got := GetSpacingForMode(mode)
		if got <= 0 {
			t.Errorf("mode %v: spacing = %d, want > 0", mode, got)
		}
		if got < prev {
			t.Errorf("mode %v: spacing %d is less than the previous mode's %d", mode, got, prev)
		}
		prev = got
	}
}

// TestModeForSizeBoundaries covers the exact breakpoints from AI.md PART 32,
// checking one row below and one row at each threshold.
func TestModeForSizeBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		cols, rows int
		want       terminal.SizeMode
	}{
		{"micro at 0x0", 0, 0, terminal.SizeModeMicro},
		{"micro just under minimal cols", 39, 20, terminal.SizeModeMicro},
		{"micro just under minimal rows", 100, 9, terminal.SizeModeMicro},
		{"minimal at boundary", 40, 10, terminal.SizeModeMinimal},
		{"minimal just under compact cols", 59, 20, terminal.SizeModeMinimal},
		{"compact at boundary", 60, 16, terminal.SizeModeCompact},
		{"compact just under standard cols", 79, 30, terminal.SizeModeCompact},
		{"standard at boundary", 80, 24, terminal.SizeModeStandard},
		{"standard just under wide cols", 119, 30, terminal.SizeModeStandard},
		{"wide at boundary", 120, 40, terminal.SizeModeWide},
		{"wide just under ultrawide cols", 199, 50, terminal.SizeModeWide},
		{"ultrawide at boundary", 200, 60, terminal.SizeModeUltrawide},
		{"ultrawide just under massive cols", 399, 70, terminal.SizeModeUltrawide},
		{"massive at boundary", 400, 80, terminal.SizeModeMassive},
		{"massive far above boundary", 1000, 200, terminal.SizeModeMassive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modeForSize(tc.cols, tc.rows); got != tc.want {
				t.Errorf("modeForSize(%d, %d) = %v, want %v", tc.cols, tc.rows, got, tc.want)
			}
		})
	}
}

// TestGetTUISymbolsUnicodeVsASCII covers the Unicode/ASCII decision matrix:
// a dumb terminal, an unset TERM, a non-UTF-8 locale, and a UTF-8 locale
// with a real TERM.
func TestGetTUISymbolsUnicodeVsASCII(t *testing.T) {
	t.Run("dumb terminal forces ASCII", func(t *testing.T) {
		t.Setenv("LANG", "en_US.UTF-8")
		env := display.DisplayEnv{TerminalType: "dumb"}
		got := GetTUISymbols(env)
		if got.Success != TUISymbolsASCII.Success {
			t.Errorf("got %+v, want ASCII set", got)
		}
	})

	t.Run("empty TERM forces ASCII", func(t *testing.T) {
		t.Setenv("LANG", "en_US.UTF-8")
		env := display.DisplayEnv{TerminalType: ""}
		got := GetTUISymbols(env)
		if got.Success != TUISymbolsASCII.Success {
			t.Errorf("got %+v, want ASCII set", got)
		}
	})

	t.Run("non-UTF-8 locale forces ASCII", func(t *testing.T) {
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", "C")
		env := display.DisplayEnv{TerminalType: "xterm-256color"}
		got := GetTUISymbols(env)
		if got.Success != TUISymbolsASCII.Success {
			t.Errorf("got %+v, want ASCII set", got)
		}
	})

	t.Run("UTF-8 locale with a real TERM uses Unicode", func(t *testing.T) {
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", "en_US.UTF-8")
		env := display.DisplayEnv{TerminalType: "xterm-256color"}
		got := GetTUISymbols(env)
		if got.Success != TUISymbols.Success {
			t.Errorf("got %+v, want Unicode set", got)
		}
	})

	t.Run("no locale vars set at all forces ASCII", func(t *testing.T) {
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", "")
		env := display.DisplayEnv{TerminalType: "xterm-256color"}
		got := GetTUISymbols(env)
		if got.Success != TUISymbolsASCII.Success {
			t.Errorf("got %+v, want ASCII set", got)
		}
	})
}

// TestStylesFromTerminalPaletteDoesNotPanic covers that building the style
// set from both the dark and light palettes never panics and yields a
// usable TUIStyles value.
func TestStylesFromTerminalPaletteDoesNotPanic(t *testing.T) {
	for name, palette := range map[string]theme.TerminalPalette{
		"dark":  theme.TerminalPaletteDark,
		"light": theme.TerminalPaletteLight,
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("StylesFromTerminalPalette panicked: %v", r)
				}
			}()
			_ = StylesFromTerminalPalette(palette)
		})
	}
}
