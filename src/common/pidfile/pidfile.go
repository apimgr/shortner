// Package pidfile implements PID file creation, stale-PID detection, and
// cleanup shared by the server binary. See AI.md PART 8 "PID File
// Handling".
package pidfile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// binaryName is the exact process name used to verify PID ownership.
// Substring matching would also match "shortner-cli".
const binaryName = "shortner"

// CheckPIDFile checks whether pidPath exists and whether the process it
// names is still running. A stale or corrupt PID file is removed.
func CheckPIDFile(pidPath string) (isRunning bool, pid int, err error) {
	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		// No PID file, not running.
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("reading pid file: %w", err)
	}

	pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		// Corrupt PID file - remove it.
		os.Remove(pidPath)
		return false, 0, nil
	}

	// Check if process is running.
	if !isProcessRunning(pid) {
		// Stale PID file - remove it.
		os.Remove(pidPath)
		return false, 0, nil
	}

	// Process exists - verify it's actually our process (not PID reuse).
	if !isOurProcess(pid) {
		// PID was reused by another process - remove stale file.
		os.Remove(pidPath)
		return false, 0, nil
	}

	return true, pid, nil
}

// WritePIDFile writes the current process PID to pidPath. Inside a
// container, this is a no-op — the container runtime supervises the
// process, and PIDs are namespace-local, so a PID file on a mounted volume
// read from the host or another container points at the wrong process or
// produces a false "already running".
func WritePIDFile(pidPath string) error {
	if isContainer() {
		return nil
	}

	// Check for existing running instance first.
	running, existingPID, err := CheckPIDFile(pidPath)
	if err != nil {
		return err
	}
	if running {
		return fmt.Errorf("already running (pid %d)", existingPID)
	}

	// AI.md PART 8 "Permissions": root → directories 0755, PID 0644;
	// unprivileged user → directories 0700, PID 0600.
	dirPerm, filePerm := os.FileMode(0o700), os.FileMode(0o600)
	if os.Geteuid() == 0 {
		dirPerm, filePerm = 0o755, 0o644
	}

	if err := os.MkdirAll(filepath.Dir(pidPath), dirPerm); err != nil {
		return fmt.Errorf("creating pid file directory: %w", err)
	}

	// Write our PID.
	pid := os.Getpid()
	return os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), filePerm)
}

// RemovePIDFile removes the PID file on shutdown. Inside a container this
// is a no-op, matching WritePIDFile, which never creates one.
func RemovePIDFile(pidPath string) error {
	if isContainer() {
		return nil
	}
	err := os.Remove(pidPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsContainer reports whether the process is running inside a container.
// Exported so other packages (path resolution, service-manager detection)
// can reuse this check instead of reimplementing it. See AI.md PART 8
// "Service Manager Detection".
func IsContainer() bool {
	return isContainer()
}

// ParentProcessName returns the parent process's command name, exported
// for reuse by service-manager detection. See AI.md PART 8 "Service
// Manager Detection".
func ParentProcessName() string {
	return getParentProcessName()
}

// isContainer returns true if running inside a container. See AI.md
// PART 8 "Service Manager Detection".
func isContainer() bool {
	// File-based detection.
	containerFiles := []string{
		// Docker
		"/.dockerenv",
		// Podman
		"/run/.containerenv",
		// LXC/LXD/Incus
		"/dev/lxc",
	}
	for _, f := range containerFiles {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}

	// Environment variable detection.
	if os.Getenv("container") != "" {
		// Generic (systemd-nspawn, lxc, etc.)
		return true
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		// Kubernetes
		return true
	}

	// Check parent process name for container init systems.
	switch getParentProcessName() {
	case "tini", "dumb-init", "s6-svscan", "runsv", "runsvdir", "catatonit":
		return true
	case binaryName:
		// Parent is our own binary - likely container entrypoint.
		return true
	}

	// Check cgroup for container indicators.
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if strings.Contains(content, "docker") ||
			strings.Contains(content, "kubepods") ||
			strings.Contains(content, "lxc") {
			return true
		}
	}

	return false
}

// getParentProcessName returns the name of the parent process, used only
// by isContainer's init-system check. Container init detection is a Unix
// concept (Docker/Podman/LXC always run Linux init systems), so the
// Windows build simply reports no match.
func getParentProcessName() string {
	ppid := os.Getppid()

	// Linux: read /proc/{ppid}/comm.
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", ppid)); err == nil {
		return strings.TrimSpace(string(data))
	}

	// macOS/BSD: use ps command.
	cmd := exec.Command("ps", "-p", strconv.Itoa(ppid), "-o", "comm=")
	if output, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(output))
	}

	return ""
}
