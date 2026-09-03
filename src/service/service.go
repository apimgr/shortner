// Package service implements privilege escalation detection, system
// service-account management, and service-manager installation for every
// platform this project targets. See AI.md PART 23 "PRIVILEGE ESCALATION &
// SERVICE" and PART 24 "SERVICE SUPPORT".
//
// The package deliberately owns only service *files* and service-manager
// commands: user/group creation, directory creation, ownership, and the
// privilege drop happen during NORMAL server startup, per PART 23
// "Service Installation Logic" ("The --service --install flag ONLY
// installs and starts the service").
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/apimgr/shortner/src/common/pidfile"
)

// Params carries every value the service templates and manager commands
// need. All of it is resolved by the caller from IDEA.md's frozen project
// variables and src/paths, so this package never guesses a path.
type Params struct {
	// InternalName is the frozen on-disk identifier (unit name, plist
	// suffix, service account name).
	InternalName string
	// InternalOrg is the frozen on-disk org identifier.
	InternalOrg string
	// ProjectName is the user-facing binary name.
	ProjectName string
	// ProjectOrg is the user-facing org name, used for Documentation=.
	ProjectOrg string
	// AppName is the human-readable application name.
	AppName string
	// BinaryPath is the absolute path the service manager will execute.
	BinaryPath string

	ConfigDir string
	DataDir   string
	CacheDir  string
	LogDir    string
	BackupDir string
	PIDFile   string

	// UserLevel selects the unprivileged fallback (systemd --user,
	// launchd LaunchAgent) described in PART 23 "Service Installation
	// Logic" step 2.
	UserLevel bool
}

// PlistName is the macOS bundle identifier, always derived and never
// stored — AI.md PART 0: `io.github.{project_org}.{internal_name}`.
func (p Params) PlistName() string {
	return "io.github." + p.ProjectOrg + "." + p.InternalName
}

// Status is the live service state reported by `--service --help`
// (AI.md PART 23 "Service Help Output").
type Status struct {
	Installed bool
	// State is "running", "stopped", or "disabled".
	State string
	// AutoStart reports whether the service starts at boot.
	AutoStart bool
	// PID is the main process ID, or 0 when not running.
	PID int
}

// Manager is a single platform service manager.
type Manager interface {
	// Name is the service manager's identifier (systemd, openrc, ...).
	Name() string
	// Files lists every path this manager owns for the service.
	Files() []string
	// Install writes the service definition files.
	Install() error
	// Remove deletes the service definition files.
	Remove() error
	// Enable turns on auto-start at boot.
	Enable() error
	// Disable turns off auto-start at boot.
	Disable() error
	Start() error
	Stop() error
	Restart() error
	// Reload asks the service to re-read its configuration without a
	// full restart.
	Reload() error
	// Status reports the live state; it never returns an error because
	// "not installed" is a valid answer on every platform.
	Status() Status
}

// ErrUnsupported is returned when no service manager could be detected
// for the running platform.
var ErrUnsupported = fmt.Errorf("service: no supported service manager detected on this system")

// New returns the Manager for the detected init system.
func New(p Params) (Manager, error) {
	return newForInit(Detect(), p)
}

// newForInit builds the Manager for an explicitly chosen init system. It
// exists so the detection rules and the manager construction can be
// tested independently.
func newForInit(init InitSystem, p Params) (Manager, error) {
	switch init {
	case Systemd:
		return &systemdManager{p: p}, nil
	case OpenRC:
		return &openrcManager{p: p}, nil
	case SysVinit:
		return &sysvinitManager{p: p}, nil
	case Runit:
		return &runitManager{p: p}, nil
	case RCd:
		return &rcdManager{p: p}, nil
	case Launchd:
		return &launchdManager{p: p}, nil
	case WindowsService:
		return newWindowsManager(p)
	default:
		return nil, ErrUnsupported
	}
}

// Install performs PART 23 "Service Installation Logic": write the
// service file, enable auto-start, then start the service.
func Install(m Manager) error {
	if err := m.Install(); err != nil {
		return err
	}
	if err := m.Enable(); err != nil {
		return err
	}
	return m.Start()
}

// Disable performs PART 23 "Service Disable Logic": stop the service and
// remove it from auto-start, keeping the service file, data, and user.
func Disable(m Manager) error {
	// A stop failure on an already-stopped service must not abort the
	// disable — the end state is what matters.
	_ = m.Stop()
	return m.Disable()
}

// PurgeResult records what an uninstall actually removed, so the CLI can
// report it truthfully instead of claiming more than it did.
type PurgeResult struct {
	RemovedPaths []string
	RemovedUser  bool
	UserName     string
}

// Uninstall performs PART 23 "Service Uninstall Logic" steps 1-5: stop,
// disable, remove the service file, delete every data directory and the
// PID file, then delete the system user/group when this binary created
// it. Step 6 (the "delete binary manually" message) belongs to the CLI.
//
// The caller MUST have obtained the interactive confirmation required by
// PART 23 before calling this.
func Uninstall(m Manager, p Params) (PurgeResult, error) {
	var result PurgeResult

	_ = m.Stop()
	_ = m.Disable()
	if err := m.Remove(); err != nil {
		return result, err
	}
	result.RemovedPaths = append(result.RemovedPaths, m.Files()...)

	// The ownership marker lives inside the data directory, so it has to
	// be read before anything is deleted.
	owned := ownsServiceAccount(p)

	for _, dir := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.LogDir, p.BackupDir} {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return result, fmt.Errorf("service: remove %s: %w", dir, err)
		}
		result.RemovedPaths = append(result.RemovedPaths, dir)
	}
	if p.PIDFile != "" {
		if err := os.Remove(p.PIDFile); err == nil {
			result.RemovedPaths = append(result.RemovedPaths, p.PIDFile)
		}
	}

	if owned {
		if err := DeleteServiceAccount(p.InternalName); err != nil {
			return result, err
		}
		result.RemovedUser = true
		result.UserName = p.InternalName
	}

	return result, nil
}

// writeFile writes a service definition file, creating its parent
// directory first.
func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("service: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("service: write %s: %w", path, err)
	}
	return nil
}

// removeFiles deletes every path, ignoring the ones that are already gone.
func removeFiles(paths ...string) error {
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove %s: %w", path, err)
		}
	}
	return nil
}

// run executes a service-manager command, folding its output into the
// error so operators see why an action failed.
var run = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("service: %s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("service: %s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return nil
}

// runOutput executes a query command and returns its trimmed stdout. A
// non-zero exit is not an error here: `systemctl is-active` and friends
// report state through the exit code and still print a usable answer.
var runOutput = func(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// fileExists reports whether path exists (of any type).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// pidFromFile reports the PID recorded in the PID file when that process
// is actually alive. It is the fallback for managers that do not expose a
// main PID of their own. Liveness is delegated to src/common/pidfile so
// there is a single stale-PID implementation in the project.
func pidFromFile(path string) int {
	if path == "" {
		return 0
	}
	running, pid, err := pidfile.CheckPIDFile(path)
	if err != nil || !running {
		return 0
	}
	return pid
}
