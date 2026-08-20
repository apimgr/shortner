// runit manager. See AI.md PART 24 "runit (Linux)" for the
// /etc/sv/{internal_name}/ layout and the run / log/run scripts.
package service

import (
	"os"
	"path/filepath"
)

// runitManager owns /etc/sv/{internal_name}/ and enables the service by
// symlinking it into the active runsvdir.
type runitManager struct {
	p Params
}

func (m *runitManager) Name() string { return string(Runit) }

func (m *runitManager) serviceDir() string {
	return filepath.Join("/etc/sv", m.p.InternalName)
}

// activeLink is the runsvdir entry that makes the service supervised. The
// default runsvdir differs per distribution, so the first existing
// candidate wins.
func (m *runitManager) activeLink() string {
	for _, dir := range []string{"/etc/runit/runsvdir/default", "/var/service", "/etc/service", "/service"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return filepath.Join(dir, m.p.InternalName)
		}
	}
	return filepath.Join("/etc/runit/runsvdir/default", m.p.InternalName)
}

func (m *runitManager) Files() []string {
	return []string{m.serviceDir(), m.activeLink()}
}

func (m *runitManager) Install() error {
	if err := writeFile(filepath.Join(m.serviceDir(), "run"), runitRunScript(m.p), 0o755); err != nil {
		return err
	}
	return writeFile(filepath.Join(m.serviceDir(), "log", "run"), runitLogRunScript(m.p), 0o755)
}

func (m *runitManager) Remove() error {
	return removeFiles(m.activeLink(), m.serviceDir())
}

func (m *runitManager) Enable() error {
	link := m.activeLink()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(link); err == nil {
		return nil
	}
	if err := os.Symlink(m.serviceDir(), link); err != nil {
		return err
	}
	return nil
}

func (m *runitManager) Disable() error {
	return removeFiles(m.activeLink())
}

func (m *runitManager) Start() error {
	return run("sv", "start", m.p.InternalName)
}

func (m *runitManager) Stop() error {
	return run("sv", "stop", m.p.InternalName)
}

func (m *runitManager) Restart() error {
	return run("sv", "restart", m.p.InternalName)
}

// Reload uses runit's own SIGHUP verb.
func (m *runitManager) Reload() error {
	return run("sv", "hup", m.p.InternalName)
}

func (m *runitManager) Status() Status {
	st := Status{Installed: fileExists(filepath.Join(m.serviceDir(), "run")), State: "stopped"}
	if !st.Installed {
		return st
	}

	_, err := os.Lstat(m.activeLink())
	st.AutoStart = err == nil

	out, err := runOutput("sv", "status", m.p.InternalName)
	switch {
	case err == nil && hasPrefixWord(out, "run"):
		st.State = "running"
		st.PID = runitPID(out)
		if st.PID == 0 {
			st.PID = pidFromFile(m.p.PIDFile)
		}
	case !st.AutoStart:
		st.State = "disabled"
	}
	return st
}
