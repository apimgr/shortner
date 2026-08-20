//go:build windows

package cmd

import (
	"io"
	"os"
	"os/exec"
)

// replaceBinary swaps the running client for the freshly downloaded one.
// Windows locks a running image, so the current binary is first renamed
// aside to ".old" and the replacement is written in its place; the leftover
// ".old" is removed on the next run.
func replaceBinary(currentPath, newBinaryPath string) error {
	oldPath := currentPath + ".old"
	_ = os.Remove(oldPath)

	if err := os.Rename(currentPath, oldPath); err != nil {
		return err
	}
	if err := copyFile(newBinaryPath, currentPath, 0o755); err != nil {
		_ = os.Rename(oldPath, currentPath)
		return err
	}
	return nil
}

// copyFile writes src to dst with the given mode.
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

// syscallExec has no Windows equivalent of exec(2), so the updated binary is
// run as a child process with the original argv and its exit code adopted.
func syscallExec(path string, args, env []string) error {
	cmd := exec.Command(path, args[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
