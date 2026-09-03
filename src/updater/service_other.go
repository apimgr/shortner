//go:build !linux && !darwin && !windows && !freebsd && !openbsd && !netbsd

package updater

import (
	"fmt"
	"runtime"
)

// restartService reports that this platform has no supported service
// manager, per the default branch of AI.md PART 22 "Service-Aware
// Update". The eight release targets never reach this file; it exists so
// `go build` stays honest on any other GOOS.
func restartService() error {
	return fmt.Errorf("updater: unsupported platform: %s", runtime.GOOS)
}
