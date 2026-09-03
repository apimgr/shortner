// Init-system detection. See AI.md PART 23 "Service Installation Logic"
// step 1 and PART 24's per-manager "Installation path" / "Detection"
// notes — most importantly the SysVinit rule, which only applies when
// openrc-run and systemctl are both absent.
package service

import (
	"os"
	"os/exec"
	"runtime"
)

// InitSystem identifies a detected platform service manager.
type InitSystem string

// The service managers PART 24 requires support for.
const (
	Systemd        InitSystem = "systemd"
	OpenRC         InitSystem = "openrc"
	SysVinit       InitSystem = "sysvinit"
	Runit          InitSystem = "runit"
	RCd            InitSystem = "rcd"
	Launchd        InitSystem = "launchd"
	WindowsService InitSystem = "windows"
	UnknownInit    InitSystem = "unknown"
)

// probe abstracts the PATH and filesystem lookups the detection rules
// depend on, so those rules are testable without a live init system.
type probe struct {
	goos     string
	lookPath func(string) (string, error)
	exists   func(string) bool
}

// hostProbe probes the real machine.
func hostProbe() probe {
	return probe{
		goos:     runtime.GOOS,
		lookPath: exec.LookPath,
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
	}
}

// has reports whether a binary is on PATH.
func (p probe) has(name string) bool {
	if p.lookPath == nil {
		return false
	}
	_, err := p.lookPath(name)
	return err == nil
}

// Detect returns the service manager for the running host.
func Detect() InitSystem {
	return hostProbe().detect()
}

// DetectName returns the detected service manager as a display string.
func DetectName() string {
	return string(Detect())
}

// detect implements PART 23 step 1's platform split and, for Linux, the
// systemd → OpenRC → runit → SysVinit precedence. SysVinit is last
// because PART 24 only permits it when openrc-run and systemctl are both
// absent and /etc/init.d exists with a working update-rc.d or chkconfig.
func (p probe) detect() InitSystem {
	switch p.goos {
	case "darwin":
		return Launchd
	case "windows":
		return WindowsService
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return RCd
	}

	hasSystemctl := p.has("systemctl")
	hasOpenRCRun := p.exists("/sbin/openrc-run") || p.exists("/usr/sbin/openrc-run") || p.has("openrc-run")

	if hasSystemctl || p.exists("/run/systemd/system") {
		return Systemd
	}
	if hasOpenRCRun || p.has("rc-service") {
		return OpenRC
	}
	if p.exists("/etc/runit") || p.exists("/etc/sv") || p.has("runsvdir") {
		return Runit
	}
	// PART 24 SysVinit detection, verbatim: openrc-run absent, systemctl
	// absent, /etc/init.d present, and update-rc.d or chkconfig working.
	if !hasOpenRCRun && !hasSystemctl && p.exists("/etc/init.d") &&
		(p.has("update-rc.d") || p.has("chkconfig")) {
		return SysVinit
	}
	return UnknownInit
}
