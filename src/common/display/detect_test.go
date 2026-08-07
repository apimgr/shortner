package display

import "testing"

func TestAutoDetectDisplayMode(t *testing.T) {
	tests := []struct {
		name string
		env  DisplayEnv
		want DisplayMode
	}{
		{
			name: "no terminal no display is headless",
			env:  DisplayEnv{IsTerminal: false, HasDisplay: false},
			want: DisplayModeHeadless,
		},
		{
			name: "TERM=dumb forces CLI even with terminal",
			env:  DisplayEnv{IsTerminal: true, TerminalType: "dumb"},
			want: DisplayModeCLI,
		},
		{
			name: "display without ssh/mosh is GUI",
			env:  DisplayEnv{HasDisplay: true, IsSSH: false, IsMosh: false},
			want: DisplayModeGUI,
		},
		{
			name: "display over ssh is not GUI, falls to terminal check",
			env:  DisplayEnv{HasDisplay: true, IsSSH: true, IsTerminal: true},
			want: DisplayModeTUI,
		},
		{
			name: "display over mosh is not GUI",
			env:  DisplayEnv{HasDisplay: true, IsMosh: true, IsTerminal: true},
			want: DisplayModeTUI,
		},
		{
			name: "terminal without display is TUI",
			env:  DisplayEnv{IsTerminal: true, HasDisplay: false},
			want: DisplayModeTUI,
		},
		{
			name: "no terminal, ssh display but not terminal falls to CLI",
			env:  DisplayEnv{IsTerminal: false, HasDisplay: true, IsSSH: true},
			want: DisplayModeCLI,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := tt.env
			if got := e.autoDetectDisplayMode(); got != tt.want {
				t.Errorf("autoDetectDisplayMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsDumbTerminal(t *testing.T) {
	e := DisplayEnv{TerminalType: "dumb"}
	if !e.IsDumbTerminal() {
		t.Error("IsDumbTerminal() = false, want true")
	}
	e.TerminalType = "xterm-256color"
	if e.IsDumbTerminal() {
		t.Error("IsDumbTerminal() = true, want false")
	}
}

func TestDisplayModeQueryHelpers(t *testing.T) {
	tests := []struct {
		mode                    DisplayMode
		gui, tui, cli, headless bool
	}{
		{DisplayModeGUI, true, false, false, false},
		{DisplayModeTUI, false, true, false, false},
		{DisplayModeCLI, false, false, true, false},
		{DisplayModeHeadless, false, false, false, true},
	}
	for _, tt := range tests {
		e := DisplayEnv{Mode: tt.mode}
		if got := e.IsAutoDetectDisplayModeGUI(); got != tt.gui {
			t.Errorf("mode %v: IsAutoDetectDisplayModeGUI() = %v, want %v", tt.mode, got, tt.gui)
		}
		if got := e.IsAutoDetectDisplayModeTUI(); got != tt.tui {
			t.Errorf("mode %v: IsAutoDetectDisplayModeTUI() = %v, want %v", tt.mode, got, tt.tui)
		}
		if got := e.IsAutoDetectDisplayModeCLI(); got != tt.cli {
			t.Errorf("mode %v: IsAutoDetectDisplayModeCLI() = %v, want %v", tt.mode, got, tt.cli)
		}
		if got := e.IsAutoDetectDisplayModeHeadless(); got != tt.headless {
			t.Errorf("mode %v: IsAutoDetectDisplayModeHeadless() = %v, want %v", tt.mode, got, tt.headless)
		}
	}
}

func TestCanUseANSI(t *testing.T) {
	tests := []struct {
		name    string
		env     DisplayEnv
		noColor string
		want    bool
	}{
		{"dumb terminal disables ANSI", DisplayEnv{TerminalType: "dumb", IsTerminal: true}, "", false},
		{"NO_COLOR disables ANSI", DisplayEnv{IsTerminal: true}, "1", false},
		{"non-terminal disables ANSI", DisplayEnv{IsTerminal: false}, "", false},
		{"terminal without NO_COLOR enables ANSI", DisplayEnv{IsTerminal: true}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			e := tt.env
			if got := CanUseANSI(&e); got != tt.want {
				t.Errorf("CanUseANSI() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetectDisplayEnvRunsWithoutPanic exercises the full auto-detection
// path (including the platform-specific detectPlatformDisplay, which is
// hard to make deterministic in a headless CI/container environment — see
// coverage-gap notes in the final report). It only asserts the function
// completes and returns internally consistent fields.
func TestDetectDisplayEnvRunsWithoutPanic(t *testing.T) {
	env := DetectDisplayEnv()
	switch env.Mode {
	case DisplayModeHeadless, DisplayModeCLI, DisplayModeTUI, DisplayModeGUI:
	default:
		t.Errorf("Mode = %v, want one of the four known DisplayMode values", env.Mode)
	}
}
