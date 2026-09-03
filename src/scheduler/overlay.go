// The AI.md PART 18 overlay health tasks: tor_health and i2p_health, both
// every 10 minutes.
//
// Each manager already runs its own 30-second monitor loop (AI.md PART 31.1
// "Tor Monitoring", PART 31.2 "I2P Monitoring"), so these tasks are the
// slower, independent backstop the spec asks the scheduler to own: they
// catch the case where the monitor goroutine itself is gone, and they make
// the overlay's state visible in `--scheduler list` alongside every other
// required task.
package scheduler

import (
	"context"
	"errors"
)

// OverlayHealth is the health probe an overlay manager exposes. Both
// *tor.Manager and *i2p.Manager satisfy it, and both are nil-receiver safe,
// so the scheduler package never imports either one.
type OverlayHealth interface {
	Enabled() bool
	Running() bool
	Healthy() bool
	Restart() error
}

// torHealthTask probes the hidden service and restarts it when it stopped
// answering. Tor has no opt-out toggle (AI.md PART 31.1) — a host with no
// tor binary simply reports not-enabled, which is a skip, not a failure.
func torHealthTask(deps Deps) TaskFunc {
	return overlayHealthTask(deps.Tor, "tor_health")
}

// i2pHealthTask probes the eepsite. I2P is opt-in (AI.md PART 31.2), so a
// disabled manager skips without ever touching a provider.
func i2pHealthTask(deps Deps) TaskFunc {
	return overlayHealthTask(deps.I2P, "i2p_health")
}

// overlayHealthTask builds the shared probe-and-restart body. A manager
// that is enabled but not running is restarted too: that is exactly the
// state a crashed provider leaves behind.
func overlayHealthTask(manager OverlayHealth, taskID string) TaskFunc {
	return func(ctx context.Context) error {
		if manager == nil || !manager.Enabled() {
			return nil
		}
		if manager.Running() && manager.Healthy() {
			return nil
		}
		if err := manager.Restart(); err != nil {
			return errors.New(taskID + ": restart failed: " + err.Error())
		}
		return nil
	}
}
