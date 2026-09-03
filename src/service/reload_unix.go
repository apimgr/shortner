//go:build !windows

// Unix reload: send SIGHUP to the running server so it re-reads its
// configuration in place. Used by the service managers whose init scripts
// have no reload verb of their own (SysVinit, launchd) and as the
// fallback for the ones whose reload verb is optional (OpenRC, rc.d).
package service

import (
	"fmt"
	"os"
	"syscall"
)

// reloadViaSIGHUP signals the PID recorded in the PID file.
func reloadViaSIGHUP(p Params) error {
	pid := pidFromFile(p.PIDFile)
	if pid == 0 {
		return fmt.Errorf("service: cannot reload: %s is not running", p.InternalName)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("service: cannot reload pid %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("service: cannot signal pid %d: %w", pid, err)
	}
	return nil
}
