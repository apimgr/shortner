//go:build !windows

// See AI.md PART 7 "Display Environment Detection" (Platform-Specific
// Display Detection, Unix/macOS).
package display

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// detectPlatformDisplay performs Unix/macOS display detection.
func (e *DisplayEnv) detectPlatformDisplay() {
	// Check for Wayland first (preferred on Linux).
	if waylandDisplay := os.Getenv("WAYLAND_DISPLAY"); waylandDisplay != "" {
		e.HasDisplay = true
		e.DisplayType = "wayland"
		return
	}

	// Check for X11.
	if display := os.Getenv("DISPLAY"); display != "" {
		e.HasDisplay = true
		e.DisplayType = "x11"
		return
	}

	// macOS: check if we have access to WindowServer.
	if runtime.GOOS == "darwin" {
		// On macOS, display is always available unless:
		// - Running over SSH
		// - Running as a LaunchDaemon (no GUI session)
		if !e.IsSSH && os.Getenv("__CFBundleIdentifier") != "" {
			e.HasDisplay = true
			e.DisplayType = "macos"
			return
		}
		// Check if WindowServer is accessible.
		cmd := exec.Command("launchctl", "managername")
		if output, err := cmd.Output(); err == nil {
			if strings.Contains(string(output), "Aqua") {
				e.HasDisplay = true
				e.DisplayType = "macos"
				return
			}
		}
	}

	e.HasDisplay = false
	e.DisplayType = "none"
}
