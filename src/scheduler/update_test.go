package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/updater"
)

// stubUpdater swaps the updater entry points for the duration of a test so
// the task's decision logic runs with no network call and no binary
// replacement. Install and restart default to succeeding no-ops; a test
// that cares reassigns them afterwards.
func stubUpdater(t *testing.T, release *updater.Release, checkErr error) {
	t.Helper()
	prevCheck, prevDo, prevRestart := checkEligible, doUpdate, restart
	checkEligible = func(ctx context.Context, current, branch string, epoch int64, deferDays int, now time.Time) (*updater.Release, error) {
		return release, checkErr
	}
	doUpdate = func(ctx context.Context, r *updater.Release) error { return nil }
	restart = func() error { return nil }
	t.Cleanup(func() { checkEligible, doUpdate, restart = prevCheck, prevDo, prevRestart })
}

// newUpdateDeps builds a configured UpdateDeps writing its state and log
// into a temp dir, plus a reader for the log contents.
func newUpdateDeps(t *testing.T, cfg config.Update) (Deps, string, func() string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "server.log")
	logger, err := applog.Open(logPath, applog.LevelInfo)
	if err != nil {
		t.Fatalf("setup log: %v", err)
	}
	statePath := updater.StatePath(dir)
	deps := Deps{Update: UpdateDeps{
		Cfg:            cfg,
		CurrentVersion: "v1.0.0",
		BuildEpoch:     time.Now().Add(-48 * time.Hour).Unix(),
		StatePath:      statePath,
		Log:            logger,
	}}
	readLog := func() string {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(data)
	}
	return deps, statePath, readLog
}

func TestUpdateCheckTaskInertWhenUnconfigured(t *testing.T) {
	stubUpdater(t, &updater.Release{TagName: "v2.0.0"}, nil)

	tests := []struct {
		name string
		deps UpdateDeps
	}{
		{"zero value", UpdateDeps{}},
		{"no version", UpdateDeps{StatePath: "/nonexistent/update.json"}},
		{"development build", UpdateDeps{CurrentVersion: "devel", StatePath: "/nonexistent/update.json"}},
		{"no state path", UpdateDeps{CurrentVersion: "v1.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := updateCheckTask(Deps{Update: tt.deps})(context.Background()); err != nil {
				t.Errorf("task error = %v, want nil (inert)", err)
			}
		})
	}
}

func TestUpdateCheckTaskNotifiesOncePerVersion(t *testing.T) {
	release := &updater.Release{TagName: "v2.0.0", PublishedAt: time.Now().UTC().Add(-72 * time.Hour)}
	stubUpdater(t, release, nil)
	deps, statePath, readLog := newUpdateDeps(t, config.Update{Branch: "stable"})

	task := updateCheckTask(deps)
	if err := task(context.Background()); err != nil {
		t.Fatalf("task error = %v", err)
	}
	state := updater.LoadState(statePath)
	if state.AvailableVersion != "v2.0.0" {
		t.Errorf("AvailableVersion = %q, want v2.0.0", state.AvailableVersion)
	}
	if state.NotifiedKey != "v2.0.0" {
		t.Errorf("NotifiedKey = %q, want v2.0.0", state.NotifiedKey)
	}
	if state.Branch != "stable" || state.CheckedAt.IsZero() {
		t.Errorf("state = %+v, want branch and check time recorded", state)
	}
	if got := strings.Count(readLog(), "update available"); got != 1 {
		t.Fatalf("notification count = %d, want 1", got)
	}

	// AI.md PART 22 "Surfacing rules": the notice fires when a version is
	// first seen, never re-sent on every task run.
	if err := task(context.Background()); err != nil {
		t.Fatalf("second run error = %v", err)
	}
	if got := strings.Count(readLog(), "update available"); got != 1 {
		t.Errorf("notification count after a second run = %d, want 1", got)
	}
}

func TestUpdateCheckTaskClearsStaleMarker(t *testing.T) {
	stubUpdater(t, nil, nil)
	deps, statePath, _ := newUpdateDeps(t, config.Update{Branch: "stable"})
	if err := updater.SaveState(statePath, updater.State{AvailableVersion: "v1.0.0", NotifiedKey: "v1.0.0"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := updateCheckTask(deps)(context.Background()); err != nil {
		t.Fatalf("task error = %v", err)
	}
	if got := updater.LoadState(statePath).AvailableVersion; got != "" {
		t.Errorf("AvailableVersion = %q, want it cleared once the update is installed", got)
	}
}

func TestUpdateCheckTaskNoAutoInstallByDefault(t *testing.T) {
	release := &updater.Release{TagName: "v2.0.0"}
	stubUpdater(t, release, nil)
	installed := false
	doUpdate = func(ctx context.Context, r *updater.Release) error {
		installed = true
		return nil
	}
	deps, _, _ := newUpdateDeps(t, config.Update{Branch: "stable", AutoInstall: false})

	if err := updateCheckTask(deps)(context.Background()); err != nil {
		t.Fatalf("task error = %v", err)
	}
	if installed {
		t.Error("the binary was replaced with auto_install off; the task must notify only")
	}
}

func TestUpdateCheckTaskAutoInstall(t *testing.T) {
	release := &updater.Release{TagName: "v2.0.0"}
	stubUpdater(t, release, nil)
	var installedTag string
	restarted := false
	doUpdate = func(ctx context.Context, r *updater.Release) error {
		installedTag = r.TagName
		return nil
	}
	restart = func() error {
		restarted = true
		return nil
	}
	deps, _, readLog := newUpdateDeps(t, config.Update{Branch: "daily", AutoInstall: true})

	if err := updateCheckTask(deps)(context.Background()); err != nil {
		t.Fatalf("task error = %v", err)
	}
	if installedTag != "v2.0.0" {
		t.Errorf("installed tag = %q, want v2.0.0", installedTag)
	}
	if !restarted {
		t.Error("the service was not restarted after an auto-install")
	}
	if !strings.Contains(readLog(), "installed v2.0.0") {
		t.Errorf("log = %q, want the install line", readLog())
	}
}

func TestUpdateCheckTaskPropagatesErrors(t *testing.T) {
	want := errors.New("github is down")
	stubUpdater(t, nil, want)
	deps, _, _ := newUpdateDeps(t, config.Update{Branch: "stable"})

	if err := updateCheckTask(deps)(context.Background()); !errors.Is(err, want) {
		t.Errorf("task error = %v, want %v", err, want)
	}

	// An install failure must surface too, rather than being swallowed.
	stubUpdater(t, &updater.Release{TagName: "v2.0.0"}, nil)
	installErr := errors.New("checksum mismatch")
	doUpdate = func(ctx context.Context, r *updater.Release) error { return installErr }
	deps, _, _ = newUpdateDeps(t, config.Update{Branch: "stable", AutoInstall: true})
	if err := updateCheckTask(deps)(context.Background()); !errors.Is(err, installErr) {
		t.Errorf("task error = %v, want %v", err, installErr)
	}
}
