//go:build windows

// See AI.md PART 7 "Display Environment Detection" (Platform-Specific
// Display Detection, Windows).
package display

import (
	"os"

	"golang.org/x/sys/windows"
)

// detectPlatformDisplay performs Windows display detection.
func (e *DisplayEnv) detectPlatformDisplay() {
	// Windows always has a display unless running as a service.
	// Check if we're running as a Windows service (no interactive desktop).

	// Method 1: Check if we have a console window.
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	hwnd, _, _ := getConsoleWindow.Call()

	// Method 2: Check if we're in session 0 (service session).
	var sessionID uint32
	windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sessionID)

	if sessionID == 0 {
		// Running as a service (session 0) - no interactive desktop.
		e.HasDisplay = false
		e.DisplayType = "none"
		return
	}

	// Check for remote desktop session.
	if os.Getenv("SESSIONNAME") == "RDP-Tcp#0" || os.Getenv("SESSIONNAME") != "" {
		// Remote desktop - has display but may want different behavior.
		e.HasDisplay = true
		e.DisplayType = "windows-rdp"
		return
	}

	// Normal Windows session with display.
	e.HasDisplay = hwnd != 0
	if e.HasDisplay {
		e.DisplayType = "windows"
	} else {
		e.DisplayType = "none"
	}
}
