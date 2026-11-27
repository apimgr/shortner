//go:build !windows

package paths

// EnforcePrivateACL is only needed for Windows system-mode paths.
func EnforcePrivateACL(string) error {
	return nil
}
