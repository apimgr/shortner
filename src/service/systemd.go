// systemd manager. See AI.md PART 24 "systemd (Linux)" for the unit
// template and installation path, and PART 23 "Service Installation
// Logic" for the root/user-level split.
package service

import (
	"os"
	"path/filepath"
	"strconv"
)

// systemdManager drives systemctl for either the system manager
// (/etc/systemd/system) or the calling user's manager (systemd --user).
type systemdManager struct {
	p Params
}

func (m *systemdManager) Name() string { return string(Systemd) }

// unitName is the systemd unit identifier, always the frozen
// {internal_name} so a binary rename never orphans the unit.
func (m *systemdManager) unitName() string { return m.p.InternalName + ".service" }

// unitPath is /etc/systemd/system/{internal_name}.service for a system
// install, or ~/.config/systemd/user/{internal_name}.service for the
// unprivileged fallback.
func (m *systemdManager) unitPath() string {
	if m.p.UserLevel {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(home, ".config", "systemd", "user", m.unitName())
	}
	return filepath.Join("/etc/systemd/system", m.unitName())
}

// ctl prefixes --user for a user-level install.
func (m *systemdManager) ctl(args ...string) []string {
	if m.p.UserLevel {
		return append([]string{"--user"}, args...)
	}
	return args
}

func (m *systemdManager) Files() []string { return []string{m.unitPath()} }

func (m *systemdManager) Install() error {
	if err := writeFile(m.unitPath(), systemdUnit(m.p), 0o644); err != nil {
		return err
	}
	return run("systemctl", m.ctl("daemon-reload")...)
}

func (m *systemdManager) Remove() error {
	if err := removeFiles(m.unitPath()); err != nil {
		return err
	}
	// A reload after removal is best effort: the unit file is already
	// gone, which is what uninstall promised.
	_ = run("systemctl", m.ctl("daemon-reload")...)
	return nil
}

func (m *systemdManager) Enable() error {
	return run("systemctl", m.ctl("enable", m.unitName())...)
}

func (m *systemdManager) Disable() error {
	return run("systemctl", m.ctl("disable", m.unitName())...)
}

func (m *systemdManager) Start() error {
	return run("systemctl", m.ctl("start", m.unitName())...)
}

func (m *systemdManager) Stop() error {
	return run("systemctl", m.ctl("stop", m.unitName())...)
}

func (m *systemdManager) Restart() error {
	return run("systemctl", m.ctl("restart", m.unitName())...)
}

// Reload sends SIGHUP to the main process instead of `systemctl reload`:
// the unit is Type=simple with no ExecReload, and the binary's own SIGHUP
// handler is what re-reads the configuration without a restart.
func (m *systemdManager) Reload() error {
	return run("systemctl", m.ctl("kill", "-s", "HUP", m.unitName())...)
}

func (m *systemdManager) Status() Status {
	st := Status{Installed: fileExists(m.unitPath()), State: "stopped"}
	if !st.Installed {
		return st
	}

	enabled, _ := runOutput("systemctl", m.ctl("is-enabled", m.unitName())...)
	st.AutoStart = enabled == "enabled" || enabled == "enabled-runtime" || enabled == "static"

	active, _ := runOutput("systemctl", m.ctl("is-active", m.unitName())...)
	switch {
	case active == "active" || active == "activating" || active == "reloading":
		st.State = "running"
	case !st.AutoStart:
		st.State = "disabled"
	}

	if st.State == "running" {
		if out, err := runOutput("systemctl", m.ctl("show", "-p", "MainPID", "--value", m.unitName())...); err == nil {
			if pid, convErr := strconv.Atoi(out); convErr == nil && pid > 0 {
				st.PID = pid
			}
		}
		if st.PID == 0 {
			st.PID = pidFromFile(m.p.PIDFile)
		}
	}
	return st
}
