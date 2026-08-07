package pidfile

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCheckPIDFileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.pid")

	running, pid, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile() error = %v, want nil", err)
	}
	if running {
		t.Error("running = true, want false")
	}
	if pid != 0 {
		t.Errorf("pid = %d, want 0", pid)
	}
}

func TestCheckPIDFileCorruptContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.pid")
	if err := os.WriteFile(path, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	running, pid, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile() error = %v, want nil", err)
	}
	if running || pid != 0 {
		t.Errorf("got running=%v pid=%d, want false/0", running, pid)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("corrupt PID file was not removed")
	}
}

func TestCheckPIDFileStalePID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.pid")
	// PID 999999 is well above any realistic live PID in a container/CI
	// process table.
	if err := os.WriteFile(path, []byte("999999"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	running, pid, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile() error = %v, want nil", err)
	}
	if running || pid != 0 {
		t.Errorf("got running=%v pid=%d, want false/0 for stale pid", running, pid)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("stale PID file was not removed")
	}
}

// TestCheckPIDFileRunningButNotOurProcess covers the "PID reused by
// another process" branch: the current test binary's own PID is a real,
// running process, but it is never named "shortner", so isOurProcess must
// return false and the file must be treated as stale.
func TestCheckPIDFileRunningButNotOurProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reused.pid")
	ownPID := os.Getpid()
	if err := os.WriteFile(path, []byte(strconv.Itoa(ownPID)), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	running, pid, err := CheckPIDFile(path)
	if err != nil {
		t.Fatalf("CheckPIDFile() error = %v, want nil", err)
	}
	if running || pid != 0 {
		t.Errorf("got running=%v pid=%d, want false/0 (PID belongs to test binary, not %q)", running, pid, binaryName)
	}
}

func TestWritePIDFileAndRemovePIDFile(t *testing.T) {
	if IsContainer() {
		t.Skip("WritePIDFile/RemovePIDFile are no-ops inside a container (see AI.md PART 8); nothing to assert")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "shortner.pid")

	if err := WritePIDFile(path); err != nil {
		t.Fatalf("WritePIDFile() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != strconv.Itoa(os.Getpid()) {
		t.Errorf("pid file content = %q, want %q", data, strconv.Itoa(os.Getpid()))
	}

	if err := RemovePIDFile(path); err != nil {
		t.Fatalf("RemovePIDFile() error = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Error("PID file still exists after RemovePIDFile")
	}

	// Removing an already-removed file is a no-op, not an error.
	if err := RemovePIDFile(path); err != nil {
		t.Errorf("RemovePIDFile() on missing file error = %v, want nil", err)
	}
}

func TestWritePIDFileAlreadyRunningInSelf(t *testing.T) {
	if IsContainer() {
		t.Skip("WritePIDFile is a no-op inside a container")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "shortner.pid")
	// Force CheckPIDFile's "running" branch: it can't actually match
	// binaryName in a test binary, so instead assert the base case: a
	// clean write followed by a second write with the same real PID
	// re-hits WritePIDFile's own not-a-fresh-file guard indirectly via
	// CheckPIDFile returning not-running (test binary isn't "shortner"),
	// meaning the second write succeeds rather than erroring. This
	// documents that behavior rather than asserting the unreachable
	// "already running" error path, which requires the real binary name.
	if err := WritePIDFile(path); err != nil {
		t.Fatalf("first WritePIDFile() error = %v", err)
	}
	if err := WritePIDFile(path); err != nil {
		t.Fatalf("second WritePIDFile() error = %v, want nil (stale file replaced)", err)
	}
	t.Cleanup(func() { RemovePIDFile(path) })
}

func TestIsContainerRunsWithoutPanic(t *testing.T) {
	// isContainer() depends on the runtime environment (Docker/Podman
	// markers, cgroups, parent process). We can't force a deterministic
	// answer without root/mount access, so this only asserts it runs
	// cleanly and returns a stable value across repeated calls.
	first := IsContainer()
	second := IsContainer()
	if first != second {
		t.Errorf("IsContainer() not stable across calls: %v then %v", first, second)
	}
}

func TestParentProcessNameRunsWithoutPanic(t *testing.T) {
	// Just verifies the call completes; the actual name is environment
	// dependent (init system, shell, or test runner parent).
	_ = ParentProcessName()
}
