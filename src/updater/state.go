package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateFile is the name of the update state file kept in the data
// directory. It exists because AI.md PART 22 requires the "update
// available" notice to fire "once per version ... not re-sent on every
// task run", and to surface in `--status` output without the status check
// making a network call of its own.
const StateFile = "update.json"

// State is the persisted result of the most recent update check.
type State struct {
	// Branch is the channel the check ran against.
	Branch string `json:"branch"`
	// CheckedAt is when the check completed (UTC).
	CheckedAt time.Time `json:"checked_at"`
	// AvailableVersion is the version the check found, empty when the
	// running build is current.
	AvailableVersion string `json:"available_version,omitempty"`
	// NotifiedKey is Release.Key of the version the operator has already
	// been told about, so the WARN log fires once per version.
	NotifiedKey string `json:"notified_key,omitempty"`
}

// StatePath returns the update state file path inside dataDir.
func StatePath(dataDir string) string {
	return filepath.Join(dataDir, StateFile)
}

// LoadState reads path. A missing or unreadable file yields the zero
// State and no error: the state is a cache, never a source of truth, and
// losing it costs at most one duplicate notification.
func LoadState(path string) State {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	return s
}

// SaveState writes s to path atomically, so a crash mid-write cannot
// leave a truncated file behind.
func SaveState(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("updater: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, StateFile+".*")
	if err != nil {
		return fmt.Errorf("updater: create temp state: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("updater: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("updater: sync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("updater: close state: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o640); err != nil {
		return fmt.Errorf("updater: chmod state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("updater: replace state: %w", err)
	}
	return nil
}
