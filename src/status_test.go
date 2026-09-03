package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/updater"
)

// statusPaths builds a Paths whose PID file and data dir live in a temp
// dir, so --status reads only test-owned state.
func statusPaths(t *testing.T, pidPath string) paths.Paths {
	t.Helper()
	return paths.Paths{Data: t.TempDir(), PIDFile: pidPath}
}

func TestRunStatusNotRunning(t *testing.T) {
	dir := t.TempDir()
	p := statusPaths(t, filepath.Join(dir, "does-not-exist.pid"))

	out, _, code := captureOutput(t, func() int { return runStatus("shortner", p) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(out, "shortner is not running") {
		t.Errorf("output = %q, want not-running message", out)
	}
}

func TestRunStatusRunning(t *testing.T) {
	if pidfile.IsContainer() {
		t.Skip("WritePIDFile is a no-op inside a container; can't seed a real PID file")
	}
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "shortner.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// runStatus uses pidfile.CheckPIDFile, which verifies the PID both
	// exists (true, it's our own test process) and matches the "shortner"
	// binary name (false, since this is the test binary). This exercises
	// the "not running" (stale-file) branch deterministically rather than
	// the "running" branch, which needs the real compiled binary name —
	// documented as a coverage gap in the final report.
	out, _, code := captureOutput(t, func() int { return runStatus("shortner", statusPaths(t, pidPath)) })
	if code != 1 {
		t.Errorf("code = %d, want 1 (PID belongs to test binary, not shortner)", code)
	}
	if !strings.Contains(out, "is not running") {
		t.Errorf("output = %q, want not-running message", out)
	}
}

func TestRunStatusPropagatesCheckError(t *testing.T) {
	// A directory where a plain file is expected forces os.ReadFile to
	// fail with something other than IsNotExist.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	pidPath := filepath.Join(blocker, "shortner.pid")

	_, stderr, code := captureOutput(t, func() int { return runStatus("shortner", statusPaths(t, pidPath)) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if stderr == "" {
		t.Error("stderr = empty, want propagated CheckPIDFile error")
	}
}

func TestPrintPendingUpdate(t *testing.T) {
	p := statusPaths(t, filepath.Join(t.TempDir(), "shortner.pid"))

	// Nothing cached: --status stays silent about updates.
	out, _, _ := captureOutput(t, func() int { printPendingUpdate(p); return 0 })
	if out != "" {
		t.Errorf("output = %q, want silence when no update is pending", out)
	}

	// A cached version equal to the running one is not pending either.
	if err := updater.SaveState(updater.StatePath(p.Data), updater.State{
		Branch:           "stable",
		AvailableVersion: version.String(),
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	out, _, _ = captureOutput(t, func() int { printPendingUpdate(p); return 0 })
	if out != "" {
		t.Errorf("output = %q, want silence when the cached version is installed", out)
	}

	// A genuinely newer cached version is surfaced to the operator.
	if err := updater.SaveState(updater.StatePath(p.Data), updater.State{
		Branch:           "beta",
		AvailableVersion: "v99.0.0",
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	out, _, _ = captureOutput(t, func() int { printPendingUpdate(p); return 0 })
	if !strings.Contains(out, "Update available: ") || !strings.Contains(out, "v99.0.0") {
		t.Errorf("output = %q, want the pending-update notice", out)
	}
	if !strings.Contains(out, "channel beta") {
		t.Errorf("output = %q, want the channel name", out)
	}
}
