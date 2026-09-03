package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/updater"
)

// UpdateDeps carries everything the AI.md PART 22 `update_check` task
// needs. A zero value leaves the task registered but inert.
type UpdateDeps struct {
	Cfg config.Update
	// CurrentVersion is the running build's version string; empty (or the
	// non-release "devel" placeholder) disables the task, since a
	// development build has no release to compare against.
	CurrentVersion string
	// BuildEpoch is the running build's timestamp, used to detect a newer
	// nightly on the rolling `daily` tag.
	BuildEpoch int64
	// StatePath is the update state file (data dir), which makes the
	// "update available" notice fire once per version rather than on
	// every run.
	StatePath string
	// Log receives the operator-facing WARN line. Nil disables it.
	Log *applog.Logger
}

// The updater entry points are indirected through package variables so
// tests can exercise the task's decision logic (defer window, once-per-
// version notification, auto_install) without a network call or a real
// binary replacement.
var (
	checkEligible = updater.CheckEligible
	doUpdate      = updater.DoUpdate
	restart       = updater.Restart
)

// configured reports whether enough is known to run a check.
func (u UpdateDeps) configured() bool {
	return u.CurrentVersion != "" && u.CurrentVersion != "devel" && u.StatePath != ""
}

// updateCheckTask implements AI.md PART 22 "Scheduled Check (update_check
// Task)": it runs the equivalent of `--update check` against the
// configured channel, filtered by `defer_days`, notifies operators once
// per newly seen eligible version, and installs it only when
// `auto_install` is on.
func updateCheckTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		u := deps.Update
		if !u.configured() {
			return nil
		}

		now := time.Now().UTC()
		release, err := checkEligible(ctx, u.CurrentVersion, u.Cfg.Branch, u.BuildEpoch, u.Cfg.DeferDays, now)
		if err != nil {
			return fmt.Errorf("update_check: %w", err)
		}

		state := updater.LoadState(u.StatePath)
		state.Branch = u.Cfg.Branch
		state.CheckedAt = now

		if release == nil {
			// Nothing eligible: clear any stale "available" marker so
			// --status stops advertising a version that is now installed.
			state.AvailableVersion = ""
			_ = updater.SaveState(u.StatePath, state)
			return nil
		}

		state.AvailableVersion = release.Version()
		// AI.md PART 22 "Surfacing rules": the WARN line is emitted when a
		// new eligible version is first seen, never re-sent per run.
		if state.NotifiedKey != release.Key() {
			state.NotifiedKey = release.Key()
			u.notify(deps.Notifier, release)
		}
		if err := updater.SaveState(u.StatePath, state); err != nil {
			return fmt.Errorf("update_check: %w", err)
		}

		if !u.Cfg.AutoInstall {
			return nil
		}
		if err := doUpdate(ctx, release); err != nil {
			return fmt.Errorf("update_check: %w", err)
		}
		u.log(applog.LevelWarn, fmt.Sprintf("update_check: installed %s, restarting", release.Version()))
		// Sent before the restart, because the restart replaces this
		// process and no code after it is guaranteed to run.
		_ = deps.Notifier.Send(notify.EventUpdateInstalled, map[string]string{
			"previous_version": u.CurrentVersion,
			"new_version":      release.Version(),
		})
		if err := restart(); err != nil {
			return fmt.Errorf("update_check: restart after update: %w", err)
		}
		return nil
	}
}

// notify emits the operator-only "update available" WARN line plus the
// matching AI.md PART 17 `update_available` email event. Both channels are
// operator-only — nothing here ever reaches a public endpoint, per PART
// 22's surfacing rules.
func (u UpdateDeps) notify(n *notify.Notifier, release *updater.Release) {
	u.log(applog.LevelWarn, fmt.Sprintf("update_check: update available: %s -> %s (channel %s)",
		u.CurrentVersion, release.Version(), u.Cfg.Branch))
	_ = n.Send(notify.EventUpdateAvailable, map[string]string{
		"current_version": u.CurrentVersion,
		"new_version":     release.Version(),
		"channel":         u.Cfg.Branch,
	})
}

// log writes one line to the scheduler log, if one was provided.
func (u UpdateDeps) log(level applog.Level, msg string) {
	if u.Log == nil {
		return
	}
	_ = u.Log.WriteLine(level, applog.FormatText(time.Now(), level.String(), msg))
}
