package color

import "testing"

func TestParseFlag(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    *bool
		wantErr bool
	}{
		{"empty means auto", "", nil, false},
		{"auto explicit", "auto", nil, false},
		{"auto uppercase", "AUTO", nil, false},
		{"whitespace auto", "  auto  ", nil, false},
		{"yes forces on", "yes", boolPtr(true), false},
		{"no forces off", "no", boolPtr(false), false},
		{"invalid value errors", "maybe", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFlag(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseFlag(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.want == nil {
				if got != nil {
					t.Errorf("ParseFlag(%q) = %v, want nil", tt.in, *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Errorf("ParseFlag(%q) = %v, want %v", tt.in, got, *tt.want)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

func TestEnabledForceColorWins(t *testing.T) {
	// forceColor takes priority over NO_COLOR and any terminal detection.
	t.Setenv("NO_COLOR", "1")

	forceOn := true
	if !Enabled(&forceOn) {
		t.Error("Enabled(true) = false, want true even with NO_COLOR set")
	}

	forceOff := false
	if Enabled(&forceOff) {
		t.Error("Enabled(false) = true, want false")
	}
}

func TestEnabledNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if Enabled(nil) {
		t.Error("Enabled(nil) = true, want false when NO_COLOR is set")
	}
}

func TestEnabledAutoDetectFallsThroughToDisplay(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	// In the non-interactive test process, stdout is not a TTY, so
	// CanUseANSI must return false regardless of TERM.
	got := Enabled(nil)
	if got {
		t.Error("Enabled(nil) = true in non-TTY test process, want false")
	}
}

func TestEmojiEnabled(t *testing.T) {
	tests := []struct {
		name    string
		noColor string
		term    string
		want    bool
	}{
		{"no overrides", "", "xterm", true},
		{"NO_COLOR disables", "1", "xterm", false},
		{"TERM=dumb disables", "", "dumb", false},
		{"both set", "1", "dumb", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			t.Setenv("TERM", tt.term)
			if got := EmojiEnabled(); got != tt.want {
				t.Errorf("EmojiEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
