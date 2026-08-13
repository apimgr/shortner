package theme

import "testing"

// TestPalettesPopulated ensures every semantic role is set (non-empty) for
// both the web and terminal palettes, in both dark and light variants —
// catching a copy-paste gap that leaves a role rendering as an empty CSS
// custom property or ANSI index.
func TestPalettesPopulated(t *testing.T) {
	themePalettes := map[string]ThemePalette{
		"dark":  ThemePaletteDark,
		"light": ThemePaletteLight,
	}
	for name, p := range themePalettes {
		for field, v := range map[string]string{
			"Background": p.Background, "Foreground": p.Foreground,
			"Primary": p.Primary, "Secondary": p.Secondary, "Accent": p.Accent,
			"Success": p.Success, "Warning": p.Warning, "Error": p.Error,
			"Info": p.Info, "Surface": p.Surface, "SurfaceAlt": p.SurfaceAlt,
			"Border": p.Border, "Muted": p.Muted,
		} {
			if v == "" {
				t.Errorf("ThemePalette%s.%s is empty", name, field)
			}
			if v[0] != '#' {
				t.Errorf("ThemePalette%s.%s = %q, want a #hex color", name, field, v)
			}
		}
	}

	termPalettes := map[string]TerminalPalette{
		"dark":  TerminalPaletteDark,
		"light": TerminalPaletteLight,
	}
	for name, p := range termPalettes {
		for field, v := range map[string]string{
			"Background": p.Background, "Foreground": p.Foreground,
			"Primary": p.Primary, "Secondary": p.Secondary, "Accent": p.Accent,
			"Success": p.Success, "Warning": p.Warning, "Error": p.Error,
			"Info": p.Info, "Surface": p.Surface, "SurfaceAlt": p.SurfaceAlt,
			"Border": p.Border, "Muted": p.Muted,
		} {
			if v == "" {
				t.Errorf("TerminalPalette%s.%s is empty", name, field)
			}
		}
	}
}

// TestThemePaletteDarkIsDracula spot-checks the Dracula-scheme values
// mandated by IDEA.md "Frontend design reference".
func TestThemePaletteDarkIsDracula(t *testing.T) {
	want := map[string]string{
		"Background": "#282a36",
		"Foreground": "#f8f8f2",
		"Primary":    "#bd93f9",
		"Error":      "#ff5555",
		"Success":    "#50fa7b",
	}
	got := map[string]string{
		"Background": ThemePaletteDark.Background,
		"Foreground": ThemePaletteDark.Foreground,
		"Primary":    ThemePaletteDark.Primary,
		"Error":      ThemePaletteDark.Error,
		"Success":    ThemePaletteDark.Success,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("ThemePaletteDark.%s = %q, want %q", k, got[k], w)
		}
	}
}
