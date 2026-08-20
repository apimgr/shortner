package cmd

import (
	"reflect"
	"testing"
)

// TestParseFlagsBoolFlags covers every shared bool flag from AI.md PART 32
// (--help --version --debug --force --quiet --verbose), plus the short
// forms -h/-v, standalone and with an explicit boolean word.
func TestParseFlagsBoolFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want Flags
	}{
		{"help long", []string{"--help"}, Flags{Help: true}},
		{"help short", []string{"-h"}, Flags{Help: true}},
		{"version long", []string{"--version"}, Flags{Version: true}},
		{"version short", []string{"-v"}, Flags{Version: true}},
		{"debug", []string{"--debug"}, Flags{Debug: true}},
		{"force", []string{"--force"}, Flags{Force: true}},
		{"quiet", []string{"--quiet"}, Flags{Quiet: true}},
		{"verbose", []string{"--verbose"}, Flags{Verbose: true}},
		{"debug explicit yes", []string{"--debug", "yes"}, Flags{Debug: true}},
		{"debug explicit no", []string{"--debug", "no"}, Flags{Debug: false}},
		{"debug equals false", []string{"--debug=false"}, Flags{Debug: false}},
		{"debug equals on", []string{"--debug=on"}, Flags{Debug: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFlags(tc.args)
			if err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			got.Args = nil
			tc.want.Args = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestParseFlagsDebugDoesNotConsumeACommand checks that a bool flag
// followed by a non-boolean-word positional argument (e.g. a command name)
// leaves that argument as positional rather than swallowing it.
func TestParseFlagsDebugDoesNotConsumeACommand(t *testing.T) {
	got, err := ParseFlags([]string{"--debug", "list"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !got.Debug {
		t.Fatal("Debug = false")
	}
	if len(got.Args) != 1 || got.Args[0] != "list" {
		t.Fatalf("Args = %v, want [list]", got.Args)
	}
}

// TestParseFlagsClientFlags covers --server --token --output, the
// AI.md-required client flags, and a few of the value flags.
func TestParseFlagsClientFlags(t *testing.T) {
	got, err := ParseFlags([]string{
		"--server", "https://example.com",
		"--token", "tok_abc",
		"--output", "json",
		"--slug", "my-slug",
		"--url", "https://dest.example.com",
		"--expire", "2030-01-01",
		"--limit", "10",
		"--page", "2",
		"--token-file", "/path/to/token",
		"--config", "/path/to/cli.yml",
		"--color", "never",
		"--lang", "fr",
	})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got.Server != "https://example.com" {
		t.Errorf("Server = %q", got.Server)
	}
	if got.Token != "tok_abc" {
		t.Errorf("Token = %q", got.Token)
	}
	if got.Output != "json" {
		t.Errorf("Output = %q", got.Output)
	}
	if got.Slug != "my-slug" || got.URL != "https://dest.example.com" || got.Expire != "2030-01-01" {
		t.Errorf("link flags: %+v", got)
	}
	if got.Limit != "10" || got.Page != "2" {
		t.Errorf("pagination flags: %+v", got)
	}
	if got.TokenFile != "/path/to/token" || got.Config != "/path/to/cli.yml" {
		t.Errorf("file flags: %+v", got)
	}
	if got.Color != "never" || got.Lang != "fr" {
		t.Errorf("display flags: %+v", got)
	}
}

// TestParseFlagsEqualsForm covers `--flag=value` for a value flag.
func TestParseFlagsEqualsForm(t *testing.T) {
	got, err := ParseFlags([]string{"--server=https://example.com"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got.Server != "https://example.com" {
		t.Fatalf("Server = %q", got.Server)
	}
}

// TestParseFlagsValueFlagMissingValue covers the error path when a value
// flag is the last argument.
func TestParseFlagsValueFlagMissingValue(t *testing.T) {
	_, err := ParseFlags([]string{"--server"})
	if err == nil {
		t.Fatal("want error for --server with no value")
	}
}

// TestParseFlagsOptionalValueFlags covers --shell and --update: value
// present, value omitted (next token starts with '-' or args ends), and a
// bare --update alone.
func TestParseFlagsOptionalValueFlags(t *testing.T) {
	t.Run("shell with value", func(t *testing.T) {
		got, err := ParseFlags([]string{"--shell", "bash"})
		if err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if !got.ShellSet || got.Shell != "bash" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("shell with no value at end of args", func(t *testing.T) {
		got, err := ParseFlags([]string{"--shell"})
		if err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if !got.ShellSet || got.Shell != "" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("shell followed by another flag does not consume it", func(t *testing.T) {
		got, err := ParseFlags([]string{"--shell", "--debug"})
		if err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if !got.ShellSet || got.Shell != "" || !got.Debug {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("bare update defaults empty", func(t *testing.T) {
		got, err := ParseFlags([]string{"--update"})
		if err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if !got.UpdateSet || got.Update != "" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("update with explicit value", func(t *testing.T) {
		got, err := ParseFlags([]string{"--update", "check"})
		if err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if !got.UpdateSet || got.Update != "check" {
			t.Fatalf("got %+v", got)
		}
	})
}

// TestParseFlagsPositionalArgs covers positional-only invocations and the
// `--` separator that forces everything after it to be positional.
func TestParseFlagsPositionalArgs(t *testing.T) {
	got, err := ParseFlags([]string{"shorten", "https://example.com"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !reflect.DeepEqual(got.Args, []string{"shorten", "https://example.com"}) {
		t.Fatalf("Args = %v", got.Args)
	}

	got, err = ParseFlags([]string{"--debug", "--", "-not-a-flag", "--also-not"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !got.Debug {
		t.Fatal("Debug = false")
	}
	if !reflect.DeepEqual(got.Args, []string{"-not-a-flag", "--also-not"}) {
		t.Fatalf("Args = %v", got.Args)
	}
}

// TestParseFlagsSingleDashLongOption covers the `-server` alias, and that a
// bare `-` is treated as positional rather than an unknown flag.
func TestParseFlagsSingleDashLongOption(t *testing.T) {
	got, err := ParseFlags([]string{"-server", "https://example.com"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got.Server != "https://example.com" {
		t.Fatalf("Server = %q", got.Server)
	}

	got, err = ParseFlags([]string{"-"})
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !reflect.DeepEqual(got.Args, []string{"-"}) {
		t.Fatalf("Args = %v, want a bare dash treated as positional", got.Args)
	}
}

// TestParseFlagsUnknownFlagIsAnError covers the default branch.
func TestParseFlagsUnknownFlagIsAnError(t *testing.T) {
	_, err := ParseFlags([]string{"--not-a-real-flag"})
	if err == nil {
		t.Fatal("want error for unknown flag")
	}
}

// TestParseFlagsEmptyArgs covers the boundary of no arguments at all.
func TestParseFlagsEmptyArgs(t *testing.T) {
	got, err := ParseFlags(nil)
	if err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if len(got.Args) != 0 {
		t.Fatalf("Args = %v, want empty", got.Args)
	}
}

// TestIsBooleanWord covers every recognized truthy/falsey token plus a
// non-boolean word.
func TestIsBooleanWord(t *testing.T) {
	truthyFalsey := []string{
		"true", "yes", "on", "1", "enable", "enabled",
		"false", "no", "off", "0", "disable", "disabled", "none",
		"TRUE", "Yes",
	}
	for _, word := range truthyFalsey {
		if !isBooleanWord(word) {
			t.Errorf("isBooleanWord(%q) = false, want true", word)
		}
	}
	notBoolean := []string{"list", "https://example.com", "", "maybe"}
	for _, word := range notBoolean {
		if isBooleanWord(word) {
			t.Errorf("isBooleanWord(%q) = true, want false", word)
		}
	}
}

// TestSplitCommand covers the empty, single, and multi-argument cases.
func TestSplitCommand(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantCmd string
		wantArg []string
	}{
		{"empty", nil, "", nil},
		{"single", []string{"list"}, "list", []string{}},
		{"with args", []string{"get", "abc123"}, "get", []string{"abc123"}},
		{"with many args", []string{"update", "abc", "--url", "x"}, "update", []string{"abc", "--url", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, args := splitCommand(tc.args)
			if cmd != tc.wantCmd {
				t.Errorf("command = %q, want %q", cmd, tc.wantCmd)
			}
			if tc.wantArg == nil {
				if len(args) != 0 {
					t.Errorf("args = %v, want empty", args)
				}
				return
			}
			if !reflect.DeepEqual(args, tc.wantArg) {
				t.Errorf("args = %v, want %v", args, tc.wantArg)
			}
		})
	}
}
