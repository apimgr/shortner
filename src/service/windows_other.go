//go:build !windows

// The Windows Service manager only exists on Windows; on every other
// platform asking for it is a detection bug, so it fails loudly rather
// than silently returning a no-op manager.
package service

import "fmt"

// newWindowsManager reports that the Windows Service Control Manager is
// unavailable on this platform.
func newWindowsManager(p Params) (Manager, error) {
	return nil, fmt.Errorf("service: the Windows Service Control Manager is not available on this platform")
}
