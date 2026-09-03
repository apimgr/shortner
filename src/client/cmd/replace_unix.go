//go:build !windows

package cmd

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// replaceBinary swaps the running client for the freshly downloaded one.
// On Unix a running executable can be renamed over, so the swap is a single
// atomic os.Rename within the install directory.
func replaceBinary(currentPath, newBinaryPath string) error {
	dir := filepath.Dir(currentPath)
	staged := filepath.Join(dir, ".update-"+filepath.Base(currentPath))

	if err := copyFile(newBinaryPath, staged, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged, currentPath); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

// copyFile writes src to dst with the given mode. The download lives in the
// system temp directory, which is often a different filesystem, so a plain
// rename across the boundary would fail.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// syscallExec replaces the current process image with the updated binary so
// the in-progress command continues on the new version.
func syscallExec(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
