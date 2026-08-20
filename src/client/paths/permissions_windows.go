//go:build windows

package paths

// setDirPermissions is a no-op on Windows — files created under %APPDATA%
// and %LOCALAPPDATA% already inherit a user-only ACL.
func setDirPermissions(path string) error {
	return nil
}

// setFilePermissions is a no-op on Windows for the same reason.
func setFilePermissions(path string) error {
	return nil
}
