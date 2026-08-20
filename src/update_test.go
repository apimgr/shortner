package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/updater"
)

// updatePaths builds a throwaway Paths rooted in a temp dir so the update
// subcommands read and write only test-owned files.
func updatePaths(t *testing.T) paths.Paths {
	t.Helper()
	dir := t.TempDir()
	return paths.Paths{
		Config:     dir,
		ConfigFile: filepath.Join(dir, "server.yml"),
		Data:       dir,
		DB:         dir,
		PIDFile:    filepath.Join(dir, "does-not-exist.pid"),
	}
}

// stubUpdater swaps the updater entry points for the duration of a test so
// the check/install flows run with no network call and no binary
// replacement.
func stubUpdater(t *testing.T, release *updater.Release, checkErr error) {
	t.Helper()
	prevCheck, prevInstall, prevRestart := checkUpdate, installUpdate, restartServer
	checkUpdate = func(ctx context.Context, current, branch string, epoch int64) (*updater.Release, error) {
		return release, checkErr
	}
	installUpdate = func(ctx context.Context, r *updater.Release) error { return nil }
	restartServer = func() error { return nil }
	t.Cleanup(func() { checkUpdate, installUpdate, restartServer = prevCheck, prevInstall, prevRestart })
}

func TestRunUpdateCheck(t *testing.T) {
	p := updatePaths(t)
	stubUpdater(t, &updater.Release{TagName: "v9.9.9"}, nil)

	out, _, code := captureOutput(t, func() int { return runUpdate("shortner", p, "check", "") })
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	for _, want := range []string{"Current version: ", "Update channel:  stable", "Update available: v9.9.9"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}
	// The result is cached so --status can report it without a second call.
	if got := updater.LoadState(updater.StatePath(p.Data)).AvailableVersion; got != "v9.9.9" {
		t.Errorf("cached version = %q, want v9.9.9", got)
	}
}

func TestRunUpdateCheckAlreadyCurrent(t *testing.T) {
	p := updatePaths(t)
	stubUpdater(t, nil, nil)

	out, _, code := captureOutput(t, func() int { return runUpdate("shortner", p, "check", "") })
	if code != 0 {
		t.Errorf("code = %d, want 0 (AI.md PART 22: no update available is exit 0)", code)
	}
	if !strings.Contains(out, "No updates available") {
		t.Errorf("output = %q, want the already-current message", out)
	}
}

func TestRunUpdateCheckError(t *testing.T) {
	p := updatePaths(t)
	stubUpdater(t, nil, errors.New("github is down"))

	_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", p, "check", "") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "github is down") {
		t.Errorf("stderr = %q, want the underlying error", stderr)
	}
}

func TestRunUpdateInstall(t *testing.T) {
	p := updatePaths(t)
	stubUpdater(t, &updater.Release{TagName: "v9.9.9"}, nil)
	installed := ""
	installUpdate = func(ctx context.Context, r *updater.Release) error {
		installed = r.TagName
		return nil
	}

	// Both a bare `--update` and `--update yes` install (AI.md PART 22).
	for _, cmd := range []string{"", "yes"} {
		t.Run("cmd="+cmd, func(t *testing.T) {
			installed = ""
			out, _, code := captureOutput(t, func() int { return runUpdate("shortner", p, cmd, "") })
			if code != 0 {
				t.Fatalf("code = %d, want 0", code)
			}
			if installed != "v9.9.9" {
				t.Errorf("installed = %q, want v9.9.9", installed)
			}
			if !strings.Contains(out, "Verified checksum and installed v9.9.9") {
				t.Errorf("output = %q, want the install confirmation", out)
			}
			// No server is running (the PID file does not exist), so the
			// operator is told to start it rather than a restart being faked.
			if !strings.Contains(out, "Start the server to run the new version.") {
				t.Errorf("output = %q, want the start-the-server hint", out)
			}
		})
	}
}

func TestRunUpdateInstallAlreadyCurrent(t *testing.T) {
	p := updatePaths(t)
	stubUpdater(t, nil, nil)
	installUpdate = func(ctx context.Context, r *updater.Release) error {
		t.Fatal("the binary was replaced with no update available")
		return nil
	}

	out, _, code := captureOutput(t, func() int { return runUpdate("shortner", p, "yes", "") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "is already current (channel stable)") {
		t.Errorf("output = %q, want the already-current message", out)
	}
}

func TestRunUpdateInstallFailure(t *testing.T) {
	p := updatePaths(t)
	stubUpdater(t, &updater.Release{TagName: "v9.9.9"}, nil)
	installUpdate = func(ctx context.Context, r *updater.Release) error {
		return errors.New("checksum mismatch")
	}

	_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", p, "yes", "") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "checksum mismatch") {
		t.Errorf("stderr = %q, want the verification failure", stderr)
	}
}

func TestRunUpdateHelp(t *testing.T) {
	p := updatePaths(t)
	for _, cmd := range []string{"help", "--help", "-h"} {
		t.Run(cmd, func(t *testing.T) {
			out, _, code := captureOutput(t, func() int { return runUpdate("shortner", p, cmd, "") })
			if code != 0 {
				t.Errorf("code = %d, want 0", code)
			}
			for _, want := range []string{
				"check ",
				"yes ",
				"branch <name>",
				"shortner --update branch beta",
				"Branch:   stable",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output = %q, want it to contain %q", out, want)
				}
			}
		})
	}
}

func TestRunUpdateHelpShowsCachedLatest(t *testing.T) {
	p := updatePaths(t)
	state := updater.State{Branch: "beta", AvailableVersion: "v9.9.9"}
	if err := updater.SaveState(updater.StatePath(p.Data), state); err != nil {
		t.Fatalf("setup: %v", err)
	}

	out, _, code := captureOutput(t, func() int { return runUpdate("shortner", p, "help", "") })
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "Latest:   v9.9.9") {
		t.Errorf("output = %q, want cached latest version", out)
	}
}

func TestRunUpdateBranchPersists(t *testing.T) {
	p := updatePaths(t)

	out, _, code := captureOutput(t, func() int { return runUpdate("shortner", p, "branch", "beta") })
	if code != 0 {
		t.Fatalf("code = %d, want 0 (stderr-free branch switch); out = %q", code, out)
	}
	if !strings.Contains(out, "Update branch set to beta") {
		t.Errorf("output = %q, want confirmation message", out)
	}

	data, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "branch: beta") {
		t.Errorf("server.yml = %q, want branch: beta persisted", string(data))
	}

	// A second switch to the same channel is a no-op, not an error.
	out, _, code = captureOutput(t, func() int { return runUpdate("shortner", p, "branch", "beta") })
	if code != 0 {
		t.Errorf("code = %d, want 0 for an already-set branch", code)
	}
	if !strings.Contains(out, "already beta") {
		t.Errorf("output = %q, want already-set message", out)
	}
}

func TestRunUpdateBranchInvalid(t *testing.T) {
	p := updatePaths(t)
	for _, name := range []string{"", "nightly", "STABLE "} {
		t.Run("branch="+name, func(t *testing.T) {
			_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", p, "branch", name) })
			if code != 1 {
				t.Errorf("code = %d, want 1", code)
			}
			if !strings.Contains(stderr, "requires stable, beta, or daily") {
				t.Errorf("stderr = %q, want the valid-branch message", stderr)
			}
		})
	}
	if _, err := os.Stat(p.ConfigFile); !os.IsNotExist(err) {
		t.Errorf("server.yml was written for an invalid branch (err = %v)", err)
	}
}

func TestRunUpdateUnknownCommand(t *testing.T) {
	p := updatePaths(t)
	_, stderr, code := captureOutput(t, func() int { return runUpdate("shortner", p, "bogus", "") })
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown --update command "bogus"`) {
		t.Errorf("stderr = %q, want unknown-command message", stderr)
	}
}

func TestRecordUpdateState(t *testing.T) {
	p := updatePaths(t)

	release := &updater.Release{TagName: "v1.2.3"}
	recordUpdateState(p, "beta", release)
	state := updater.LoadState(updater.StatePath(p.Data))
	if state.Branch != "beta" || state.AvailableVersion != "v1.2.3" {
		t.Fatalf("state = %+v, want branch beta / v1.2.3", state)
	}
	if state.NotifiedKey != release.Key() {
		t.Errorf("NotifiedKey = %q, want %q", state.NotifiedKey, release.Key())
	}
	if state.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero, want the check timestamp")
	}

	// A later check that finds nothing must clear the stale marker so
	// --status stops advertising an already-installed version.
	recordUpdateState(p, "beta", nil)
	if got := updater.LoadState(updater.StatePath(p.Data)).AvailableVersion; got != "" {
		t.Errorf("AvailableVersion = %q, want it cleared", got)
	}
}

func TestPrintUpdateHelpUsesConfiguredBranch(t *testing.T) {
	p := updatePaths(t)
	if _, _, code := captureOutput(t, func() int { return runUpdateBranch("shortner", p, "daily") }); code != 0 {
		t.Fatalf("runUpdateBranch code = %d, want 0", code)
	}

	out, _, _ := captureOutput(t, func() int { printUpdateHelp("shortner", p); return 0 })
	if !strings.Contains(out, "Branch:   daily") {
		t.Errorf("output = %q, want the configured branch", out)
	}
	if !strings.Contains(out, "Version:  "+version.String()) {
		t.Errorf("output = %q, want the running version", out)
	}
}
