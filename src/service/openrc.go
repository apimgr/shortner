// OpenRC manager. See AI.md PART 24 "OpenRC (Alpine, Gentoo, Devuan)" for
// the init script template, installation path, and rc-update/rc-service
// commands.
package service

import "path/filepath"

// openrcManager drives rc-service and rc-update against
// /etc/init.d/{internal_name}.
type openrcManager struct {
	p Params
}

func (m *openrcManager) Name() string { return string(OpenRC) }

func (m *openrcManager) scriptPath() string {
	return filepath.Join("/etc/init.d", m.p.InternalName)
}

func (m *openrcManager) Files() []string { return []string{m.scriptPath()} }

func (m *openrcManager) Install() error {
	return writeFile(m.scriptPath(), openrcScript(m.p), 0o755)
}

func (m *openrcManager) Remove() error { return removeFiles(m.scriptPath()) }

func (m *openrcManager) Enable() error {
	return run("rc-update", "add", m.p.InternalName, "default")
}

func (m *openrcManager) Disable() error {
	return run("rc-update", "del", m.p.InternalName, "default")
}

func (m *openrcManager) Start() error {
	return run("rc-service", m.p.InternalName, "start")
}

func (m *openrcManager) Stop() error {
	return run("rc-service", m.p.InternalName, "stop")
}

func (m *openrcManager) Restart() error {
	return run("rc-service", m.p.InternalName, "restart")
}

// Reload uses the init script's reload verb when OpenRC exposes one and
// otherwise signals the running process directly, so "reload" never
// silently becomes a restart.
func (m *openrcManager) Reload() error {
	if err := run("rc-service", m.p.InternalName, "reload"); err == nil {
		return nil
	}
	return reloadViaSIGHUP(m.p)
}

func (m *openrcManager) Status() Status {
	st := Status{Installed: fileExists(m.scriptPath()), State: "stopped"}
	if !st.Installed {
		return st
	}

	if out, err := runOutput("rc-update", "show", "default"); err == nil {
		st.AutoStart = containsWord(out, m.p.InternalName)
	}

	if _, err := runOutput("rc-service", m.p.InternalName, "status"); err == nil {
		st.State = "running"
		st.PID = pidFromFile(m.p.PIDFile)
	} else if !st.AutoStart {
		st.State = "disabled"
	}
	return st
}
