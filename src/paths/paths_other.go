//go:build !windows

package paths

// windowsIsAdministrator is unused outside Windows builds.
func windowsIsAdministrator() bool {
	return false
}
