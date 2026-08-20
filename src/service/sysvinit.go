// SysVinit manager. See AI.md PART 24 "SysVinit (legacy Linux, init.d)"
// for the script template, the shared /etc/init.d path, and the
// update-rc.d / chkconfig enable commands.
package service

import (
	"os/exec"
	"path/filepath"
)

// sysvinitManager drives /etc/init.d/{internal_name} directly, enabling
// it with update-rc.d (Debian-style) or chkconfig (RHEL-style).
type sysvinitManager struct {
	p Params
}

func (m *sysvinitManager) Name() string { return string(SysVinit) }

func (m *sysvinitManager) scriptPath() string {
	return filepath.Join("/etc/init.d", m.p.InternalName)
}

func (m *sysvinitManager) Files() []string { return []string{m.scriptPath()} }

func (m *sysvinitManager) Install() error {
	return writeFile(m.scriptPath(), sysvinitScript(m.p), 0o755)
}

func (m *sysvinitManager) Remove() error { return removeFiles(m.scriptPath()) }

// hasChkconfig reports whether the RHEL-style enable tool is present;
// update-rc.d is preferred when both exist because the generated script
// carries LSB headers.
func hasChkconfig() bool {
	_, err := exec.LookPath("chkconfig")
	return err == nil
}

func hasUpdateRCD() bool {
	_, err := exec.LookPath("update-rc.d")
	return err == nil
}

func (m *sysvinitManager) Enable() error {
	if hasUpdateRCD() {
		return run("update-rc.d", m.p.InternalName, "defaults")
	}
	if hasChkconfig() {
		if err := run("chkconfig", "--add", m.p.InternalName); err != nil {
			return err
		}
		return run("chkconfig", m.p.InternalName, "on")
	}
	return errNoSysVTool
}

func (m *sysvinitManager) Disable() error {
	if hasUpdateRCD() {
		return run("update-rc.d", m.p.InternalName, "remove")
	}
	if hasChkconfig() {
		return run("chkconfig", m.p.InternalName, "off")
	}
	return errNoSysVTool
}

func (m *sysvinitManager) Start() error {
	return run(m.scriptPath(), "start")
}

func (m *sysvinitManager) Stop() error {
	return run(m.scriptPath(), "stop")
}

func (m *sysvinitManager) Restart() error {
	return run(m.scriptPath(), "restart")
}

// Reload signals the running process: the PART 24 SysVinit script
// implements only start/stop/restart/status, so a real reload has to go
// through the binary's own SIGHUP handler.
func (m *sysvinitManager) Reload() error {
	return reloadViaSIGHUP(m.p)
}

func (m *sysvinitManager) Status() Status {
	st := Status{Installed: fileExists(m.scriptPath()), State: "stopped"}
	if !st.Installed {
		return st
	}

	// The generated script's own status verb is the authority on
	// running/stopped; it exits 0 only when the recorded PID is alive.
	if _, err := runOutput(m.scriptPath(), "status"); err == nil {
		st.State = "running"
		st.PID = pidFromFile(m.p.PIDFile)
	}

	st.AutoStart = sysvRunlevelLinked(m.p.InternalName)
	if st.State != "running" && !st.AutoStart {
		st.State = "disabled"
	}
	return st
}

// sysvRunlevelLinked reports whether an rc?.d start symlink exists for the
// service, which is how both update-rc.d and chkconfig record auto-start.
func sysvRunlevelLinked(name string) bool {
	for _, level := range []string{"2", "3", "4", "5"} {
		matches, err := filepath.Glob("/etc/rc" + level + ".d/S??" + name)
		if err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}
