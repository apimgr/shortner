package service

import (
	"strings"
)

// taskSchedulerManager installs the unprivileged Windows service fallback
// as a task started at the current user's logon. It never creates or
// modifies a Windows service in the Service Control Manager.
type taskSchedulerManager struct {
	p Params
}

func (m *taskSchedulerManager) Name() string { return "windows-task-scheduler" }

// Files reports no owned file because Task Scheduler stores task definitions
// outside the project filesystem.
func (m *taskSchedulerManager) Files() []string { return nil }

func (m *taskSchedulerManager) taskName() string { return m.p.InternalName }

func (m *taskSchedulerManager) Install() error {
	return run("schtasks", "/Create", "/SC", "ONLOGON", "/TN", m.taskName(), "/TR", quoteTaskCommand(m.p.BinaryPath), "/RL", "LIMITED", "/F")
}

func (m *taskSchedulerManager) Remove() error {
	if !m.Status().Installed {
		return nil
	}
	return run("schtasks", "/Delete", "/TN", m.taskName(), "/F")
}

func (m *taskSchedulerManager) Enable() error {
	return run("schtasks", "/Change", "/TN", m.taskName(), "/ENABLE")
}

func (m *taskSchedulerManager) Disable() error {
	return run("schtasks", "/Change", "/TN", m.taskName(), "/DISABLE")
}

func (m *taskSchedulerManager) Start() error {
	return run("schtasks", "/Run", "/TN", m.taskName())
}

func (m *taskSchedulerManager) Stop() error {
	return run("schtasks", "/End", "/TN", m.taskName())
}

func (m *taskSchedulerManager) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}
	return m.Start()
}

func (m *taskSchedulerManager) Reload() error {
	return reloadViaSIGHUP(m.p)
}

func (m *taskSchedulerManager) Status() Status {
	st := Status{State: "stopped"}
	out, err := runOutput("schtasks", "/Query", "/TN", m.taskName(), "/FO", "LIST", "/V")
	if err != nil {
		return st
	}
	st.Installed = true
	st.AutoStart = taskSchedulerEnabled(out)
	switch taskSchedulerState(out) {
	case "running":
		st.State = "running"
	case "disabled":
		st.AutoStart = false
		st.State = "disabled"
	default:
		if !st.AutoStart {
			st.State = "disabled"
		}
	}
	return st
}

func quoteTaskCommand(path string) string {
	return `"` + strings.ReplaceAll(path, `"`, `\"`) + `"`
}

func taskSchedulerEnabled(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		label, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.EqualFold(strings.TrimSpace(label), "Scheduled Task State") {
			return strings.EqualFold(strings.TrimSpace(value), "Enabled")
		}
	}
	return !strings.Contains(strings.ToLower(output), "disabled")
}

func taskSchedulerState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		label, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(label), "Status") {
			continue
		}
		value = strings.ToLower(strings.TrimSpace(value))
		if strings.Contains(value, "running") {
			return "running"
		}
		if strings.Contains(value, "disabled") {
			return "disabled"
		}
		return value
	}
	return ""
}

var _ Manager = (*taskSchedulerManager)(nil)
