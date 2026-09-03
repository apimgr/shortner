package service

import (
	"errors"
	"testing"
)

// fakeProbe builds a probe whose PATH and filesystem answers come from
// the given sets, so the AI.md PART 24 detection rules can be exercised
// without a live init system.
func fakeProbe(goos string, binaries, files []string) probe {
	bin := map[string]bool{}
	for _, b := range binaries {
		bin[b] = true
	}
	fileSet := map[string]bool{}
	for _, f := range files {
		fileSet[f] = true
	}
	return probe{
		goos: goos,
		lookPath: func(name string) (string, error) {
			if bin[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		exists: func(path string) bool { return fileSet[path] },
	}
}

func TestProbeDetect(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		binaries []string
		files    []string
		want     InitSystem
	}{
		{
			name: "macOS is always launchd",
			goos: "darwin",
			want: Launchd,
		},
		{
			name: "Windows is always the SCM",
			goos: "windows",
			want: WindowsService,
		},
		{
			name: "FreeBSD is rc.d",
			goos: "freebsd",
			want: RCd,
		},
		{
			name: "OpenBSD is rc.d",
			goos: "openbsd",
			want: RCd,
		},
		{
			name:     "systemd wins over everything else on Linux",
			goos:     "linux",
			binaries: []string{"systemctl", "openrc-run", "sv", "update-rc.d"},
			files:    []string{"/run/systemd/system", "/etc/init.d"},
			want:     Systemd,
		},
		{
			name:     "OpenRC when openrc-run exists and systemd does not",
			goos:     "linux",
			binaries: []string{"openrc-run", "rc-update", "update-rc.d"},
			files:    []string{"/etc/init.d"},
			want:     OpenRC,
		},
		{
			name:     "runit when only sv and the sv directory exist",
			goos:     "linux",
			binaries: []string{"sv"},
			files:    []string{"/etc/sv"},
			want:     Runit,
		},
		{
			name: "SysVinit only when neither openrc-run nor systemctl exist",
			goos: "linux",
			// PART 24: SysVinit is mutually exclusive with OpenRC, which
			// share /etc/init.d — the absence of both openrc-run and
			// systemctl is what makes this SysVinit.
			binaries: []string{"update-rc.d"},
			files:    []string{"/etc/init.d"},
			want:     SysVinit,
		},
		{
			name:     "chkconfig also qualifies as SysVinit",
			goos:     "linux",
			binaries: []string{"chkconfig"},
			files:    []string{"/etc/init.d"},
			want:     SysVinit,
		},
		{
			name:     "init.d without a management tool is not SysVinit",
			goos:     "linux",
			binaries: []string{},
			files:    []string{"/etc/init.d"},
			want:     UnknownInit,
		},
		{
			name: "a bare container has no init system",
			goos: "linux",
			want: UnknownInit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fakeProbe(tc.goos, tc.binaries, tc.files).detect()
			if got != tc.want {
				t.Errorf("detect() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectNameOnHost(t *testing.T) {
	// The host answer is environment dependent; the contract that must
	// hold everywhere is that it never panics and never returns "".
	if DetectName() == "" {
		t.Error("DetectName() returned an empty string")
	}
}
