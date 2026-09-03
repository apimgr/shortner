package theme

import "testing"

func TestGetThemePalette(t *testing.T) {
	tests := []struct {
		name      string
		themeMode string
		colorFgBg string
		want      ThemePalette
	}{
		{"light returns light palette", "light", "", ThemePaletteLight},
		{"dark returns dark palette", "dark", "", ThemePaletteDark},
		{"unknown falls back to dark", "solarized", "", ThemePaletteDark},
		{"empty falls back to dark", "", "", ThemePaletteDark},
		{"auto with dark COLORFGBG returns dark", "auto", "15;0", ThemePaletteDark},
		{"auto with light COLORFGBG returns light", "auto", "0;15", ThemePaletteLight},
		{"auto with no COLORFGBG defaults to dark", "auto", "", ThemePaletteDark},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COLORFGBG", tt.colorFgBg)
			if got := GetThemePalette(tt.themeMode); got != tt.want {
				t.Errorf("GetThemePalette(%q) = %+v, want %+v", tt.themeMode, got, tt.want)
			}
		})
	}
}

func TestIsSystemDarkTheme(t *testing.T) {
	tests := []struct {
		name      string
		colorFgBg string
		want      bool
	}{
		{"unset defaults to dark", "", true},
		{"background index 0 is dark", "15;0", true},
		{"background index 7 is light", "0;7", false},
		{"background index 15 is light", "0;15", false},
		{"background index 8 is dark", "15;8", true},
		{"unparsable background defaults to dark", "15;bg", true},
		{"single value without semicolon defaults to dark", "0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COLORFGBG", tt.colorFgBg)
			if got := IsSystemDarkTheme(); got != tt.want {
				t.Errorf("IsSystemDarkTheme() with COLORFGBG=%q = %v, want %v", tt.colorFgBg, got, tt.want)
			}
		})
	}
}
