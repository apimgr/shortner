//go:build !windows

package service

import (
	"fmt"
	"os"
	"syscall"
)

// DropPrivileges changes every Go runtime thread to the dedicated service
// identity after privileged setup has completed.
func DropPrivileges(uid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := syscall.Setgroups([]int{uid}); err != nil {
		return fmt.Errorf("service: set service group %d: %w", uid, err)
	}
	if err := syscall.Setgid(uid); err != nil {
		return fmt.Errorf("service: set service gid %d: %w", uid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("service: set service uid %d: %w", uid, err)
	}
	if os.Geteuid() == 0 || os.Getuid() == 0 {
		return fmt.Errorf("service: privilege drop to uid %d did not take effect", uid)
	}
	return nil
}
