package terminal

import "testing"

func TestCalculateMode(t *testing.T) {
	tests := []struct {
		name       string
		cols, rows int
		want       SizeMode
	}{
		{"micro by cols", 20, 24, SizeModeMicro},
		{"micro by rows", 80, 5, SizeModeMicro},
		{"minimal by cols", 50, 24, SizeModeMinimal},
		{"minimal by rows", 80, 12, SizeModeMinimal},
		{"compact by cols", 70, 24, SizeModeCompact},
		{"compact by rows", 80, 20, SizeModeCompact},
		{"standard exact boundary", 80, 24, SizeModeStandard},
		{"standard by cols", 100, 24, SizeModeStandard},
		{"wide exact boundary", 120, 40, SizeModeWide},
		{"ultrawide exact boundary", 200, 60, SizeModeUltrawide},
		{"massive exact boundary", 400, 80, SizeModeMassive},
		{"massive well above", 1000, 200, SizeModeMassive},
		{"zero is micro", 0, 0, SizeModeMicro},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calculateMode(tt.cols, tt.rows); got != tt.want {
				t.Errorf("calculateMode(%d, %d) = %v, want %v", tt.cols, tt.rows, got, tt.want)
			}
		})
	}
}

func TestGetTerminalSizeFallback(t *testing.T) {
	// In the non-interactive test process, stdout is not a TTY so
	// term.GetSize returns 0,0 and GetTerminalSize must fall back to 80x24.
	size := GetTerminalSize()
	if size.Cols != 80 {
		t.Errorf("Cols = %d, want fallback 80", size.Cols)
	}
	if size.Rows != 24 {
		t.Errorf("Rows = %d, want fallback 24", size.Rows)
	}
	if size.Mode != SizeModeStandard {
		t.Errorf("Mode = %v, want %v", size.Mode, SizeModeStandard)
	}
}

func TestSizeModeBreakpointHelpers(t *testing.T) {
	tests := []struct {
		mode         SizeMode
		wantASCIIArt bool
		wantBorders  bool
		wantSidebar  bool
		wantIcons    bool
	}{
		{SizeModeMicro, false, false, false, false},
		{SizeModeMinimal, false, false, false, true},
		{SizeModeCompact, false, true, false, true},
		{SizeModeStandard, true, true, false, true},
		{SizeModeWide, true, true, true, true},
		{SizeModeUltrawide, true, true, true, true},
		{SizeModeMassive, true, true, true, true},
	}
	for _, tt := range tests {
		if got := tt.mode.ShowASCIIArt(); got != tt.wantASCIIArt {
			t.Errorf("%v.ShowASCIIArt() = %v, want %v", tt.mode, got, tt.wantASCIIArt)
		}
		if got := tt.mode.ShowBorders(); got != tt.wantBorders {
			t.Errorf("%v.ShowBorders() = %v, want %v", tt.mode, got, tt.wantBorders)
		}
		if got := tt.mode.ShowSidebar(); got != tt.wantSidebar {
			t.Errorf("%v.ShowSidebar() = %v, want %v", tt.mode, got, tt.wantSidebar)
		}
		if got := tt.mode.ShowIcons(); got != tt.wantIcons {
			t.Errorf("%v.ShowIcons() = %v, want %v", tt.mode, got, tt.wantIcons)
		}
	}
}
