package service

import (
	"errors"
	"strings"
	"testing"
)

// recorder captures the commands a manager would run, so every verb can
// be asserted without a live init system. AI.md PART 24's install and
// enable steps genuinely cannot be exercised end to end here — that is
// noted in TODO.AI.md — but the exact command lines can.
type recorder struct {
	commands []string
	// output is consulted by runOutput, keyed by the command line.
	output map[string]string
	// fail makes every recorded command return an error.
	fail bool
}

// install swaps the package's run/runOutput hooks for the duration of a
// test.
func (r *recorder) install(t *testing.T) {
	t.Helper()
	if r.output == nil {
		r.output = map[string]string{}
	}
	originalRun, originalOutput := run, runOutput
	t.Cleanup(func() { run, runOutput = originalRun, originalOutput })

	run = func(name string, args ...string) error {
		line := strings.TrimSpace(name + " " + strings.Join(args, " "))
		r.commands = append(r.commands, line)
		if r.fail {
			return errors.New("recorded failure")
		}
		return nil
	}
	runOutput = func(name string, args ...string) (string, error) {
		line := strings.TrimSpace(name + " " + strings.Join(args, " "))
		r.commands = append(r.commands, line)
		out, ok := r.output[line]
		if !ok {
			return "", errors.New("no recorded output")
		}
		return out, nil
	}
}

// ran reports whether any recorded command contains every fragment.
func (r *recorder) ran(fragments ...string) bool {
	for _, cmd := range r.commands {
		matched := true
		for _, fragment := range fragments {
			if !strings.Contains(cmd, fragment) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func TestNewForInit(t *testing.T) {
	p := templateParams()
	tests := []struct {
		init InitSystem
		want string
	}{
		{Systemd, "systemd"},
		{OpenRC, "openrc"},
		{SysVinit, "sysvinit"},
		{Runit, "runit"},
		{RCd, "rcd"},
		{Launchd, "launchd"},
	}
	for _, tc := range tests {
		t.Run(string(tc.init), func(t *testing.T) {
			m, err := newForInit(tc.init, p)
			if err != nil {
				t.Fatalf("newForInit(%v): %v", tc.init, err)
			}
			if m.Name() != tc.want {
				t.Errorf("Name() = %q, want %q", m.Name(), tc.want)
			}
			if len(m.Files()) == 0 {
				t.Error("Files() is empty")
			}
		})
	}

	if _, err := newForInit(UnknownInit, p); !errors.Is(err, ErrUnsupported) {
		t.Errorf("newForInit(unknown) error = %v, want ErrUnsupported", err)
	}
}

// TestSystemdUserLevelLifecycle exercises the full PART 23 user-service
// fallback against a temporary HOME, which is the one install path that
// touches no system directory.
func TestSystemdUserLevelLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := templateParams()
	p.UserLevel = true
	m := &systemdManager{p: p}

	unit := m.unitPath()
	if !strings.HasPrefix(unit, home) {
		t.Fatalf("user unit path %q is not under HOME", unit)
	}
	if !strings.HasSuffix(unit, "/.config/systemd/user/shortner.service") {
		t.Errorf("unit path = %q, want the systemd --user location", unit)
	}

	rec := &recorder{}
	rec.install(t)

	if err := Install(m); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !fileExists(unit) {
		t.Fatal("Install did not write the unit file")
	}
	if !rec.ran("systemctl", "--user", "daemon-reload") {
		t.Errorf("missing daemon-reload; ran %v", rec.commands)
	}
	if !rec.ran("systemctl", "--user", "enable", "shortner.service") {
		t.Errorf("missing enable; ran %v", rec.commands)
	}
	if !rec.ran("systemctl", "--user", "start", "shortner.service") {
		t.Errorf("missing start; ran %v", rec.commands)
	}

	// Status reads the real (temporary) unit file plus stubbed systemctl.
	rec.output["systemctl --user is-enabled shortner.service"] = "enabled"
	rec.output["systemctl --user is-active shortner.service"] = "active"
	rec.output["systemctl --user show -p MainPID --value shortner.service"] = "4321"
	st := m.Status()
	if !st.Installed || !st.AutoStart || st.State != "running" || st.PID != 4321 {
		t.Errorf("Status() = %+v, want installed/running/enabled with pid 4321", st)
	}

	rec.output["systemctl --user is-enabled shortner.service"] = "disabled"
	rec.output["systemctl --user is-active shortner.service"] = "inactive"
	st = m.Status()
	if st.State != "disabled" || st.AutoStart || st.PID != 0 {
		t.Errorf("Status() = %+v, want a disabled, stopped service", st)
	}

	if err := m.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !rec.ran("systemctl", "--user", "kill", "-s", "HUP") {
		t.Errorf("Reload did not send SIGHUP; ran %v", rec.commands)
	}

	if err := m.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if fileExists(unit) {
		t.Error("Remove left the unit file in place")
	}
}

// TestSystemdSystemPaths checks the privileged install location without
// writing to it.
func TestSystemdSystemPaths(t *testing.T) {
	m := &systemdManager{p: templateParams()}
	if got, want := m.unitPath(), "/etc/systemd/system/shortner.service"; got != want {
		t.Errorf("unitPath() = %q, want %q", got, want)
	}
	if got, want := m.unitName(), "shortner.service"; got != want {
		t.Errorf("unitName() = %q, want %q", got, want)
	}
	if got := m.ctl("start"); len(got) != 1 || got[0] != "start" {
		t.Errorf("ctl() on a system install = %v, want no --user prefix", got)
	}
}

// TestManagerVerbs asserts the command line each manager runs for every
// lifecycle verb, per AI.md PART 24.
func TestManagerVerbs(t *testing.T) {
	p := templateParams()

	tests := []struct {
		name    string
		init    InitSystem
		verb    func(Manager) error
		want    []string
		wantAny bool
	}{
		{name: "openrc enable", init: OpenRC, verb: Manager.Enable, want: []string{"rc-update add shortner default"}},
		{name: "openrc disable", init: OpenRC, verb: Manager.Disable, want: []string{"rc-update del shortner default"}},
		{name: "openrc start", init: OpenRC, verb: Manager.Start, want: []string{"rc-service shortner start"}},
		{name: "openrc stop", init: OpenRC, verb: Manager.Stop, want: []string{"rc-service shortner stop"}},
		{name: "openrc restart", init: OpenRC, verb: Manager.Restart, want: []string{"rc-service shortner restart"}},

		{name: "sysvinit start", init: SysVinit, verb: Manager.Start, want: []string{"/etc/init.d/shortner start"}},
		{name: "sysvinit stop", init: SysVinit, verb: Manager.Stop, want: []string{"/etc/init.d/shortner stop"}},
		{name: "sysvinit restart", init: SysVinit, verb: Manager.Restart, want: []string{"/etc/init.d/shortner restart"}},

		{name: "runit start", init: Runit, verb: Manager.Start, want: []string{"sv start shortner"}},
		{name: "runit stop", init: Runit, verb: Manager.Stop, want: []string{"sv stop shortner"}},
		{name: "runit restart", init: Runit, verb: Manager.Restart, want: []string{"sv restart shortner"}},
		{name: "runit reload", init: Runit, verb: Manager.Reload, want: []string{"sv hup shortner"}},

		{name: "rcd enable", init: RCd, verb: Manager.Enable, want: []string{"sysrc shortner_enable=YES"}},
		{name: "rcd disable", init: RCd, verb: Manager.Disable, want: []string{"sysrc shortner_enable=NO"}},
		{name: "rcd start", init: RCd, verb: Manager.Start, want: []string{"service shortner start"}},
		{name: "rcd stop", init: RCd, verb: Manager.Stop, want: []string{"service shortner stop"}},
		{name: "rcd restart", init: RCd, verb: Manager.Restart, want: []string{"service shortner restart"}},

		{name: "launchd restart", init: Launchd, verb: Manager.Restart, want: []string{"launchctl kickstart -k"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := newForInit(tc.init, p)
			if err != nil {
				t.Fatalf("newForInit: %v", err)
			}
			rec := &recorder{}
			rec.install(t)
			if err := tc.verb(m); err != nil {
				t.Fatalf("verb: %v", err)
			}
			if !rec.ran(tc.want...) {
				t.Errorf("commands = %v, want one containing %v", rec.commands, tc.want)
			}
		})
	}
}

// TestManagerFiles pins the PART 24 installation paths.
func TestManagerFiles(t *testing.T) {
	p := templateParams()
	tests := []struct {
		init InitSystem
		want []string
	}{
		{Systemd, []string{"/etc/systemd/system/shortner.service"}},
		{OpenRC, []string{"/etc/init.d/shortner"}},
		{SysVinit, []string{"/etc/init.d/shortner"}},
		{Runit, []string{"/etc/sv/shortner", "runsvdir/default/shortner"}},
		{RCd, []string{"/usr/local/etc/rc.d/shortner"}},
		{Launchd, []string{"/Library/LaunchDaemons/io.github.apimgr.shortner.plist"}},
	}
	for _, tc := range tests {
		t.Run(string(tc.init), func(t *testing.T) {
			m, err := newForInit(tc.init, p)
			if err != nil {
				t.Fatalf("newForInit: %v", err)
			}
			files := strings.Join(m.Files(), " ")
			for _, want := range tc.want {
				if !strings.Contains(files, want) {
					t.Errorf("Files() = %v, want to contain %q", m.Files(), want)
				}
			}
		})
	}
}

// TestNotInstalledStatus proves every manager answers "not installed"
// rather than failing when nothing has been installed yet.
func TestNotInstalledStatus(t *testing.T) {
	p := templateParams()
	// Point the launchd plist and the runit service dir at a temp tree so
	// a real /Library or /etc/sv on the host cannot influence the result.
	for _, init := range []InitSystem{Systemd, OpenRC, SysVinit, Runit, RCd, Launchd} {
		t.Run(string(init), func(t *testing.T) {
			rec := &recorder{}
			rec.install(t)
			m, err := newForInit(init, p)
			if err != nil {
				t.Fatalf("newForInit: %v", err)
			}
			st := m.Status()
			if st.Installed {
				t.Skip("a real service of this type is installed on the test host")
			}
			if st.State != "stopped" {
				t.Errorf("State = %q, want %q", st.State, "stopped")
			}
			if st.PID != 0 {
				t.Errorf("PID = %d, want 0", st.PID)
			}
		})
	}
}

// fakeManager records orchestration calls for the PART 23 Install /
// Disable / Uninstall sequences.
type fakeManager struct {
	calls   []string
	files   []string
	stopErr error
}

func (f *fakeManager) record(name string) { f.calls = append(f.calls, name) }

func (f *fakeManager) Name() string    { return "fake" }
func (f *fakeManager) Files() []string { return f.files }
func (f *fakeManager) Install() error  { f.record("install"); return nil }
func (f *fakeManager) Remove() error   { f.record("remove"); return nil }
func (f *fakeManager) Enable() error   { f.record("enable"); return nil }
func (f *fakeManager) Disable() error  { f.record("disable"); return nil }
func (f *fakeManager) Start() error    { f.record("start"); return nil }
func (f *fakeManager) Stop() error     { f.record("stop"); return f.stopErr }
func (f *fakeManager) Restart() error  { f.record("restart"); return nil }
func (f *fakeManager) Reload() error   { f.record("reload"); return nil }
func (f *fakeManager) Status() Status  { return Status{State: "stopped"} }

func TestInstallOrder(t *testing.T) {
	f := &fakeManager{}
	if err := Install(f); err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := "install,enable,start"
	if got := strings.Join(f.calls, ","); got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// TestDisableIgnoresStopFailure covers PART 23 "Service Disable Logic":
// an already-stopped service must still get disabled.
func TestDisableIgnoresStopFailure(t *testing.T) {
	f := &fakeManager{stopErr: errors.New("already stopped")}
	if err := Disable(f); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if got, want := strings.Join(f.calls, ","), "stop,disable"; got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// TestUninstallRemovesEverything covers PART 23 "Service Uninstall Logic"
// steps 1-4 against a throwaway path set.
func TestUninstallRemovesEverything(t *testing.T) {
	root := t.TempDir()
	p := Params{
		InternalName: "shortner",
		ConfigDir:    root + "/config",
		DataDir:      root + "/data",
		CacheDir:     root + "/cache",
		LogDir:       root + "/logs",
		BackupDir:    root + "/backups",
		PIDFile:      root + "/run/server.pid",
	}
	for _, path := range []string{
		p.ConfigDir + "/server.yml",
		p.DataDir + "/server.db",
		p.CacheDir + "/geoip.mmdb",
		p.LogDir + "/server.log",
		p.BackupDir + "/backup.tar.gz",
		p.PIDFile,
	} {
		if err := writeFile(path, "x\n", 0o644); err != nil {
			t.Fatalf("writeFile: %v", err)
		}
	}

	serviceFile := root + "/etc/shortner.service"
	if err := writeFile(serviceFile, "unit\n", 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	f := &fakeManager{files: []string{serviceFile}}

	result, err := Uninstall(f, p)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if got, want := strings.Join(f.calls, ","), "stop,disable,remove"; got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.LogDir, p.BackupDir} {
		if fileExists(dir) {
			t.Errorf("%s still exists after uninstall", dir)
		}
	}
	if fileExists(p.PIDFile) {
		t.Error("the PID file still exists after uninstall")
	}
	if len(result.RemovedPaths) != 7 {
		t.Errorf("RemovedPaths = %v, want the service file, five directories, and the PID file", result.RemovedPaths)
	}
	// No ownership marker was written, so no account may be claimed.
	if result.RemovedUser {
		t.Error("RemovedUser = true without an ownership marker")
	}
}

// TestUninstallDeletesOwnedAccount proves the account is only deleted
// when the marker says this binary created it. DeleteServiceAccount is
// stubbed through the run hook — creating and deleting a real system
// user is not something a test may do.
func TestUninstallDeletesOwnedAccount(t *testing.T) {
	if AccountExists("shortner") {
		t.Skip("a real shortner account exists on this host")
	}
	root := t.TempDir()
	p := Params{
		InternalName: "shortner",
		ConfigDir:    root + "/config",
		DataDir:      root + "/data",
	}
	if err := writeFile(accountMarker(p), "shortner\n", 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	rec := &recorder{}
	rec.install(t)

	result, err := Uninstall(&fakeManager{}, p)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !result.RemovedUser || result.UserName != "shortner" {
		t.Errorf("result = %+v, want the owned account to be removed", result)
	}
}

func TestPIDFromFile(t *testing.T) {
	if got := pidFromFile(""); got != 0 {
		t.Errorf("pidFromFile(\"\") = %d, want 0", got)
	}
	if got := pidFromFile(t.TempDir() + "/missing.pid"); got != 0 {
		t.Errorf("pidFromFile(missing) = %d, want 0", got)
	}
}

// TestReloadWithoutRunningServer covers the SIGHUP fallback used by the
// managers whose init scripts have no reload verb: with nothing running
// it must fail loudly rather than silently restarting anything.
func TestReloadWithoutRunningServer(t *testing.T) {
	p := templateParams()
	p.PIDFile = t.TempDir() + "/server.pid"
	m := &sysvinitManager{p: p}
	err := m.Reload()
	if err == nil {
		t.Fatal("Reload succeeded with no running server")
	}
	if !strings.Contains(err.Error(), "is not running") {
		t.Errorf("error = %v, want a not-running message", err)
	}
}

// TestSysVinitEnableRequiresATool covers the PART 24 rule that SysVinit
// auto-start needs update-rc.d or chkconfig; without either, the failure
// is explicit.
func TestSysVinitEnableRequiresATool(t *testing.T) {
	if hasUpdateRCD() || hasChkconfig() {
		t.Skip("this host has a SysVinit management tool")
	}
	rec := &recorder{}
	rec.install(t)
	m := &sysvinitManager{p: templateParams()}
	if err := m.Enable(); !errors.Is(err, errNoSysVTool) {
		t.Errorf("Enable() error = %v, want errNoSysVTool", err)
	}
	if err := m.Disable(); !errors.Is(err, errNoSysVTool) {
		t.Errorf("Disable() error = %v, want errNoSysVTool", err)
	}
	if len(rec.commands) != 0 {
		t.Errorf("commands = %v, want none", rec.commands)
	}
}
