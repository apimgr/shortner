// Package paths resolves the client's user-scope directories. AI.md PART 32
// "CLI Directory Structure" forbids the client from ever touching a system
// location (/etc, /var/lib, /var/log, C:\ProgramData) — every path below is
// rooted in the invoking user's home or profile.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Paths holds every directory and file the client uses.
type Paths struct {
	// ConfigDir holds cli.yml and any named config profiles.
	ConfigDir string
	// DataDir holds persistent client state.
	DataDir string
	// CacheDir holds disposable cached responses.
	CacheDir string
	// LogDir holds cli.log.
	LogDir string
	// ConfigFile is the resolved config file path.
	ConfigFile string
	// LogFile is the resolved log file path.
	LogFile string
}

// Resolve returns the client paths for the given organization and internal
// name, honoring the platform's user-scope conventions. configName is the
// value of --config ("" means the default cli.yml).
func Resolve(org, name, configName string) Paths {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	var p Paths
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		p.ConfigDir = filepath.Join(appData, org, name)
		p.DataDir = filepath.Join(localAppData, org, name, "data")
		p.CacheDir = filepath.Join(localAppData, org, name, "cache")
		p.LogDir = filepath.Join(localAppData, org, name, "log")
	} else {
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		cacheHome := os.Getenv("XDG_CACHE_HOME")
		if cacheHome == "" {
			cacheHome = filepath.Join(home, ".cache")
		}
		p.ConfigDir = filepath.Join(configHome, org, name)
		p.DataDir = filepath.Join(dataHome, org, name)
		p.CacheDir = filepath.Join(cacheHome, org, name)
		p.LogDir = filepath.Join(home, ".local", "log", org, name)
	}

	p.LogFile = filepath.Join(p.LogDir, "cli.log")
	p.ConfigFile = ResolveConfigPath(configName, p.ConfigDir)
	return p
}

// ResolveConfigPath turns a --config value into an absolute file path.
// A bare name becomes {configDir}/{name}.yml; a name that already carries a
// .yml/.yaml extension is used as-is; an absolute or ~-rooted path is
// expanded and used verbatim. An empty name selects the default cli.yml.
func ResolveConfigPath(configName, configDir string) string {
	if configName == "" {
		return filepath.Join(configDir, "cli.yml")
	}

	if strings.HasPrefix(configName, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			configName = filepath.Join(home, strings.TrimPrefix(configName, "~"))
		}
	}

	if filepath.IsAbs(configName) || strings.ContainsRune(configName, os.PathSeparator) {
		return resolveYamlExtension(configName)
	}

	return resolveYamlExtension(filepath.Join(configDir, configName))
}

// resolveYamlExtension appends the correct YAML extension to a path that has
// none. An existing .yaml file wins over the .yml default; otherwise .yml.
func resolveYamlExtension(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".yml" || ext == ".yaml" {
		return path
	}
	if _, err := os.Stat(path + ".yml"); err == nil {
		return path + ".yml"
	}
	if _, err := os.Stat(path + ".yaml"); err == nil {
		return path + ".yaml"
	}
	return path + ".yml"
}

// EnsureDirs creates every client directory with user-only permissions.
func (p Paths) EnsureDirs() error {
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.LogDir, filepath.Dir(p.ConfigFile)} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := setDirPermissions(dir); err != nil {
			return err
		}
	}
	return nil
}

// SecureFile applies user-only permissions to a file the client wrote.
func SecureFile(path string) error {
	return setFilePermissions(path)
}
