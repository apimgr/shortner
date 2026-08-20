package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/paths"
)

func TestDetectServiceManagerContainer(t *testing.T) {
	// This assertion is only meaningful when the test process is actually
	// inside a container; otherwise it exercises the remaining detection
	// branches (systemd/launchd/runit/s6/sysv/rcd/manual) based on the
	// real host, which is inherently environment dependent. Either way
	// the call must not panic and must return one of the known values.
	got := detectServiceManager()
	known := map[string]bool{
		"container": true, "systemd": true, "launchd": true, "runit": true,
		"s6": true, "sysv": true, "rcd": true, "manual": true,
	}
	if !known[got] {
		t.Errorf("detectServiceManager() = %q, not a known value", got)
	}
}

// testPaths is a throwaway path set rooted under the test's temp dir so
// nothing in these tests can touch real system directories.
func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	dir := t.TempDir()
	return paths.Paths{
		Binary:  dir + "/shortner",
		Config:  dir + "/config",
		Data:    dir + "/data",
		Cache:   dir + "/cache",
		Logs:    dir + "/logs",
		Backup:  dir + "/backup",
		PIDFile: dir + "/run/shortner.pid",
	}
}

func TestRunServiceHelp(t *testing.T) {
	for _, cmd := range []string{"", "help", "--help", "-h", "status"} {
		t.Run("cmd="+cmd, func(t *testing.T) {
			p := testPaths(t)
			out, _, code := captureOutput(t, func() int { return runService("shortner", p, cmd) })
			if code != 0 {
				t.Errorf("code = %d, want 0", code)
			}
			// AI.md PART 23 "Service Help Output" — the exact block.
			for _, want := range []string{
				"Service management commands:",
				"start                                 - Start the service",
				"stop                                  - Stop the service",
				"restart                               - Restart the service",
				"reload                                - Reload configuration without restart",
				"--install                              - Install, enable, and start service",
				"--disable                              - Stop and disable service (keeps data)",
				"--uninstall                            - Stop, disable, and remove everything (keeps binary)",
				"Current status:",
				"  Service:    ",
				"  State:      ",
				"  Auto-start: ",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\ngot:\n%s", want, out)
				}
			}
		})
	}
}

func TestRunServiceUnknownCommand(t *testing.T) {
	p := testPaths(t)
	_, stderr, code := captureOutput(t, func() int { return runService("shortner", p, "bogus") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --service command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestServiceNeedsPrivilege(t *testing.T) {
	for _, cmd := range []string{"start", "stop", "restart", "reload", "--install", "--disable", "--uninstall"} {
		if !serviceNeedsPrivilege(cmd) {
			t.Errorf("serviceNeedsPrivilege(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range []string{"", "bogus", "--help", "install"} {
		if serviceNeedsPrivilege(cmd) {
			t.Errorf("serviceNeedsPrivilege(%q) = true, want false", cmd)
		}
	}
}

func TestServiceParams(t *testing.T) {
	p := testPaths(t)
	params := serviceParams(p)
	if params.InternalName != paths.InternalName {
		t.Errorf("InternalName = %q, want %q", params.InternalName, paths.InternalName)
	}
	if params.InternalOrg != paths.InternalOrg {
		t.Errorf("InternalOrg = %q, want %q", params.InternalOrg, paths.InternalOrg)
	}
	if params.ConfigDir != p.Config || params.DataDir != p.Data || params.LogDir != p.Logs {
		t.Errorf("params do not mirror the resolved paths: %+v", params)
	}
	if params.BinaryPath == "" {
		t.Error("BinaryPath is empty")
	}
	// io.github.{project_org}.{internal_name} per AI.md PART 24 launchd.
	if want := "io.github.apimgr.shortner"; params.PlistName() != want {
		t.Errorf("PlistName() = %q, want %q", params.PlistName(), want)
	}
}

// TestRunServiceUninstallCancelled proves the AI.md PART 23 confirmation
// gate: answering anything but yes must abort before any destructive
// step runs.
func TestRunServiceUninstallCancelled(t *testing.T) {
	for _, answer := range []string{"n", "", "no", "maybe"} {
		t.Run("answer="+answer, func(t *testing.T) {
			original := promptLine
			t.Cleanup(func() { promptLine = original })

			var asked string
			promptLine = func(prompt string) (string, error) {
				asked = prompt
				if answer == "" {
					return "", errors.New("unexpected newline")
				}
				return answer, nil
			}

			p := testPaths(t)
			_, stderr, code := captureOutput(t, func() int { return runService("shortner", p, "--uninstall") })
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			if !strings.Contains(stderr, "uninstall cancelled") {
				t.Errorf("stderr = %q, want cancellation message", stderr)
			}
			want := "This will delete ALL data, configs, and the system user. Continue? [y/N]"
			if !strings.Contains(asked, want) {
				t.Errorf("prompt = %q, want to contain %q", asked, want)
			}
		})
	}
}
