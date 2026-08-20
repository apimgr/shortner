//go:build windows

// Windows has no SIGHUP: the Service Control Manager delivers control
// codes instead, and the Windows manager implements Reload through
// svc.ParamChange rather than through this fallback.
package service

import "fmt"

// reloadViaSIGHUP is never the reload path on Windows; it exists so the
// shared manager code compiles for every target platform.
func reloadViaSIGHUP(p Params) error {
	return fmt.Errorf("service: reload by signal is not supported on Windows; use --service restart")
}
