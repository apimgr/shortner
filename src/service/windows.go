//go:build windows

// Windows Service manager. See AI.md PART 23 "Windows Service Account"
// (Virtual Service Account via an empty ServiceStartName) and PART 24
// "Windows Service".
package service

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// windowsManager drives the Service Control Manager. The service always
// runs as its Virtual Service Account (NT SERVICE\{internal_name}), which
// Windows creates and manages automatically when ServiceStartName is
// empty — no user creation, no password, minimal privileges.
type windowsManager struct {
	p Params
}

// newWindowsManager builds the SCM-backed manager.
func newWindowsManager(p Params) (Manager, error) {
	return &windowsManager{p: p}, nil
}

func (m *windowsManager) Name() string { return string(WindowsService) }

// Files reports no on-disk service definition: the SCM stores the
// service in the registry, not in a file this package owns.
func (m *windowsManager) Files() []string { return nil }

// open connects to the SCM and opens the service.
func (m *windowsManager) open() (*mgr.Mgr, *mgr.Service, error) {
	scm, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("service: connect to service control manager: %w", err)
	}
	s, err := scm.OpenService(m.p.InternalName)
	if err != nil {
		scm.Disconnect()
		return nil, nil, fmt.Errorf("service: open service %s: %w", m.p.InternalName, err)
	}
	return scm, s, nil
}

func (m *windowsManager) Install() error {
	scm, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("service: connect to service control manager: %w", err)
	}
	defer scm.Disconnect()

	if existing, openErr := scm.OpenService(m.p.InternalName); openErr == nil {
		existing.Close()
		return fmt.Errorf("service: %s is already installed", m.p.InternalName)
	}

	s, err := scm.CreateService(
		m.p.InternalName,
		m.p.BinaryPath,
		mgr.Config{
			DisplayName: m.p.ProjectName,
			Description: m.p.ProjectName + " service",
			StartType:   mgr.StartAutomatic,
			// Empty = Virtual Service Account (NT SERVICE\{internal_name}).
			ServiceStartName: "",
		},
	)
	if err != nil {
		return fmt.Errorf("service: create service %s: %w", m.p.InternalName, err)
	}
	defer s.Close()
	return nil
}

func (m *windowsManager) Remove() error {
	scm, s, err := m.open()
	if err != nil {
		return nil
	}
	defer scm.Disconnect()
	defer s.Close()
	if err := s.Delete(); err != nil {
		return fmt.Errorf("service: delete service %s: %w", m.p.InternalName, err)
	}
	return nil
}

// setStartType switches the service between automatic and disabled start.
func (m *windowsManager) setStartType(startType uint32) error {
	scm, s, err := m.open()
	if err != nil {
		return err
	}
	defer scm.Disconnect()
	defer s.Close()

	cfg, err := s.Config()
	if err != nil {
		return fmt.Errorf("service: read config for %s: %w", m.p.InternalName, err)
	}
	cfg.StartType = startType
	if err := s.UpdateConfig(cfg); err != nil {
		return fmt.Errorf("service: update config for %s: %w", m.p.InternalName, err)
	}
	return nil
}

func (m *windowsManager) Enable() error  { return m.setStartType(mgr.StartAutomatic) }
func (m *windowsManager) Disable() error { return m.setStartType(mgr.StartDisabled) }

func (m *windowsManager) Start() error {
	scm, s, err := m.open()
	if err != nil {
		return err
	}
	defer scm.Disconnect()
	defer s.Close()
	if err := s.Start(); err != nil {
		return fmt.Errorf("service: start %s: %w", m.p.InternalName, err)
	}
	return nil
}

func (m *windowsManager) Stop() error {
	scm, s, err := m.open()
	if err != nil {
		return err
	}
	defer scm.Disconnect()
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("service: stop %s: %w", m.p.InternalName, err)
	}
	// Wait for the SCM to report the service actually stopped, so a
	// following Start (restart) does not race the shutdown.
	deadline := time.Now().Add(30 * time.Second)
	for status.State != svc.Stopped {
		if time.Now().After(deadline) {
			return fmt.Errorf("service: timed out waiting for %s to stop", m.p.InternalName)
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("service: query %s: %w", m.p.InternalName, err)
		}
	}
	return nil
}

func (m *windowsManager) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start()
}

// Reload sends the SCM's ParamChange control code, which the service
// handler treats as "re-read configuration" — the Windows equivalent of
// SIGHUP.
func (m *windowsManager) Reload() error {
	scm, s, err := m.open()
	if err != nil {
		return err
	}
	defer scm.Disconnect()
	defer s.Close()
	if _, err := s.Control(svc.ParamChange); err != nil {
		return fmt.Errorf("service: reload %s: %w", m.p.InternalName, err)
	}
	return nil
}

func (m *windowsManager) Status() Status {
	st := Status{State: "stopped"}

	scm, s, err := m.open()
	if err != nil {
		return st
	}
	defer scm.Disconnect()
	defer s.Close()
	st.Installed = true

	if cfg, cfgErr := s.Config(); cfgErr == nil {
		st.AutoStart = cfg.StartType == mgr.StartAutomatic
	}

	status, queryErr := s.Query()
	if queryErr == nil {
		switch status.State {
		case svc.Running, svc.StartPending, svc.ContinuePending:
			st.State = "running"
			st.PID = int(status.ProcessId)
		default:
			if !st.AutoStart {
				st.State = "disabled"
			}
		}
	} else if !st.AutoStart {
		st.State = "disabled"
	}
	return st
}
