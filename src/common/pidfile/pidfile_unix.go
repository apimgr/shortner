//go:build !windows

// See AI.md PART 8 "PID File Handling" (process-existence checks, Unix).
package pidfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// isProcessRunning checks if a process with the given PID exists (Unix).
func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds - need to send signal 0.
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but belongs to another user - it IS running.
	return errors.Is(err, syscall.EPERM)
}

// isOurProcess verifies the process is actually our binary (Unix).
func isOurProcess(pid int) bool {
	// Read /proc/{pid}/exe symlink (Linux).
	exePath, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		// On macOS/BSD, use ps command.
		return isOurProcessDarwin(pid)
	}
	// Exact match - substring matching would also match "shortner-cli".
	return filepath.Base(exePath) == binaryName
}

// isOurProcessDarwin checks the process name on macOS/BSD.
func isOurProcessDarwin(pid int) bool {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	// Exact match - substring matching would also match "shortner-cli".
	return strings.TrimSpace(string(output)) == binaryName
}
