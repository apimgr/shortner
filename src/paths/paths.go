// Package paths resolves OS- and privilege-specific runtime directories.
// See AI.md PART 4 for the authoritative path table.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/apimgr/shortner/src/common/pidfile"
)

// internalOrg and internalName are frozen project identifiers — see IDEA.md.
const (
	internalOrg  = "apimgr"
	internalName = "shortner"
	projectName  = "shortner"
)

// ProjectName is the user-facing project name used in the filenames this
// app creates outside its own directories — most notably the backup
// archive names in AI.md PART 21 ("{project_name}_backup_YYYY-MM-DD...").
const ProjectName = projectName

// InternalName and InternalOrg are the frozen on-disk identifiers,
// exported so callers that must name the same identity outside a path —
// the systemd unit, the launchd label, the service account (AI.md PART 23
// / PART 24) — reuse these values instead of re-declaring them.
const (
	InternalName = internalName
	InternalOrg  = internalOrg
)

// Paths holds every resolved runtime directory/file for the current OS and
// privilege level.
type Paths struct {
	Binary     string
	Config     string
	ConfigFile string
	Data       string
	Cache      string
	Logs       string
	LogFile    string
	Backup     string
	PIDFile    string
	SSL        string
	Security   string
	DB         string
}

// IsPrivileged reports whether the process is running as root (Unix) or an
// administrator-equivalent context (Windows is handled separately by callers).
func IsPrivileged() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return os.Geteuid() == 0
}

// Resolve returns the Paths for the current OS and privilege level.
// A non-empty envOverride forces the Docker-style /config and /data layout,
// used when the CONTAINER environment variable is set.
func Resolve(inContainer bool) Paths {
	if inContainer {
		return dockerPaths()
	}

	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "windows":
		return windowsPaths(home)
	case "darwin":
		return darwinPaths(home)
	case "freebsd", "openbsd", "netbsd":
		return bsdPaths(home)
	default:
		return linuxPaths(home)
	}
}

func dockerPaths() Paths {
	return Paths{
		Binary:     "/usr/local/bin/" + projectName,
		Config:     "/config/" + projectName,
		ConfigFile: filepath.Join("/config", projectName, "server.yml"),
		Data:       "/data/" + projectName,
		Cache:      filepath.Join("/data", projectName, "cache"),
		Logs:       filepath.Join("/data", "log", projectName),
		LogFile:    filepath.Join("/data", "log", projectName, "server.log"),
		Backup:     filepath.Join("/data", "backups", projectName),
		SSL:        filepath.Join("/config", projectName, "ssl"),
		Security:   filepath.Join("/data", projectName, "security"),
		DB:         filepath.Join("/data", "db", "sqlite"),
	}
}

func linuxPaths(home string) Paths {
	if IsPrivileged() {
		return Paths{
			Binary:     "/usr/local/bin/" + projectName,
			Config:     filepath.Join("/etc", internalOrg, internalName),
			ConfigFile: filepath.Join("/etc", internalOrg, internalName, "server.yml"),
			Data:       filepath.Join("/var/lib", internalOrg, internalName),
			Cache:      filepath.Join("/var/cache", internalOrg, internalName),
			Logs:       filepath.Join("/var/log", internalOrg, internalName),
			LogFile:    filepath.Join("/var/log", internalOrg, internalName, "server.log"),
			Backup:     filepath.Join("/mnt/Backups", internalOrg, internalName),
			PIDFile:    filepath.Join("/var/run", internalOrg, internalName+".pid"),
			SSL:        filepath.Join("/etc", internalOrg, internalName, "ssl"),
			Security:   filepath.Join("/var/lib", internalOrg, internalName, "security"),
			DB:         filepath.Join("/var/lib", internalOrg, internalName, "db"),
		}
	}
	return Paths{
		Binary:     filepath.Join(home, ".local/bin", projectName),
		Config:     filepath.Join(home, ".config", internalOrg, internalName),
		ConfigFile: filepath.Join(home, ".config", internalOrg, internalName, "server.yml"),
		Data:       filepath.Join(home, ".local/share", internalOrg, internalName),
		Cache:      filepath.Join(home, ".cache", internalOrg, internalName),
		Logs:       filepath.Join(home, ".local/log", internalOrg, internalName),
		LogFile:    filepath.Join(home, ".local/log", internalOrg, internalName, "server.log"),
		Backup:     filepath.Join(home, ".local/share/Backups", internalOrg, internalName),
		PIDFile:    filepath.Join(home, ".local/share", internalOrg, internalName, internalName+".pid"),
		SSL:        filepath.Join(home, ".config", internalOrg, internalName, "ssl"),
		Security:   filepath.Join(home, ".local/share", internalOrg, internalName, "security"),
		DB:         filepath.Join(home, ".local/share", internalOrg, internalName, "db"),
	}
}

func darwinPaths(home string) Paths {
	if IsPrivileged() {
		base := filepath.Join("/Library/Application Support", internalOrg, internalName)
		return Paths{
			Binary:     "/usr/local/bin/" + projectName,
			Config:     base,
			ConfigFile: filepath.Join(base, "server.yml"),
			Data:       filepath.Join(base, "data"),
			Cache:      filepath.Join("/Library/Caches", internalOrg, internalName),
			Logs:       filepath.Join("/Library/Logs", internalOrg, internalName),
			LogFile:    filepath.Join("/Library/Logs", internalOrg, internalName, "server.log"),
			Backup:     filepath.Join("/Library/Backups", internalOrg, internalName),
			PIDFile:    filepath.Join("/var/run", internalOrg, internalName+".pid"),
			SSL:        filepath.Join(base, "ssl"),
			Security:   filepath.Join(base, "data", "security"),
			DB:         filepath.Join(base, "db"),
		}
	}
	base := filepath.Join(home, "Library/Application Support", internalOrg, internalName)
	return Paths{
		Binary:     filepath.Join(home, "bin", projectName),
		Config:     base,
		ConfigFile: filepath.Join(base, "server.yml"),
		Data:       base,
		Cache:      filepath.Join(home, "Library/Caches", internalOrg, internalName),
		Logs:       filepath.Join(home, "Library/Logs", internalOrg, internalName),
		LogFile:    filepath.Join(home, "Library/Logs", internalOrg, internalName, "server.log"),
		Backup:     filepath.Join(home, "Library/Backups", internalOrg, internalName),
		PIDFile:    filepath.Join(base, internalName+".pid"),
		SSL:        filepath.Join(base, "ssl"),
		Security:   filepath.Join(base, "data", "security"),
		DB:         filepath.Join(base, "db"),
	}
}

func bsdPaths(home string) Paths {
	if IsPrivileged() {
		return Paths{
			Binary:     "/usr/local/bin/" + projectName,
			Config:     filepath.Join("/usr/local/etc", internalOrg, internalName),
			ConfigFile: filepath.Join("/usr/local/etc", internalOrg, internalName, "server.yml"),
			Data:       filepath.Join("/var/db", internalOrg, internalName),
			Cache:      filepath.Join("/var/cache", internalOrg, internalName),
			Logs:       filepath.Join("/var/log", internalOrg, internalName),
			LogFile:    filepath.Join("/var/log", internalOrg, internalName, "server.log"),
			Backup:     filepath.Join("/var/backups", internalOrg, internalName),
			PIDFile:    filepath.Join("/var/run", internalOrg, internalName+".pid"),
			SSL:        filepath.Join("/usr/local/etc", internalOrg, internalName, "ssl"),
			Security:   filepath.Join("/var/db", internalOrg, internalName, "security"),
			DB:         filepath.Join("/var/db", internalOrg, internalName, "db"),
		}
	}
	return linuxPaths(home)
}

func windowsPaths(home string) Paths {
	programData := os.Getenv("ProgramData")
	if IsAdministrator() && programData != "" {
		base := filepath.Join(programData, internalOrg, internalName)
		return Paths{
			Binary:     filepath.Join(`C:\Program Files`, internalOrg, internalName, projectName+".exe"),
			Config:     base,
			ConfigFile: filepath.Join(base, "server.yml"),
			Data:       filepath.Join(base, "data"),
			Cache:      filepath.Join(base, "cache"),
			Logs:       filepath.Join(base, "logs"),
			LogFile:    filepath.Join(base, "logs", "server.log"),
			Backup:     filepath.Join(programData, "Backups", internalOrg, internalName),
			PIDFile:    filepath.Join(base, internalName+".pid"),
			SSL:        filepath.Join(base, "ssl"),
			Security:   filepath.Join(base, "data", "security"),
			DB:         filepath.Join(base, "db"),
		}
	}
	appData := os.Getenv("AppData")
	localAppData := os.Getenv("LocalAppData")
	base := filepath.Join(appData, internalOrg, internalName)
	localBase := filepath.Join(localAppData, internalOrg, internalName)
	return Paths{
		Binary:     filepath.Join(localAppData, internalOrg, internalName, projectName+".exe"),
		Config:     base,
		ConfigFile: filepath.Join(base, "server.yml"),
		Data:       localBase,
		Cache:      filepath.Join(localBase, "cache"),
		Logs:       filepath.Join(localBase, "logs"),
		LogFile:    filepath.Join(localBase, "logs", "server.log"),
		Backup:     filepath.Join(localAppData, "Backups", internalOrg, internalName),
		PIDFile:    filepath.Join(localBase, internalName+".pid"),
		SSL:        filepath.Join(base, "ssl"),
		Security:   filepath.Join(localBase, "security"),
		DB:         filepath.Join(localBase, "db"),
	}
}

// IsAdministrator reports whether the process has Windows Administrator
// privileges. On non-Windows platforms it always returns false. Windows
// admin-token detection is tracked in TODO.AI.md (PART 4).
func IsAdministrator() bool {
	return runtime.GOOS == "windows" && windowsIsAdministrator()
}

// startedElevated is captured once at process start, before any future
// privilege drop (server startup step 8g in AI.md PART 8 — not yet
// implemented; tracked in TODO.AI.md). Directory mode (system vs user)
// must never be re-derived after that drop: the service account's HOME
// points at the data dir, so a late $HOME lookup would nest user-style
// paths inside it.
var startedElevated = IsPrivileged()

// EnsureDir creates path (and any missing parents) with the permission
// appropriate to the current privilege level, then verifies it is
// writable. See AI.md PART 8 "Directory Flags" / "Directory Validation
// Rules".
func EnsureDir(path string) error {
	perm := os.FileMode(0o700)
	if IsPrivileged() {
		perm = 0o755
	}

	if err := os.MkdirAll(path, perm); err != nil {
		return fmt.Errorf("paths: create directory %s: %w", path, err)
	}

	testFile := filepath.Join(path, ".write-test")
	if err := os.WriteFile(testFile, []byte{}, 0o600); err != nil {
		return fmt.Errorf("paths: directory %s is not writable: %w", path, err)
	}
	os.Remove(testFile)

	return nil
}

// EnsurePIDFile creates the PID file's parent directory with the correct
// permissions. See AI.md PART 8 "Directory Flags".
func EnsurePIDFile(path string) error {
	return EnsureDir(filepath.Dir(path))
}

// firstNonEmpty returns the first non-empty value among the CLI flag,
// environment variable, and computed default, in that priority order.
// See AI.md PART 8 "Environment Variable Fallbacks".
func firstNonEmpty(flagValue, envKey, defaultValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return defaultValue
}

// GetConfigDir returns the config directory from --config, CONFIG_DIR, or
// the OS/privilege default.
func GetConfigDir(flagValue, defaultDir string) string {
	return firstNonEmpty(flagValue, "CONFIG_DIR", defaultDir)
}

// GetDataDir returns the data directory from --data, DATA_DIR, or the
// OS/privilege default.
func GetDataDir(flagValue, defaultDir string) string {
	return firstNonEmpty(flagValue, "DATA_DIR", defaultDir)
}

// GetCacheDir returns the cache directory from --cache, CACHE_DIR, or the
// OS/privilege default.
func GetCacheDir(flagValue, defaultDir string) string {
	return firstNonEmpty(flagValue, "CACHE_DIR", defaultDir)
}

// GetLogDir returns the log directory from --log, LOG_DIR, or the
// OS/privilege default.
func GetLogDir(flagValue, defaultDir string) string {
	return firstNonEmpty(flagValue, "LOG_DIR", defaultDir)
}

// GetPIDFile returns the PID file path from --pid, PID_FILE, or the
// OS/privilege default.
func GetPIDFile(flagValue, defaultFile string) string {
	return firstNonEmpty(flagValue, "PID_FILE", defaultFile)
}

// GetDatabaseDir returns the SQLite database directory. Docker uses a
// separate /data/db/sqlite directory; native installs use {data_dir}/db/.
// See AI.md PART 8 "Environment Variable Fallbacks".
func GetDatabaseDir(dataDir string) string {
	if v := os.Getenv("DATABASE_DIR"); v != "" {
		return v
	}
	if pidfile.IsContainer() {
		return "/data/db/sqlite"
	}
	return filepath.Join(dataDir, "db")
}

// GetBackupDir returns the backup directory from --backup, BACKUP_DIR, or
// the system backup dir if writable, else a mode-aware fallback: system
// mode falls back inside dataDir (never a $HOME-derived path — the
// service account's HOME points at dataDir); user mode falls back to the
// user backup dir. See AI.md PART 8 "Environment Variable Fallbacks".
func GetBackupDir(flagValue, dataDir string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("BACKUP_DIR"); v != "" {
		return v
	}

	sysBackup := systemBackupDir()
	if isWritable(sysBackup) {
		return sysBackup
	}
	if startedElevated {
		return filepath.Join(dataDir, "backup")
	}
	return userBackupDir()
}

// isWritable reports whether path's parent directory exists and can be
// written to (checked by creating and removing a probe file).
func isWritable(path string) bool {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return false
	}

	testFile := filepath.Join(parent, ".write_test_"+strconv.FormatInt(time.Now().UnixNano(), 36))
	f, err := os.Create(testFile)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(testFile)
	return true
}

// systemBackupDir returns the system-level backup directory for the
// current OS.
func systemBackupDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join("/Library/Backups", internalOrg, internalName)
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), "Backups", internalOrg, internalName)
	case "freebsd", "openbsd", "netbsd":
		return filepath.Join("/var/backups", internalOrg, internalName)
	default:
		return filepath.Join("/mnt/Backups", internalOrg, internalName)
	}
}

// userBackupDir returns the user-level backup directory for the current
// OS. USER MODE ONLY — never call when startedElevated is true.
func userBackupDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library/Backups", internalOrg, internalName)
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Backups", internalOrg, internalName)
	default:
		return filepath.Join(home, ".local/share/Backups", internalOrg, internalName)
	}
}
