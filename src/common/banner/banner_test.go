package banner

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/common/terminal"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. The banner package prints directly to
// os.Stdout (via fmt.Println/Printf), so this is the only way to assert
// on its output without changing the production code.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	return buf.String()
}

func TestModeLabel(t *testing.T) {
	tests := []struct {
		name string
		cfg  BannerConfig
		want string
	}{
		{"no debug", BannerConfig{AppMode: "production"}, "production"},
		{"debug suffix", BannerConfig{AppMode: "development", Debug: true}, "development [debug]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := modeLabel(tt.cfg); got != tt.want {
				t.Errorf("modeLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintStartupBannerFull(t *testing.T) {
	cfg := BannerConfig{
		AppName: "shortner",
		Version: "1.0.0",
		AppMode: "production",
		URLs:    []string{"http://localhost:8090/"},
	}
	out := captureStdout(t, func() {
		printStartupBannerFull(cfg, terminal.TerminalSize{Cols: 80, Rows: 24, Mode: terminal.SizeModeStandard})
	})
	for _, want := range []string{"shortner", "1.0.0", "production", "http://localhost:8090/"} {
		if !strings.Contains(out, want) {
			t.Errorf("full banner output = %q, want to contain %q", out, want)
		}
	}
}

func TestPrintStartupBannerCompact(t *testing.T) {
	cfg := BannerConfig{AppName: "shortner", Version: "1.0.0", AppMode: "production", URLs: []string{"http://x/"}}
	out := captureStdout(t, func() { printStartupBannerCompact(cfg) })
	if !strings.Contains(out, "shortner") || !strings.Contains(out, "http://x/") {
		t.Errorf("compact banner output = %q, missing expected content", out)
	}
}

func TestPrintStartupBannerMinimal(t *testing.T) {
	cfg := BannerConfig{AppName: "shortner", Version: "1.0.0", AppMode: "production", URLs: []string{"http://x/"}}
	out := captureStdout(t, func() { printStartupBannerMinimal(cfg) })
	if !strings.Contains(out, "shortner") || !strings.Contains(out, "Mode: production") {
		t.Errorf("minimal banner output = %q, missing expected content", out)
	}
}

func TestPrintStartupBannerMicro(t *testing.T) {
	t.Run("with URL", func(t *testing.T) {
		cfg := BannerConfig{AppName: "shortner", Version: "1.0.0", URLs: []string{"http://x/"}}
		out := captureStdout(t, func() { printStartupBannerMicro(cfg) })
		if !strings.Contains(out, "shortner 1.0.0 http://x/") {
			t.Errorf("micro banner output = %q, want to contain URL", out)
		}
	})
	t.Run("without URL", func(t *testing.T) {
		cfg := BannerConfig{AppName: "shortner", Version: "1.0.0"}
		out := captureStdout(t, func() { printStartupBannerMicro(cfg) })
		if strings.TrimSpace(out) != "shortner 1.0.0" {
			t.Errorf("micro banner output = %q, want %q", strings.TrimSpace(out), "shortner 1.0.0")
		}
	})
}

func TestPrintStartupBannerDispatchesBySize(t *testing.T) {
	// PrintStartupBanner itself calls terminal.GetTerminalSize(), which
	// falls back to 80x24 (SizeModeStandard) in the non-TTY test process.
	// This exercises the >= SizeModeStandard branch end-to-end.
	cfg := BannerConfig{AppName: "shortner", Version: "1.0.0", AppMode: "production", URLs: []string{"http://x/"}}
	out := captureStdout(t, func() { PrintStartupBanner(cfg) })
	if !strings.Contains(out, "shortner") {
		t.Errorf("PrintStartupBanner() output = %q, want to contain app name", out)
	}
}

func TestPrintBoxLineTruncatesLongText(t *testing.T) {
	out := captureStdout(t, func() { printBoxLine(10, "this text is way too long for the box") })
	if strings.Contains(out, "way too long for the box") {
		t.Errorf("printBoxLine() did not truncate: %q", out)
	}
}

func TestUseBoxDrawing(t *testing.T) {
	t.Setenv("TERM", "dumb")
	if useBoxDrawing() {
		t.Error("useBoxDrawing() = true with TERM=dumb, want false")
	}
	t.Setenv("TERM", "xterm-256color")
	// Result depends on terminal detection in the test process, but must
	// not panic and must be a valid bool either way; re-run for TERM=dumb
	// is the only branch we can assert deterministically.
	_ = useBoxDrawing()
}
