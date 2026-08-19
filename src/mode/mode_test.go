package mode

import (
	"strings"
	"testing"
)

// resetModeState restores package-level state between test cases, since
// mode.go keeps mutable package vars (currentMode/debugEnabled).
func resetModeState() {
	currentMode = Production
	debugEnabled = false
}

func TestAppModeString(t *testing.T) {
	tests := []struct {
		name string
		m    AppMode
		want string
	}{
		{"production", Production, "production"},
		{"development", Development, "development"},
		{"debug", Debug, "debug"},
		{"unknown value falls back to production", AppMode(99), "production"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetAppMode(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantMode  AppMode
		wantDebug bool
	}{
		{"dev alias", "dev", Development, false},
		{"devel alias", "devel", Development, false},
		{"development full", "development", Development, false},
		{"debug selects debug mode and enables debug", "debug", Debug, true},
		{"uppercase normalizes", "DEBUG", Debug, true},
		{"whitespace trimmed", "  dev  ", Development, false},
		{"empty defaults to production", "", Production, false},
		{"unknown defaults to production", "bogus", Production, false},
		{"explicit production", "production", Production, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetModeState()
			SetAppMode(tt.in)
			if GetCurrentAppMode() != tt.wantMode {
				t.Errorf("GetCurrentAppMode() = %v, want %v", GetCurrentAppMode(), tt.wantMode)
			}
			if IsDebugEnabled() != tt.wantDebug {
				t.Errorf("IsDebugEnabled() = %v, want %v", IsDebugEnabled(), tt.wantDebug)
			}
		})
	}
}

func TestSetDebugEnabled(t *testing.T) {
	resetModeState()
	SetDebugEnabled(true)
	if !IsDebugEnabled() {
		t.Fatal("expected debug enabled")
	}
	SetDebugEnabled(false)
	if IsDebugEnabled() {
		t.Fatal("expected debug disabled")
	}
}

func TestIsAppModeDevProd(t *testing.T) {
	resetModeState()
	SetAppMode("production")
	if !IsAppModeProd() || IsAppModeDev() {
		t.Fatal("expected production mode")
	}
	SetAppMode("development")
	if !IsAppModeDev() || IsAppModeProd() {
		t.Fatal("expected development mode")
	}
}

func TestGetAppModeString(t *testing.T) {
	resetModeState()
	SetAppMode("production")
	if got := GetAppModeString(); got != "production" {
		t.Errorf("GetAppModeString() = %q, want %q", got, "production")
	}

	resetModeState()
	SetAppMode("debug")
	if got := GetAppModeString(); got != "debug [debugging]" {
		t.Errorf("GetAppModeString() = %q, want %q", got, "debug [debugging]")
	}
}

func TestBanner(t *testing.T) {
	resetModeState()
	SetAppMode("production")
	if got := Banner(); !strings.Contains(got, "🔒") || !strings.Contains(got, "production") {
		t.Errorf("Banner() = %q, want lock icon + production", got)
	}

	resetModeState()
	SetAppMode("development")
	if got := Banner(); !strings.Contains(got, "🔧") || !strings.Contains(got, "development") {
		t.Errorf("Banner() = %q, want wrench icon + development", got)
	}
}

// TestFromEnv covers the MODE/DEBUG env var priority rules: MODE=debug
// selects debug mode and defaults debug on, but an explicitly set DEBUG
// (even DEBUG=false) always wins over that default.
func TestFromEnv(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		debug     string
		setDebug  bool
		wantMode  AppMode
		wantDebug bool
	}{
		{"MODE unset, DEBUG unset", "", "", false, Production, false},
		{"MODE=development", "development", "", false, Development, false},
		{"MODE=debug sets debug mode + debug on", "debug", "", false, Debug, true},
		{"MODE=debug DEBUG=false disables debug but keeps debug mode", "debug", "false", true, Debug, false},
		{"MODE=production DEBUG=true", "production", "true", true, Production, true},
		{"DEBUG=1 alone enables debug, mode stays production", "", "1", true, Production, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetModeState()
			if tt.mode != "" {
				t.Setenv("MODE", tt.mode)
			} else {
				t.Setenv("MODE", "")
			}
			if tt.setDebug {
				t.Setenv("DEBUG", tt.debug)
			}
			FromEnv()
			if GetCurrentAppMode() != tt.wantMode {
				t.Errorf("mode = %v, want %v", GetCurrentAppMode(), tt.wantMode)
			}
			if IsDebugEnabled() != tt.wantDebug {
				t.Errorf("debug = %v, want %v", IsDebugEnabled(), tt.wantDebug)
			}
		})
	}
}
