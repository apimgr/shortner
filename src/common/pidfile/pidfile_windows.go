//go:build windows

// See AI.md PART 8 "PID File Handling" (process-existence checks, Windows).
package pidfile

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// isProcessRunning checks if a process with the given PID exists (Windows).
func isProcessRunning(pid int) bool {
	// On Windows, FindProcess succeeds for any valid PID.
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.Pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	// Try to get exit code - fails if the process doesn't exist or we lack
	// permission. For our own processes this should work.
	var exitCode uint32
	err = windows.GetExitCodeProcess(handle, &exitCode)
	return err == nil && exitCode == 259 // STILL_ACTIVE
}

// isOurProcess verifies the process is actually our binary (Windows).
func isOurProcess(pid int) bool {
	// Use the Windows API to get the process image name.
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)

	var buf [windows.MAX_PATH]uint16
	size := uint32(windows.MAX_PATH)
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return false
	}
	exePath := windows.UTF16ToString(buf[:size])
	// Exact match (case-insensitive) - substring matching would also match
	// "shortner-cli.exe".
	base := filepath.Base(exePath)
	return strings.EqualFold(base, binaryName+".exe") || strings.EqualFold(base, binaryName)
}
