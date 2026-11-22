//go:build windows

package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

// replaceBinary replaces the running binary (Windows).
// Windows cannot delete or overwrite a running executable, so the running
// binary is renamed to .old, the new one is moved into its place, and the
// .old file is scheduled for deletion on the next reboot.
func replaceBinary(currentPath, newBinaryPath string) error {
	oldPath := currentPath + ".old"

	// A leftover .old from a previous update would block the rename.
	if err := os.Remove(oldPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("updater: remove previous old binary: %w", err)
	}

	if err := os.Rename(currentPath, oldPath); err != nil {
		return fmt.Errorf("updater: rename current binary: %w", err)
	}

	if err := os.Rename(newBinaryPath, currentPath); err != nil {
		// Put the original back so the service still has a binary.
		if restoreErr := os.Rename(oldPath, currentPath); restoreErr != nil {
			return fmt.Errorf("updater: move new binary: %w (restore original: %v)", err, restoreErr)
		}
		return fmt.Errorf("updater: move new binary: %w", err)
	}

	// MOVEFILE_DELAY_UNTIL_REBOOT with a nil destination deletes the file
	// on the next boot, once nothing has it mapped any more.
	oldPathPtr, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return fmt.Errorf("updater: prepare old binary cleanup path: %w", err)
	}
	if err := windows.MoveFileEx(oldPathPtr, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); err != nil {
		fmt.Fprintf(os.Stderr, "shortner: WARNING: update installed, but remove %s after restart; automatic cleanup could not be scheduled: %v\n", oldPath, err)
	}

	return nil
}

// restartSelf starts a new instance and exits (Windows), because Windows
// has no exec() that replaces the running process image.
func restartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("updater: start new process: %w", err)
	}

	// Give the new process a moment to come up before this one goes away.
	time.Sleep(100 * time.Millisecond)

	os.Exit(0)
	// Unreachable; os.Exit does not return.
	return nil
}

// underServiceManager reports whether this process is running under the
// Windows Service Control Manager.
func underServiceManager() bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false
	}
	return isService
}
