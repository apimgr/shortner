//go:build !windows

package updater

import (
	"fmt"
	"os"
	"syscall"

	"github.com/apimgr/shortner/src/common/pidfile"
)

// replaceBinary replaces the running binary (Unix).
// Unix allows renaming over a running executable: the old image stays
// mapped in memory until the process exits, and the new one takes over on
// the next start.
func replaceBinary(currentPath, newBinaryPath string) error {
	info, err := os.Stat(currentPath)
	if err != nil {
		return fmt.Errorf("updater: stat current binary: %w", err)
	}

	if err := os.Rename(newBinaryPath, currentPath); err != nil {
		return fmt.Errorf("updater: replace binary: %w", err)
	}

	if err := os.Chmod(currentPath, info.Mode()); err != nil {
		return fmt.Errorf("updater: restore permissions: %w", err)
	}

	return nil
}

// restartSelf re-executes the current process (Unix), replacing the
// process image with the newly installed binary.
func restartSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}

// underServiceManager reports whether this process was started by systemd
// or launchd: both reparent the service to PID 1 and export a marker
// variable (INVOCATION_ID for systemd, XPC_SERVICE_NAME for launchd).
//
// Inside a container the reparenting test is meaningless — the entrypoint
// (or the app itself) is PID 1 with no service manager behind it — so the
// container case re-execs in place instead.
func underServiceManager() bool {
	if pidfile.IsContainer() {
		return false
	}
	if os.Getenv("INVOCATION_ID") != "" || os.Getenv("XPC_SERVICE_NAME") != "" {
		return true
	}
	return os.Getppid() == 1
}
