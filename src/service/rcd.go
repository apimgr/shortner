// FreeBSD rc.d manager. See AI.md PART 24 "rc.d (FreeBSD)" for the script
// template and the /usr/local/etc/rc.d installation path.
package service

import "path/filepath"

// rcdManager owns /usr/local/etc/rc.d/{internal_name} and toggles
// auto-start through the {internal_name}_enable rc.conf variable.
type rcdManager struct {
	p Params
}

func (m *rcdManager) Name() string { return string(RCd) }

func (m *rcdManager) scriptPath() string {
	return filepath.Join("/usr/local/etc/rc.d", m.p.InternalName)
}

func (m *rcdManager) rcvar() string { return m.p.InternalName + "_enable" }

func (m *rcdManager) Files() []string { return []string{m.scriptPath()} }

func (m *rcdManager) Install() error {
	return writeFile(m.scriptPath(), rcdScript(m.p), 0o755)
}

func (m *rcdManager) Remove() error { return removeFiles(m.scriptPath()) }

func (m *rcdManager) Enable() error {
	return run("sysrc", m.rcvar()+"=YES")
}

func (m *rcdManager) Disable() error {
	return run("sysrc", m.rcvar()+"=NO")
}

func (m *rcdManager) Start() error {
	return run("service", m.p.InternalName, "start")
}

func (m *rcdManager) Stop() error {
	return run("service", m.p.InternalName, "stop")
}

func (m *rcdManager) Restart() error {
	return run("service", m.p.InternalName, "restart")
}

// Reload uses rc.subr's reload verb, falling back to a direct SIGHUP when
// the script does not implement one.
func (m *rcdManager) Reload() error {
	if err := run("service", m.p.InternalName, "reload"); err == nil {
		return nil
	}
	return reloadViaSIGHUP(m.p)
}

func (m *rcdManager) Status() Status {
	st := Status{Installed: fileExists(m.scriptPath()), State: "stopped"}
	if !st.Installed {
		return st
	}

	if out, err := runOutput("sysrc", "-n", m.rcvar()); err == nil {
		st.AutoStart = isYes(out)
	}

	if _, err := runOutput("service", m.p.InternalName, "status"); err == nil {
		st.State = "running"
		st.PID = pidFromFile(m.p.PIDFile)
	} else if !st.AutoStart {
		st.State = "disabled"
	}
	return st
}
