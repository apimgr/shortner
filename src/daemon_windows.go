//go:build windows

// Daemonize (Windows): Windows has no traditional Unix daemonization;
// --daemon is ignored with a warning and the process continues in the
// foreground. Use --service --install / --service start for a real
// background service (Windows SCM). See AI.md PART 8 "Daemonization
// (Windows)".
package main

import (
	"fmt"
	"os"
)

// Daemonize warns that --daemon is unsupported on Windows and continues
// in the foreground.
func Daemonize() error {
	fmt.Fprintln(os.Stderr, "Warning: --daemon is not supported on Windows")
	fmt.Fprintln(os.Stderr, "Use --service --install && --service start for Windows Service")
	return nil
}
