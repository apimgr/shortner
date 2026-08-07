package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/httpserver"
)

// captureOutput redirects both os.Stdout and os.Stderr for the duration of
// fn (run() writes to both, directly and via fmt.Fprintln(os.Stderr, ...)).
func captureOutput(t *testing.T, fn func() int) (stdout, stderr string, code int) {
	t.Helper()

	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	code = fn()

	wOut.Close()
	wErr.Close()
	var bufOut, bufErr bytes.Buffer
	io.Copy(&bufOut, rOut)
	io.Copy(&bufErr, rErr)
	return bufOut.String(), bufErr.String(), code
}

func TestFlagWasSet(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	shell := fs.String("shell", "", "")
	other := fs.Bool("other", false, "")
	_ = other

	if err := fs.Parse([]string{"--shell", "help"}); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !flagWasSet(fs, "shell") {
		t.Error("flagWasSet(shell) = false, want true (explicitly passed)")
	}
	if flagWasSet(fs, "other") {
		t.Error("flagWasSet(other) = true, want false (never passed)")
	}
	if flagWasSet(fs, "nonexistent") {
		t.Error("flagWasSet(nonexistent) = true, want false")
	}
	_ = shell
}

func TestFirstArg(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"one", []string{"bash"}, "bash"},
		{"multiple returns first", []string{"bash", "extra"}, "bash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstArg(tt.in); got != tt.want {
				t.Errorf("firstArg(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestInjectDefaultUpdateValue(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"bare trailing --update gets check appended", []string{"--update"}, []string{"--update", "check"}},
		{"--update with value untouched", []string{"--update", "yes"}, []string{"--update", "yes"}},
		{"--update not last untouched", []string{"--update", "--debug"}, []string{"--update", "--debug"}},
		{"no --update untouched", []string{"--debug"}, []string{"--debug"}},
		{"empty args untouched", []string{}, []string{}},
		{"other flag trailing untouched", []string{"--debug"}, []string{"--debug"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := injectDefaultUpdateValue(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("injectDefaultUpdateValue(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("injectDefaultUpdateValue(%v) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"--help", []string{"--help"}, "Usage:"},
		{"-h", []string{"-h"}, "Usage:"},
		{"--version", []string{"--version"}, "devel"},
		{"-v", []string{"-v"}, "devel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return run(tt.args) })
			if code != 0 {
				t.Errorf("run(%v) code = %d, want 0", tt.args, code)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("run(%v) stdout = %q, want to contain %q", tt.args, out, tt.want)
			}
		})
	}
}

func TestRunUnknownFlagReturns2(t *testing.T) {
	_, _, code := captureOutput(t, func() int { return run([]string{"--not-a-real-flag"}) })
	if code != 2 {
		t.Errorf("run() code = %d, want 2 for parse error", code)
	}
}

func TestRunInvalidColorReturns2(t *testing.T) {
	_, stderr, code := captureOutput(t, func() int { return run([]string{"--color=bogus"}) })
	if code != 2 {
		t.Errorf("run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "invalid --color value") {
		t.Errorf("stderr = %q, want to contain color error", stderr)
	}
}

func TestRunDispatchesShell(t *testing.T) {
	out, _, code := captureOutput(t, func() int { return run([]string{"--shell", "help"}) })
	if code != 0 {
		t.Errorf("run(--shell help) code = %d, want 0", code)
	}
	if !strings.Contains(out, "Shell integration") {
		t.Errorf("stdout = %q, want shell help text", out)
	}
}

func TestRunDispatchesService(t *testing.T) {
	out, _, code := captureOutput(t, func() int { return run([]string{"--service", "help"}) })
	if code != 0 {
		t.Errorf("run(--service help) code = %d, want 0", code)
	}
	if !strings.Contains(out, "Manage the") {
		t.Errorf("stdout = %q, want service help text", out)
	}
}

func TestRunDispatchesMaintenance(t *testing.T) {
	out, _, code := captureOutput(t, func() int { return run([]string{"--maintenance", "help"}) })
	if code != 0 {
		t.Errorf("run(--maintenance help) code = %d, want 0", code)
	}
	if !strings.Contains(out, "maintenance operations") {
		t.Errorf("stdout = %q, want maintenance help text", out)
	}
}

func TestRunDispatchesUpdateBareTrailing(t *testing.T) {
	// A bare trailing --update relies on injectDefaultUpdateValue to avoid
	// a flag-parsing error, then dispatches to the "check" branch.
	_, stderr, code := captureOutput(t, func() int { return run([]string{"--update"}) })
	if code != 1 {
		t.Errorf("run(--update) code = %d, want 1 (not yet available)", code)
	}
	if !strings.Contains(stderr, "--update check is not yet available") {
		t.Errorf("stderr = %q, want update-check not-yet-available message", stderr)
	}
}

func TestRunDispatchesUpdateHelp(t *testing.T) {
	out, _, code := captureOutput(t, func() int { return run([]string{"--update", "--help"}) })
	if code != 0 {
		t.Errorf("run(--update --help) code = %d, want 0", code)
	}
	if !strings.Contains(out, "Check for and perform self-updates") {
		t.Errorf("stdout = %q, want update help text", out)
	}
}

// TestRunFullStartupPathWithIsolatedDirs exercises run()'s full startup
// sequence end to end (directory creation, config load/save, token
// generation, PID file lifecycle, banner) while keeping every filesystem
// write confined to a per-test temp directory via explicit --config/--data
// /--cache/--log/--backup/--pid overrides, so it never touches real system
// paths regardless of the privilege level or container status of the test
// runner.
func TestRunFullStartupPathWithIsolatedDirs(t *testing.T) {
	old := startHTTPServer
	startHTTPServer = func(*httpserver.Server) error { return nil }
	defer func() { startHTTPServer = old }()

	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	dataDir := filepath.Join(base, "data")
	cacheDir := filepath.Join(base, "cache")
	logDir := filepath.Join(base, "logs")
	backupDir := filepath.Join(base, "backup")
	pidFile := filepath.Join(base, "run", "shortner.pid")

	args := []string{
		"--config", configDir,
		"--data", dataDir,
		"--cache", cacheDir,
		"--log", logDir,
		"--backup", backupDir,
		"--pid", pidFile,
		"--mode", "development",
		"--color", "no",
	}

	out, _, code := captureOutput(t, func() int { return run(args) })
	if code != 0 {
		t.Fatalf("run() code = %d, want 0; stdout = %q", code, out)
	}

	cfgPath := filepath.Join(configDir, "server.yml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Errorf("expected server.yml to be created at %s: %v", cfgPath, err)
	}
	if !strings.Contains(out, "development") {
		t.Errorf("stdout = %q, want to mention development mode", out)
	}
	for _, dir := range []string{configDir, dataDir, cacheDir, logDir, backupDir} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("expected directory %s to exist: %v", dir, err)
		}
	}
}

func TestRunAppliesAddressPortBaseURLOverrides(t *testing.T) {
	old := startHTTPServer
	startHTTPServer = func(*httpserver.Server) error { return nil }
	defer func() { startHTTPServer = old }()

	base := t.TempDir()
	args := []string{
		"--config", filepath.Join(base, "config"),
		"--data", filepath.Join(base, "data"),
		"--cache", filepath.Join(base, "cache"),
		"--log", filepath.Join(base, "logs"),
		"--backup", filepath.Join(base, "backup"),
		"--pid", filepath.Join(base, "run", "shortner.pid"),
		"--address", "127.0.0.1",
		"--port", "9999",
		"--baseurl", "/s/",
	}

	out, _, code := captureOutput(t, func() int { return run(args) })
	if code != 0 {
		t.Fatalf("run() code = %d, want 0", code)
	}
	if !strings.Contains(out, "127.0.0.1:9999/s/") {
		t.Errorf("stdout = %q, want to contain overridden address/port/baseurl", out)
	}
}
