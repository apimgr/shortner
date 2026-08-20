//go:build !windows

package paths

import "os"

// setDirPermissions enforces 0700 on a client directory.
func setDirPermissions(path string) error {
	return os.Chmod(path, 0o700)
}

// setFilePermissions enforces 0600 on a client file — cli.yml carries the
// API token, so AI.md PART 32 requires user-only access.
func setFilePermissions(path string) error {
	return os.Chmod(path, 0o600)
}
