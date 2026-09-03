//go:build windows

package paths

import "os"

// windowsIsAdministrator reports whether the process token has elevated
// (Administrator) privileges, using the standard "open a privileged device"
// probe so the package stays dependency-free.
func windowsIsAdministrator() bool {
	f, err := os.Open(`\\.\PHYSICALDRIVE0`)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
