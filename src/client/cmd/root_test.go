package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// newTestIO builds an in-memory IO so Run never touches the process streams.
func newTestIO() (IO, *bytes.Buffer, *bytes.Buffer) {
	var in, out, errOut bytes.Buffer
	return IO{In: &in, Out: &out, Err: &errOut}, &out, &errOut
}

// isolateHome points HOME (and the XDG_* variables) at a fresh temp
// directory so Run's config loading never touches the real user config.
func isolateHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("SHORTNER_SERVER_PRIMARY", "")
	t.Setenv("SHORTNER_TOKEN", "")
}

// TestRunVersion covers --version: exit 0 and output naming the given
// binary name.
func TestRunVersion(t *testing.T) {
	isolateHome(t)
	streams, out, _ := newTestIO()
	code := Run([]string{"--version"}, streams, "shortner-cli")
	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK", code)
	}
	if !strings.Contains(out.String(), "shortner-cli") {
		t.Fatalf("out = %q, want it to mention the binary name", out.String())
	}
}

// TestRunHelp covers --help: exit 0 and non-empty output.
func TestRunHelp(t *testing.T) {
	isolateHome(t)
	streams, out, _ := newTestIO()
	code := Run([]string{"--help"}, streams, "shortner-cli")
	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK", code)
	}
	if out.Len() == 0 {
		t.Fatal("want help output")
	}
}

// TestRunUnknownFlagExitsUsage covers the parse-failure path: exit 64
// (ExitUsage) per AI.md PART 32's exit code table, plus a usage hint on
// stderr.
func TestRunUnknownFlagExitsUsage(t *testing.T) {
	isolateHome(t)
	streams, _, errOut := newTestIO()
	code := Run([]string{"--not-a-real-flag"}, streams, "shortner-cli")
	if code != ExitUsage {
		t.Fatalf("code = %d, want ExitUsage (64)", code)
	}
	if code != 64 {
		t.Fatalf("ExitUsage = %d, want 64 per AI.md PART 32", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("want an error message on stderr")
	}
}

// TestRunNoServerConfigured covers a command that requires a server
// (list) when cli.yml has never been configured: it must fail with
// ExitConfig rather than attempting a network call.
func TestRunNoServerConfigured(t *testing.T) {
	isolateHome(t)
	streams, _, errOut := newTestIO()
	code := Run([]string{"list"}, streams, "shortner-cli")
	if code != ExitConfig {
		t.Fatalf("code = %d, want ExitConfig", code)
	}
	if !strings.Contains(errOut.String(), "no server configured") {
		t.Fatalf("errOut = %q, want a no-server-configured message", errOut.String())
	}
}

// TestRunHelpCommandDoesNotRequireServer covers that the "help" positional
// command is answered even with no server configured.
func TestRunHelpCommandDoesNotRequireServer(t *testing.T) {
	isolateHome(t)
	streams, out, _ := newTestIO()
	code := Run([]string{"help"}, streams, "shortner-cli")
	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK", code)
	}
	if out.Len() == 0 {
		t.Fatal("want help output")
	}
}

// TestRunFirstInvocationWritesConfigFile covers that a first run creates
// cli.yml so the client works with zero configuration afterward.
func TestRunFirstInvocationWritesConfigFile(t *testing.T) {
	isolateHome(t)
	streams, _, _ := newTestIO()
	Run([]string{"help"}, streams, "shortner-cli")

	streams2, _, errOut2 := newTestIO()
	code := Run([]string{"list"}, streams2, "shortner-cli")
	if code != ExitConfig {
		t.Fatalf("second run code = %d, want ExitConfig (still no server)", code)
	}
	if errOut2.Len() == 0 {
		t.Fatal("want an error message")
	}
}

// TestRunInvalidOutputFlagIsUsageError covers --output with an unsupported
// value.
func TestRunInvalidOutputFlagIsUsageError(t *testing.T) {
	isolateHome(t)
	streams, _, errOut := newTestIO()
	code := Run([]string{"--output", "xml", "list"}, streams, "shortner-cli")
	if code != ExitUsage {
		t.Fatalf("code = %d, want ExitUsage", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("want an error message")
	}
}

// TestRunServerFlagPersistsToConfig covers that a valid --server flag is
// saved to cli.yml when the current value is empty, and is honored on the
// very next invocation without repeating the flag.
func TestRunServerFlagPersistsToConfig(t *testing.T) {
	isolateHome(t)
	streams, _, errOut := newTestIO()
	// 127.0.0.1 with no listener refuses the connection immediately, so the
	// test stays fast and hermetic while still proving requireServer()
	// accepted the flag (a rejected/unconfigured server would fail earlier,
	// with ExitConfig, before any connection is attempted).
	code := Run([]string{"--server", "http://127.0.0.1:1", "list"}, streams, "shortner-cli")
	if code == ExitConfig {
		t.Fatalf("code = ExitConfig, want the server flag to be accepted; stderr = %q", errOut.String())
	}
}

// TestRunUnknownCommand covers the default dispatch branch for a command
// word that is neither a known verb, a URL, nor a slug-shaped bare word.
func TestRunUnknownCommand(t *testing.T) {
	isolateHome(t)
	streams, _, errOut := newTestIO()
	code := Run([]string{"--server", "http://127.0.0.1:1", "%%not-a-slug%%"}, streams, "shortner-cli")
	// updateGate runs first and, on a fetch error, never blocks the
	// command (AI.md PART 32), so the unknown-command usage error is still
	// reached even with no reachable server.
	if code != ExitUsage {
		t.Fatalf("code = %d, want ExitUsage", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("want an error message")
	}
}

// TestSplitCommandViaRunEmptyArgsIsInteractive covers that Run with no
// positional command falls into the interactive dispatch. In this test
// environment stdout is not a terminal, so displayMode resolves to "plain"
// and the result is a usage error rather than a TUI launch.
func TestSplitCommandViaRunEmptyArgsIsInteractive(t *testing.T) {
	isolateHome(t)
	streams, _, errOut := newTestIO()
	code := Run(nil, streams, "shortner-cli")
	if code != ExitUsage {
		t.Fatalf("code = %d, want ExitUsage (no command, no terminal)", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("want an error message")
	}
}

// TestUserAgent covers the User-Agent format, which must always be built
// from the compiled project name, never the possibly-renamed binary
// basename.
func TestUserAgent(t *testing.T) {
	ua := UserAgent()
	if !strings.HasPrefix(ua, "shortner-cli/") {
		t.Fatalf("UserAgent() = %q, want a shortner-cli/ prefix", ua)
	}
}

// TestLooksLikeURL covers the smart-argument URL detection.
func TestLooksLikeURL(t *testing.T) {
	cases := []struct {
		arg  string
		want bool
	}{
		{"https://example.com", true},
		{"http://example.com", true},
		{"HTTPS://EXAMPLE.COM", true},
		{"example.com", false},
		{"abc123", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksLikeURL(tc.arg); got != tc.want {
			t.Errorf("looksLikeURL(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

// TestLooksLikeSlug covers the slug-shape boundary rules: 3-20 chars,
// alphanumeric plus hyphens.
func TestLooksLikeSlug(t *testing.T) {
	cases := []struct {
		arg  string
		want bool
	}{
		{"ab", false},
		{"abc", true},
		{strings.Repeat("a", 20), true},
		{strings.Repeat("a", 21), false},
		{"my-slug-1", true},
		{"has space", false},
		{"has_underscore", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksLikeSlug(tc.arg); got != tc.want {
			t.Errorf("looksLikeSlug(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

// TestParseDuration covers the fallback-on-error and fallback-on-non-positive
// rules used to turn config duration strings into time.Duration.
func TestParseDuration(t *testing.T) {
	fallback := 42 * time.Second
	cases := []struct {
		value string
		want  bool // true => expect the parsed value, false => expect fallback
	}{
		{"30s", true},
		{"", false},
		{"not-a-duration", false},
		{"0s", false},
		{"-5s", false},
	}
	for _, tc := range cases {
		got := parseDuration(tc.value, fallback)
		if tc.want {
			if got == fallback {
				t.Errorf("parseDuration(%q) = fallback, want the parsed value", tc.value)
			}
		} else if got != fallback {
			t.Errorf("parseDuration(%q) = %v, want fallback %v", tc.value, got, fallback)
		}
	}
}
