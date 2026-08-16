package display

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("w.Close() error = %v", err)
	}
	os.Stdout = orig

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	return string(out)
}

func TestNewSpinner(t *testing.T) {
	tests := []struct {
		name     string
		env      *DisplayEnv
		noColor  string
		wantANSI bool
	}{
		{"dumb terminal returns TextSpinner", &DisplayEnv{TerminalType: "dumb", IsTerminal: true}, "", false},
		{"normal terminal returns ANSISpinner", &DisplayEnv{TerminalType: "xterm-256color", IsTerminal: true}, "", true},
		{"NO_COLOR returns TextSpinner", &DisplayEnv{TerminalType: "xterm-256color", IsTerminal: true}, "1", false},
		{"non-terminal returns TextSpinner", &DisplayEnv{TerminalType: "xterm-256color", IsTerminal: false}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			s := NewSpinner(tt.env, "Working")
			_, isANSI := s.(*ANSISpinner)
			if isANSI != tt.wantANSI {
				t.Errorf("NewSpinner() ANSI = %v, want %v", isANSI, tt.wantANSI)
			}
		})
	}
}

func TestTextSpinnerStartStop(t *testing.T) {
	s := &TextSpinner{message: "Processing"}

	startOut := captureStdout(t, s.Start)
	if startOut != "Processing...\n" {
		t.Errorf("TextSpinner.Start() output = %q, want %q", startOut, "Processing...\n")
	}
	if strings.ContainsRune(startOut, '\x1b') {
		t.Errorf("TextSpinner.Start() output contains ANSI escape: %q", startOut)
	}

	stopOut := captureStdout(t, func() { s.Stop("") })
	if stopOut != "Done.\n" {
		t.Errorf("TextSpinner.Stop(\"\") output = %q, want %q", stopOut, "Done.\n")
	}

	stopOut = captureStdout(t, func() { s.Stop("Finished") })
	if stopOut != "Finished\n" {
		t.Errorf("TextSpinner.Stop(\"Finished\") output = %q, want %q", stopOut, "Finished\n")
	}
}

func TestANSISpinnerStartStop(t *testing.T) {
	s := &ANSISpinner{message: "Working"}

	startOut := captureStdout(t, s.Start)
	if !strings.Contains(startOut, "Working") {
		t.Errorf("ANSISpinner.Start() output = %q, want it to contain %q", startOut, "Working")
	}
	if !strings.ContainsRune(startOut, '\x1b') {
		t.Errorf("ANSISpinner.Start() output = %q, want an ANSI escape", startOut)
	}

	stopOut := captureStdout(t, func() { s.Stop("") })
	if !strings.Contains(stopOut, "Done.") {
		t.Errorf("ANSISpinner.Stop(\"\") output = %q, want it to contain %q", stopOut, "Done.")
	}
	if !strings.ContainsRune(stopOut, '\x1b') {
		t.Errorf("ANSISpinner.Stop() output = %q, want an ANSI escape", stopOut)
	}
}

func TestShowProgress(t *testing.T) {
	tests := []struct {
		name      string
		env       *DisplayEnv
		noColor   string
		percent   int
		wantExact string
		wantANSI  bool
	}{
		{"dumb terminal prints plain text", &DisplayEnv{TerminalType: "dumb", IsTerminal: true}, "", 50, "50% complete\n", false},
		{"dumb terminal at 0 percent", &DisplayEnv{TerminalType: "dumb", IsTerminal: true}, "", 0, "0% complete\n", false},
		{"normal terminal prints ANSI bar", &DisplayEnv{TerminalType: "xterm-256color", IsTerminal: true}, "", 50, "", true},
		{"NO_COLOR prints plain text", &DisplayEnv{TerminalType: "xterm-256color", IsTerminal: true}, "1", 50, "50% complete\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			out := captureStdout(t, func() { ShowProgress(tt.env, tt.percent) })
			if tt.wantExact != "" && out != tt.wantExact {
				t.Errorf("ShowProgress() output = %q, want %q", out, tt.wantExact)
			}
			if tt.wantANSI && !strings.Contains(out, "\r[") {
				t.Errorf("ShowProgress() output = %q, want an ANSI progress bar", out)
			}
			if !tt.wantANSI && strings.ContainsRune(out, '\x1b') {
				t.Errorf("ShowProgress() output = %q, want no ANSI escape", out)
			}
		})
	}
}
