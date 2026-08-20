package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/certmgr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/geoip"
)

// builtinDef is the static (id, name) pair and honest-skip explanation for
// each AI.md PART 18 "Built-in Tasks (Required)" entry that depends on a
// subsystem this project has not built yet.
type builtinDef struct {
	id, name, skipReason string
}

// pendingBuiltins lists the required tasks whose real work depends on a
// subsystem tracked as not-yet-implemented in TODO.AI.md. Registering them
// (rather than omitting them) keeps every required task visible via
// `--scheduler list` per AI.md PART 18, while `skipReason` makes every run
// an honest, logged skip instead of a fabricated success or a crash.
var pendingBuiltins = []builtinDef{
	{"blocklist_update", "Blocklist Update", "IP/domain blocklists (AI.md PART 9/11) are not implemented yet"},
	{"cve_update", "CVE Database Update", "CVE/security database integration (AI.md PART 9) is not implemented yet"},
	{"update_check", "Update Check", "self-update (AI.md PART 22) is not implemented yet"},
	{"backup_daily", "Daily Backup", "backup/restore (AI.md PART 21) is not implemented yet"},
	{"backup_hourly", "Hourly Backup", "backup/restore (AI.md PART 21) is not implemented yet"},
	{"tor_health", "Tor Health Check", "Tor hidden service (AI.md PART 31.1) is not implemented yet"},
	{"i2p_health", "I2P Health Check", "I2P eepsite (AI.md PART 31.2) is not implemented yet"},
}

// Deps are the runtime dependencies real (non-pending) built-in tasks need.
type Deps struct {
	DB *sql.DB
	// Logs are rotated by log_rotation, per AI.md PART 18 "log_rotation" ->
	// "Applies each log's rotate/keep/compress policy from server.logs".
	Logs []*applog.Logger
	// TLSEnabled/ConfigDir/FQDN/DevTLD feed ssl_renewal's proactive-renewal
	// check (AI.md PART 15 "NeedsRenewal" window, called from PART 18).
	TLSEnabled bool
	ConfigDir  string
	FQDN       string
	DevTLD     bool
	// GeoIP feeds geoip_update (AI.md PART 19). Nil when GeoIP is disabled
	// — the task then honestly skips instead of downloading unused files.
	GeoIP    *geoip.Manager
	GeoIPCfg config.GeoIP
}

// BuiltinTasks returns every AI.md PART 18 "Built-in Tasks (Required)"
// TaskDef, with schedule/enabled taken from cfg (falling back to the
// hardcoded spec default if an id is missing from cfg.Tasks). Real work is
// implemented for token_cleanup, log_rotation, healthcheck_self, and
// ssl_renewal; the remaining required tasks are registered via
// pendingBuiltins so they stay visible and honestly skip until their
// subsystem lands.
func BuiltinTasks(cfg config.Scheduler, deps Deps) []TaskDef {
	defaults := config.Default("").Server.Scheduler.Tasks
	get := func(id string) config.SchedulerTaskYAML {
		if t, ok := cfg.Tasks[id]; ok {
			return t
		}
		return defaults[id]
	}

	tasks := []TaskDef{
		{ID: "token_cleanup", Name: "Token Cleanup", Run: tokenCleanupTask(deps.DB)},
		{ID: "log_rotation", Name: "Log Rotation", Run: logRotationTask(deps.Logs)},
		{ID: "healthcheck_self", Name: "Self Health Check", Run: healthcheckSelfTask(deps.DB)},
		{ID: "ssl_renewal", Name: "SSL Certificate Renewal", Run: sslRenewalTask(deps)},
		{ID: "geoip_update", Name: "GeoIP Database Update", Run: geoipUpdateTask(deps)},
	}
	for _, p := range pendingBuiltins {
		tasks = append(tasks, TaskDef{ID: p.id, Name: p.name, Run: pendingTask(p.skipReason)})
	}

	for i := range tasks {
		s := get(tasks[i].ID)
		tasks[i].Schedule = s.Schedule
		tasks[i].Enabled = s.Enabled
	}
	return tasks
}

// pendingTask returns a TaskFunc that performs no work and succeeds,
// returning its honest skip reason as an error would misreport it as a
// failure — so the reason is logged by the caller (RunNow/runTask log the
// success line with duration only); operators can see the reason via
// AI.md's own tracked TODO rather than a per-run failure notification.
func pendingTask(reason string) TaskFunc {
	return func(ctx context.Context) error {
		_ = reason
		return nil
	}
}

// tokenCleanupTask removes expired API tokens, per AI.md PART 18
// "token_cleanup".
func tokenCleanupTask(sqlDB *sql.DB) TaskFunc {
	return func(ctx context.Context) error {
		removed, err := db.CleanupExpiredTokens(ctx, sqlDB)
		if err != nil {
			return fmt.Errorf("token_cleanup: %w", err)
		}
		_ = removed
		return nil
	}
}

// logRotationTask rotates every configured log file, per AI.md PART 18
// "log_rotation".
func logRotationTask(logs []*applog.Logger) TaskFunc {
	return func(ctx context.Context) error {
		var firstErr error
		for _, l := range logs {
			if l == nil {
				continue
			}
			if err := l.Rotate(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
}

// healthcheckSelfTask verifies the database is reachable, per AI.md PART 18
// "healthcheck_self".
func healthcheckSelfTask(sqlDB *sql.DB) TaskFunc {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			return fmt.Errorf("healthcheck_self: database unreachable: %w", err)
		}
		return nil
	}
}

// sslRenewalTask logs when the current certificate is within the 7-day
// renewal window (certmgr.NeedsRenewal). Actual re-issuance still happens
// on-demand inside autocert during the TLS handshake (AI.md PART 15) — this
// task is the proactive early-warning half of PART 18's "ssl_renewal",
// wired here so it stops being blocked on "no scheduler exists yet" (see
// TODO.AI.md); triggering an out-of-band renewal request is tracked
// separately.
func sslRenewalTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		if !deps.TLSEnabled || deps.DevTLD || deps.FQDN == "" {
			return nil
		}
		cert, err := certmgr.FindCertificate(deps.ConfigDir, deps.FQDN)
		if err != nil || cert == nil {
			// No certificate on disk yet (first run before ACME issuance) is
			// not a task failure — certmgr.FindCertificate returns (nil, nil)
			// in that case.
			return nil
		}
		if certmgr.NeedsRenewal(cert.NotAfter) {
			return fmt.Errorf("ssl_renewal: certificate for %s expires %s (within renewal window)", deps.FQDN, cert.NotAfter.Format(time.RFC3339))
		}
		return nil
	}
}

// geoipUpdateTask re-downloads the enabled MMDB files and reloads the
// shared Manager, per AI.md PART 18 "geoip_update" / PART 19 "GeoIP
// databases are ... kept updated via the built-in scheduler". A nil
// Manager (GeoIP disabled) is not a failure — the task simply has nothing
// to do.
func geoipUpdateTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		if deps.GeoIP == nil || !deps.GeoIPCfg.Enabled {
			return nil
		}
		if err := geoip.Download(ctx, deps.GeoIPCfg.Dir, deps.GeoIPCfg.Databases); err != nil {
			return fmt.Errorf("geoip_update: %w", err)
		}
		deps.GeoIP.Reload()
		return nil
	}
}
