// launchd manager. See AI.md PART 24 "launchd (macOS)" for the plist
// template, the /Library/LaunchDaemons installation path, and the
// launchctl load/unload commands, plus PART 23 "Service Installation
// Logic" for the LaunchAgent fallback when not running as root.
package service

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// launchdManager owns the plist and drives launchctl in either the system
// domain (LaunchDaemon) or the calling user's GUI domain (LaunchAgent).
type launchdManager struct {
	p Params
}

func (m *launchdManager) Name() string { return string(Launchd) }

// plistPath is /Library/LaunchDaemons/{plist_name}.plist for a system
// install, or ~/Library/LaunchAgents/{plist_name}.plist for the
// unprivileged fallback.
func (m *launchdManager) plistPath() string {
	if m.p.UserLevel {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		return filepath.Join(home, "Library", "LaunchAgents", m.p.PlistName()+".plist")
	}
	return filepath.Join("/Library/LaunchDaemons", m.p.PlistName()+".plist")
}

// domain is the launchctl service target domain.
func (m *launchdManager) domain() string {
	if m.p.UserLevel {
		return "gui/" + strconv.Itoa(os.Getuid())
	}
	return "system"
}

// target is the fully qualified launchctl service target.
func (m *launchdManager) target() string {
	return m.domain() + "/" + m.p.PlistName()
}

func (m *launchdManager) Files() []string { return []string{m.plistPath()} }

func (m *launchdManager) Install() error {
	return writeFile(m.plistPath(), launchdPlist(m.p), 0o644)
}

func (m *launchdManager) Remove() error { return removeFiles(m.plistPath()) }

// Enable marks the service startable. RunAtLoad in the plist is what
// actually starts it at boot; `launchctl enable` only clears a previous
// explicit disable, so a failure here is not fatal on its own.
func (m *launchdManager) Enable() error {
	if err := run("launchctl", "enable", m.target()); err != nil {
		return run("launchctl", "load", "-w", m.plistPath())
	}
	return nil
}

func (m *launchdManager) Disable() error {
	if err := run("launchctl", "disable", m.target()); err != nil {
		return run("launchctl", "unload", "-w", m.plistPath())
	}
	return nil
}

// Start prefers the modern bootstrap subcommand and falls back to the
// load form documented in PART 24 for older macOS releases.
func (m *launchdManager) Start() error {
	if err := run("launchctl", "bootstrap", m.domain(), m.plistPath()); err == nil {
		return nil
	}
	return run("launchctl", "load", "-w", m.plistPath())
}

func (m *launchdManager) Stop() error {
	if err := run("launchctl", "bootout", m.target()); err == nil {
		return nil
	}
	return run("launchctl", "unload", m.plistPath())
}

func (m *launchdManager) Restart() error {
	if err := run("launchctl", "kickstart", "-k", m.target()); err == nil {
		return nil
	}
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start()
}

// Reload signals the running process: launchd has no reload verb, so the
// binary's own SIGHUP handler is the only way to re-read configuration
// without a restart.
func (m *launchdManager) Reload() error {
	return reloadViaSIGHUP(m.p)
}

func (m *launchdManager) Status() Status {
	st := Status{Installed: fileExists(m.plistPath()), State: "stopped"}
	if !st.Installed {
		return st
	}

	// RunAtLoad is always true in the generated plist, so auto-start is
	// on unless the label has been explicitly disabled.
	disabled, _ := runOutput("launchctl", "print-disabled", m.domain())
	st.AutoStart = !strings.Contains(disabled, "\""+m.p.PlistName()+"\" => true")

	out, err := runOutput("launchctl", "print", m.target())
	if err == nil {
		if strings.Contains(out, "state = running") {
			st.State = "running"
		}
		st.PID = launchdPID(out)
	} else if list, listErr := runOutput("launchctl", "list", m.p.PlistName()); listErr == nil {
		if pid := launchdListPID(list); pid > 0 {
			st.State = "running"
			st.PID = pid
		}
	}

	if st.State == "running" && st.PID == 0 {
		st.PID = pidFromFile(m.p.PIDFile)
	}
	if st.State != "running" && !st.AutoStart {
		st.State = "disabled"
	}
	return st
}

// launchdPID extracts the "pid = N" field from `launchctl print` output.
func launchdPID(out string) int {
	for _, line := range strings.Split(out, "\n") {
		field := strings.TrimSpace(line)
		if !strings.HasPrefix(field, "pid = ") {
			continue
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(field, "pid = "))); err == nil {
			return pid
		}
	}
	return 0
}

// launchdListPID extracts the PID from the legacy `launchctl list LABEL`
// property-list output ("PID" = 1234;).
func launchdListPID(out string) int {
	for _, line := range strings.Split(out, "\n") {
		field := strings.TrimSpace(line)
		if !strings.HasPrefix(field, "\"PID\" = ") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(field, "\"PID\" = ")), ";")
		if pid, err := strconv.Atoi(value); err == nil {
			return pid
		}
	}
	return 0
}
