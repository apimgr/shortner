package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/common/pidfile"
)

func TestRunStatusNotRunning(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "does-not-exist.pid")

	out, _, code := captureOutput(t, func() int { return runStatus("shortner", pidPath) })
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
	out, _, code := captureOutput(t, func() int { return runStatus("shortner", pidPath) })
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

	_, stderr, code := captureOutput(t, func() int { return runStatus("shortner", pidPath) })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if stderr == "" {
		t.Error("stderr = empty, want propagated CheckPIDFile error")
	}
}
