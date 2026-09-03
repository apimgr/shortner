package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatePath(t *testing.T) {
	if got := StatePath("/var/lib/apimgr/shortner"); got != "/var/lib/apimgr/shortner/update.json" {
		t.Errorf("StatePath = %q, want the data-dir update.json", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := StatePath(t.TempDir())
	want := State{
		Branch:           BranchBeta,
		CheckedAt:        time.Date(2026, 8, 19, 6, 0, 0, 0, time.UTC),
		AvailableVersion: "v1.2.3",
		NotifiedKey:      "v1.2.3",
	}
	if err := SaveState(path, want); err != nil {
		t.Fatalf("SaveState error = %v", err)
	}

	got := LoadState(path)
	if got != want {
		t.Errorf("LoadState = %+v, want %+v", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The state file is operator-only information; it never needs to be
	// world-readable.
	if perm := info.Mode().Perm(); perm != 0o640 {
		t.Errorf("mode = %o, want 640", perm)
	}
}

func TestSaveStateCreatesDataDir(t *testing.T) {
	path := StatePath(filepath.Join(t.TempDir(), "nested", "data"))
	if err := SaveState(path, State{Branch: BranchStable}); err != nil {
		t.Fatalf("SaveState error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file not created: %v", err)
	}
}

func TestLoadStateTolerantOfMissingAndCorruptFiles(t *testing.T) {
	dir := t.TempDir()

	// The state is a cache, not a source of truth: neither a missing nor a
	// truncated file may break a status check or a scheduler run.
	if got := LoadState(StatePath(dir)); got != (State{}) {
		t.Errorf("LoadState(missing) = %+v, want the zero State", got)
	}

	path := StatePath(dir)
	if err := os.WriteFile(path, []byte(`{"branch": "beta"`), 0o640); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := LoadState(path); got != (State{}) {
		t.Errorf("LoadState(corrupt) = %+v, want the zero State", got)
	}
}
