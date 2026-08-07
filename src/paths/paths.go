// Package paths resolves OS- and privilege-specific runtime directories.
// See AI.md PART 4 for the authoritative path table.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// internalOrg and internalName are frozen project identifiers — see IDEA.md.
const (
	internalOrg  = "apimgr"
	internalName = "shortner"
	projectName  = "shortner"
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
