package service

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnforcePrivatePaths fixes private directory and file modes before config
// reads and before any managed child process is started.
func EnforcePrivatePaths(p Params) error {
	privateDirs := []string{
		filepath.Join(p.ConfigDir, "yaml.d"), filepath.Join(p.ConfigDir, "ssl"),
		filepath.Join(p.ConfigDir, "tor"), filepath.Join(p.ConfigDir, "i2p"),
		filepath.Join(p.DataDir, "security"), filepath.Join(p.DataDir, "tor"),
		filepath.Join(p.DataDir, "i2p"),
	}
	for _, root := range privateDirs {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return fmt.Errorf("service: create private path %s: %w", root, err)
		}
		if err := chmodTree(root, 0o700, 0o600); err != nil {
			return err
		}
	}
	configFile := filepath.Join(p.ConfigDir, "server.yml")
	if info, err := os.Lstat(configFile); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("service: refusing symlink for private path %s", configFile)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("service: private path %s is not a regular file", configFile)
		}
		if err := os.Chmod(configFile, 0o600); err != nil {
			return fmt.Errorf("service: secure private path %s: %w", configFile, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("service: inspect private path %s: %w", configFile, err)
	}
	return nil
}

// EnforceRuntimeModes applies the normal root/user mode table, then tightens
// every PART 8 private subtree and the server config file.
func EnforceRuntimeModes(p Params, elevated bool) error {
	dirMode, fileMode := os.FileMode(0o700), os.FileMode(0o600)
	if elevated {
		dirMode, fileMode = 0o755, 0o644
	}
	for _, root := range runtimeRoots(p) {
		if root == "" {
			continue
		}
		if err := chmodTree(root, dirMode, fileMode); err != nil {
			return err
		}
	}
	return EnforcePrivatePaths(p)
}

func runtimeRoots(p Params) []string {
	return []string{p.ConfigDir, p.DataDir, p.DBDir, p.CacheDir, p.LogDir, p.BackupDir}
}

func ownTree(root string, uid, gid int) error {
	if root == "" {
		return nil
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("service: refusing symlink in runtime path %s", path)
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("service: set owner on %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("service: set runtime path ownership %s: %w", root, err)
	}
	return nil
}

func chmodTree(root string, dirMode, fileMode os.FileMode) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("service: refusing symlink in private path %s", path)
		}
		mode := fileMode
		if entry.IsDir() {
			mode = dirMode
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("service: secure private path %s: %w", path, err)
		}
		return nil
	})
}

// SetRuntimeOwnership restores service-account ownership for files created
// while startup still has elevated privileges.
func SetRuntimeOwnership(p Params, uid int) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if p.PIDFile != "" {
		info, err := os.Lstat(p.PIDFile)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			return fmt.Errorf("service: refusing non-regular PID file %s", p.PIDFile)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("service: inspect PID file %s: %w", p.PIDFile, err)
		}
	}
	if err := EnforcePrivatePaths(p); err != nil {
		return err
	}
	for _, root := range runtimeRoots(p) {
		if root == "" {
			continue
		}
		if err := ownTree(root, uid, uid); err != nil {
			return err
		}
	}
	if err := ownPIDParent(p, uid); err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(p.ConfigDir, "server.yml"), p.PIDFile} {
		if path == "" {
			continue
		}
		info, err := os.Lstat(path)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("service: refusing symlink for runtime file %s", path)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("service: inspect runtime file %s: %w", path, err)
		}
		if err := os.Chown(path, uid, uid); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("service: set owner on %s: %w", path, err)
		}
	}
	return EnforceRuntimeModes(p, true)
}

// ownPIDParent makes the configured PID directory removable by the dropped
// service account without recursively changing a caller-selected directory.
// Parents inside managed runtime roots are already owned by that account.
// Outside those roots, only an empty dedicated directory is safe to transfer.
func ownPIDParent(p Params, uid int) error {
	if p.PIDFile == "" {
		return nil
	}
	parent := filepath.Clean(filepath.Dir(p.PIDFile))
	for _, root := range runtimeRoots(p) {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), parent)
		if err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, "..") {
			return nil
		}
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("service: inspect PID directory %s: %w", parent, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("service: refusing non-directory PID path %s", parent)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return fmt.Errorf("service: inspect PID directory %s: %w", parent, err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(p.PIDFile) {
			return fmt.Errorf("service: refusing to change ownership of non-dedicated PID directory %s", parent)
		}
	}
	if err := os.Chown(parent, uid, uid); err != nil {
		return fmt.Errorf("service: set owner on PID directory %s: %w", parent, err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		return fmt.Errorf("service: secure PID directory %s: %w", parent, err)
	}
	return nil
}
