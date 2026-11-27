package scheduler

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/backup"
	"github.com/apimgr/shortner/src/blocklist"
	"github.com/apimgr/shortner/src/certmgr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/cve"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/geoip"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/pgp"
)

// Deps are the runtime dependencies built-in tasks need.
type Deps struct {
	DB      db.Handle
	DBProbe interface{ Probe(context.Context) error }
	Tokens  config.Tokens
	// Logs are rotated by log_rotation, per AI.md PART 18 "log_rotation" ->
	// "Applies each log's rotate/keep/compress policy from server.logs".
	Logs                    []*applog.Logger
	AuditLog                *applog.AuditLogger
	DiskPaths               []string
	TempDir                 string
	TempPrefix              string
	MaintenanceCleanup      config.MaintenanceCleanup
	DiskLowThresholdPercent float64
	DiskLowRepeat           time.Duration
	DiskLowEnabled          bool
	DiskCheckInterval       string
	DatabaseError           config.DatabaseErrorNotification
	// TLSEnabled/ConfigDir/FQDN/DevTLD feed ssl_renewal's proactive-renewal
	// check (AI.md PART 15 "NeedsRenewal" window, called from PART 18).
	TLSEnabled                  bool
	ConfigDir                   string
	FQDN                        string
	DevTLD                      bool
	TLSConfig                   *tls.Config
	CVEOptions                  cve.Options
	SecurityReportRetentionDays int
	PGPStore                    *pgp.Store
	Keyservers                  []string
	// GeoIP feeds geoip_update (AI.md PART 19). Nil when GeoIP is disabled
	// — the task then honestly skips instead of downloading unused files.
	GeoIP      *geoip.Manager
	GeoIPCfg   config.GeoIP
	Blocklists *blocklist.Manager
	// Backup feeds backup_daily/backup_hourly (AI.md PART 21). A zero
	// value leaves both tasks registered but inert.
	Backup BackupDeps
	// Update feeds update_check (AI.md PART 22). A zero value leaves the
	// task registered but inert.
	Update UpdateDeps
	// Notifier raises the AI.md PART 17 email events a task owns
	// (backup_complete/backup_failed, ssl_expiring/ssl_renewal_failed,
	// update_available/update_installed). Nil is safe — every Notifier
	// method is inert on a nil receiver, which is exactly PART 17's
	// "no SMTP = no email, ever" behaviour.
	Notifier        *notify.Notifier
	Logger          *applog.Logger
	MaintainSecrets func(context.Context) error
	// Tor and I2P feed tor_health and i2p_health (AI.md PART 18, PART 31).
	// Nil means that overlay network is not configured at all, in which
	// case the task registers and honestly skips.
	Tor OverlayHealth
	I2P OverlayHealth
}

// BuiltinTasks returns every AI.md PART 18 required TaskDef, with schedule
// and enabled values from cfg (falling back to the spec defaults).
func BuiltinTasks(cfg config.Scheduler, deps Deps) []TaskDef {
	defaults := config.Default("").Server.Scheduler.Tasks
	get := func(id string) config.SchedulerTaskYAML {
		if t, ok := cfg.Tasks[id]; ok {
			return t
		}
		return defaults[id]
	}

	tasks := []TaskDef{
		{ID: "database_check", Name: "Database Connection Check", Run: databaseCheckTask(deps)},
		{ID: "token_cleanup", Name: "Token Cleanup", Run: tokenCleanupTask(deps.DB, deps.Tokens)},
		{ID: "security_cleanup", Name: "Security Report Cleanup", Run: securityCleanupTask(deps)},
		{ID: "log_rotation", Name: "Log Rotation", Run: logRotationTask(deps.Logs, deps.AuditLog)},
		{ID: "disk_check", Name: "Disk Space Check", Run: diskCheckTask(deps)},
		{ID: "healthcheck_self", Name: "Self Health Check", Run: healthcheckSelfTask(deps.DB)},
		{ID: "ssl_renewal", Name: "SSL Certificate Renewal", Run: sslRenewalTask(deps)},
		{ID: "geoip_update", Name: "GeoIP Database Update", Run: geoipUpdateTask(deps)},
		{ID: "cve_update", Name: "CVE Database Update", Run: cveUpdateTask(deps)},
		{ID: "backup_daily", Name: "Daily Backup", Run: backupDailyTask(deps)},
		{ID: "backup_hourly", Name: "Hourly Backup", Run: backupHourlyTask(deps)},
		{ID: "update_check", Name: "Update Check", Run: updateCheckTask(deps)},
		{ID: "keyserver_retry", Name: "PGP Keyserver Retry", Run: keyserverRetryTask(deps)},
		{ID: "blocklist_update", Name: "Blocklist Update", Run: blocklistUpdateTask(deps.Blocklists)},
	}
	if deps.Tor != nil && deps.Tor.Enabled() {
		tasks = append(tasks, TaskDef{ID: "tor_health", Name: "Tor Health Check", Run: torHealthTask(deps)})
	}
	if deps.I2P != nil && deps.I2P.Enabled() {
		tasks = append(tasks, TaskDef{ID: "i2p_health", Name: "I2P Health Check", Run: i2pHealthTask(deps)})
	}
	for i := range tasks {
		s := get(tasks[i].ID)
		tasks[i].Schedule = s.Schedule
		tasks[i].Enabled = s.Enabled
		tasks[i].RetryOnFail = s.RetryOnFail
		if s.RetryDelay != "" {
			if delay, err := time.ParseDuration(s.RetryDelay); err == nil {
				tasks[i].RetryDelay = delay
			}
		}
		if tasks[i].ID == "disk_check" && tasks[i].Schedule == "" {
			interval := deps.DiskCheckInterval
			if interval == "" {
				interval = "5m"
			}
			tasks[i].Schedule, tasks[i].Enabled = "@every "+interval, true
		}
		if tasks[i].ID == "database_check" {
			interval := deps.DatabaseError.CheckInterval
			if interval == "" {
				interval = "1m"
			}
			tasks[i].Schedule = "@every " + interval
			tasks[i].Enabled = tasks[i].Enabled && deps.DatabaseError.Enabled
		}
	}
	return tasks
}

type databaseProbeState struct {
	mu           sync.Mutex
	failures     int
	since        time.Time
	lastError    string
	lastNotified time.Time
}

var databaseCredentialPattern = regexp.MustCompile(`(?i)(password|passwd|token|auth_token|authtoken|secret)(\s*[:=]\s*|["']\s*:\s*["'])([^&\s,;"']+)`)
var databaseURLCredentialPattern = regexp.MustCompile(`(?i)(://)[^/@\s]+@`)

func redactDatabaseProbeError(err error) string {
	if err == nil {
		return "database probe failed"
	}
	message := databaseCredentialPattern.ReplaceAllString(err.Error(), "$1$2[redacted]")
	message = databaseURLCredentialPattern.ReplaceAllString(message, "$1[redacted]@")
	return strings.TrimSpace(message)
}

func databaseCheckTask(deps Deps) TaskFunc {
	state := &databaseProbeState{}
	return func(ctx context.Context) error {
		if !deps.DatabaseError.Enabled {
			return nil
		}
		probe := deps.DBProbe
		if probe == nil {
			if candidate, ok := deps.DB.(interface{ Probe(context.Context) error }); ok {
				probe = candidate
			}
		}
		var err error
		if probe != nil {
			err = probe.Probe(ctx)
		} else if deps.DB != nil {
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = deps.DB.PingContext(checkCtx)
			cancel()
		} else {
			err = fmt.Errorf("database handle unavailable")
		}
		now := time.Now().UTC()
		state.mu.Lock()
		if err == nil {
			recovered := state.failures > 0
			state.failures, state.since, state.lastError, state.lastNotified = 0, time.Time{}, "", time.Time{}
			state.mu.Unlock()
			if recovered && deps.Logger != nil {
				_ = deps.Logger.WriteLine(applog.LevelInfo, "Database connection recovered\n")
			}
			return nil
		}
		if state.failures == 0 {
			state.since = now
		}
		state.failures++
		state.lastError = redactDatabaseProbeError(err)
		threshold := deps.DatabaseError.Failures
		if threshold < 1 {
			threshold = 3
		}
		if threshold > state.failures {
			state.mu.Unlock()
			return nil
		}
		repeat := 24 * time.Hour
		if parsed, parseErr := time.ParseDuration(deps.DatabaseError.Repeat); parseErr == nil && parsed > 0 {
			repeat = parsed
		}
		if !state.lastNotified.IsZero() && now.Sub(state.lastNotified) < repeat {
			state.mu.Unlock()
			return nil
		}
		since := state.since.Format(time.RFC3339)
		lastError := state.lastError
		state.lastNotified = now
		state.mu.Unlock()
		if deps.Logger != nil {
			_ = deps.Logger.WriteLine(applog.LevelWarn, "Database connection problem: "+lastError+"\n")
		}
		if deps.Notifier != nil {
			_ = deps.Notifier.Send(notify.EventDatabaseError, map[string]string{"since": since, "error": lastError})
		}
		return nil
	}
}

func blocklistUpdateTask(manager *blocklist.Manager) TaskFunc {
	return func(ctx context.Context) error {
		if manager == nil {
			return fmt.Errorf("blocklist_update: manager unavailable")
		}
		return manager.Update(ctx)
	}
}

// keyserverRetryTask resumes persisted PGP submissions whose backoff has
// elapsed, per AI.md PART 11 and PART 18.
func keyserverRetryTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		if deps.PGPStore == nil || len(deps.Keyservers) == 0 || !deps.PGPStore.HasKeypair() {
			return nil
		}
		fingerprint, due, err := deps.PGPStore.DueKeyservers(time.Now().UTC())
		if err != nil {
			return fmt.Errorf("keyserver_retry: read retry state: %w", err)
		}
		configured := make(map[string]bool, len(deps.Keyservers))
		for _, keyserver := range deps.Keyservers {
			configured[keyserver] = true
		}
		pending := due[:0]
		for _, keyserver := range due {
			if configured[keyserver] {
				pending = append(pending, keyserver)
			}
		}
		if len(pending) == 0 {
			return nil
		}
		armored, err := deps.PGPStore.ReadPublicKey()
		if err != nil {
			return fmt.Errorf("keyserver_retry: read public key: %w", err)
		}
		results := pgp.Publish(ctx, nil, pending, armored, time.Now)
		if err := deps.PGPStore.SaveKeyserverState(fingerprint, results); err != nil {
			return fmt.Errorf("keyserver_retry: save retry state: %w", err)
		}
		var firstErr error
		published := map[string]time.Time{}
		for _, result := range results {
			if result.OK() {
				published[result.Keyserver] = result.At.UTC()
			} else if firstErr == nil {
				firstErr = fmt.Errorf("%s: %s", result.Keyserver, result.Err)
			}
		}
		if len(published) > 0 && deps.DB != nil {
			record, err := db.GetPGPKey(ctx, deps.DB, fingerprint)
			if err != nil {
				return fmt.Errorf("keyserver_retry: load key record: %w", err)
			}
			if record != nil {
				if record.KeyserversPublished == nil {
					record.KeyserversPublished = map[string]time.Time{}
				}
				for server, at := range published {
					record.KeyserversPublished[server] = at
				}
				if err := db.UpsertPGPKey(ctx, deps.DB, *record); err != nil {
					return fmt.Errorf("keyserver_retry: save key record: %w", err)
				}
			}
		}
		return firstErr
	}
}

// tokenCleanupTask preserves owner-visible expired linked tokens, per AI.md
// PART 8, and purges idle owners without resources when configured.
func tokenCleanupTask(sqlDB db.Handle, tokens config.Tokens) TaskFunc {
	return func(ctx context.Context) error {
		if _, err := db.CleanupExpiredTokens(ctx, sqlDB); err != nil {
			return fmt.Errorf("token_cleanup: %w", err)
		}
		if _, err := db.PurgeIdleOwners(ctx, sqlDB, tokens.IdlePurgeDays); err != nil {
			return fmt.Errorf("token_cleanup idle owners: %w", err)
		}
		return nil
	}
}

// logRotationTask rotates every configured log file, per AI.md PART 18
// "log_rotation".
func logRotationTask(logs []*applog.Logger, audit ...interface{ RotateIfDue(time.Time) error }) TaskFunc {
	return func(ctx context.Context) error {
		var firstErr error
		for _, l := range logs {
			if l == nil {
				continue
			}
			if err := l.RotateIfDue(time.Now()); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		for _, l := range audit {
			if l != nil {
				if err := l.RotateIfDue(time.Now()); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	}
}

// healthcheckSelfTask verifies the database is reachable, per AI.md PART 18
// "healthcheck_self".
func healthcheckSelfTask(sqlDB db.Handle) TaskFunc {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := sqlDB.PingContext(ctx); err != nil {
			return fmt.Errorf("healthcheck_self: database unreachable: %w", err)
		}
		return nil
	}
}

// sslRenewalTask renews app-managed certificates inside the seven-day
// window by invoking the configured TLS provider. System and local
// certificates remain outside the app's renewal ownership.
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
		if !cert.Tier.ManagesRenewal() || !certmgr.NeedsRenewal(cert.NotAfter) {
			return nil
		}
		if deps.TLSConfig == nil || deps.TLSConfig.GetCertificate == nil {
			return fmt.Errorf("ssl_renewal: no managed renewal provider is configured for %s", deps.FQDN)
		}
		updated, err := deps.TLSConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: deps.FQDN})
		if err != nil {
			notifySSL(deps, cert.NotAfter)
			return fmt.Errorf("ssl_renewal: renew %s: %w", deps.FQDN, err)
		}
		if updated == nil {
			return fmt.Errorf("ssl_renewal: renewal provider returned no certificate for %s", deps.FQDN)
		}
		return nil
	}
}

// notifySSL raises the AI.md PART 17 SSL event matching the certificate's
// state: `ssl_renewal_failed` once the certificate has actually lapsed
// (autocert's on-demand renewal has demonstrably not happened), otherwise
// `ssl_expiring` for the ordinary inside-the-window warning.
func notifySSL(deps Deps, notAfter time.Time) {
	now := time.Now().UTC()
	days := int(notAfter.UTC().Sub(now).Hours() / 24)
	vars := map[string]string{
		"fqdn":        deps.FQDN,
		"expires_in":  fmt.Sprintf("%d days", days),
		"expiry_date": notAfter.UTC().Format(time.RFC3339),
	}
	if days > 0 {
		_ = deps.Notifier.Send(notify.EventSSLExpiring, vars)
		return
	}
	vars["error"] = fmt.Sprintf("certificate for %s expired on %s and has not been renewed", deps.FQDN, notAfter.UTC().Format(time.RFC3339))
	// Renewal is attempted by autocert during the next TLS handshake, and
	// this task re-checks on its own schedule; naming the task's next run
	// would be misleading, so the handshake is named instead.
	vars["next_retry"] = "on the next TLS handshake"
	_ = deps.Notifier.Send(notify.EventSSLRenewalFailed, vars)
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

func cveUpdateTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		if deps.CVEOptions.SecurityDir == "" {
			return fmt.Errorf("cve_update: security data directory is not configured")
		}
		if err := cve.Update(ctx, deps.CVEOptions); err != nil {
			return fmt.Errorf("cve_update: %w", err)
		}
		return nil
	}
}

func securityCleanupTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		if deps.MaintainSecrets != nil {
			if err := deps.MaintainSecrets(ctx); err != nil {
				return fmt.Errorf("security_cleanup: maintain project secrets: %w", err)
			}
		}
		retentionDays := deps.SecurityReportRetentionDays
		if retentionDays <= 0 {
			retentionDays = 365
		}
		deleted, err := db.DeleteClosedSecurityReportsBefore(ctx, deps.DB, time.Now().UTC().AddDate(0, 0, -retentionDays))
		if err != nil {
			return err
		}
		retiredSecrets, err := db.CleanupExpiredPreviousSecrets(ctx, deps.DB)
		if err != nil {
			return fmt.Errorf("security_cleanup: retire previous secrets: %w", err)
		}
		rotationRemoved := false
		if deps.PGPStore != nil {
			unlock, lockErr := deps.PGPStore.LockExclusive()
			if lockErr != nil {
				return lockErr
			}
			rotationRemoved, err = deps.PGPStore.CleanupExpiredRotation(time.Now().UTC())
			unlock()
			if err != nil {
				return err
			}
		}
		if deps.Logger != nil && (deleted > 0 || rotationRemoved || retiredSecrets > 0) {
			_ = deps.Logger.WriteLine(applog.LevelInfo, fmt.Sprintf("security_cleanup: deleted %d expired reports; retired secrets=%d; retired PGP key removed=%t\n", deleted, retiredSecrets, rotationRemoved))
		}
		return nil
	}
}

type diskWarning struct {
	last   time.Time
	warned bool
}

var diskWarnings = struct {
	sync.Mutex
	volumes map[string]diskWarning
}{volumes: make(map[string]diskWarning)}

func diskCheckTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		if len(deps.DiskPaths) == 0 {
			return nil
		}
		threshold := deps.DiskLowThresholdPercent
		if threshold <= 0 || threshold > 100 {
			threshold = 85
		}
		repeat := deps.DiskLowRepeat
		if repeat <= 0 {
			repeat = 24 * time.Hour
		}
		now := time.Now().UTC()
		seen := make(map[string]bool)
		for _, path := range deps.DiskPaths {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			usage, err := disk.UsageWithContext(ctx, path)
			if err != nil {
				return fmt.Errorf("disk_check %s: %w", path, err)
			}
			volume := volumeKey(path)
			if seen[volume] {
				continue
			}
			seen[volume] = true
			diskWarnings.Lock()
			state := diskWarnings.volumes[volume]
			if cleanupThreshold := deps.MaintenanceCleanup.DiskThreshold; cleanupThreshold > 0 && usage.UsedPercent >= float64(cleanupThreshold) {
				if err := cleanupDiskSpace(ctx, deps, now); err != nil {
					return fmt.Errorf("disk_check cleanup: %w", err)
				}
			}
			if usage.UsedPercent >= threshold {
				shouldWarn := !state.warned || now.Sub(state.last) >= repeat
				if shouldWarn {
					state.warned, state.last = true, now
					diskWarnings.volumes[volume] = state
					diskWarnings.Unlock()
					if deps.Logger != nil {
						_ = deps.Logger.WriteLine(applog.LevelWarn, fmt.Sprintf("disk space low: path=%q used_percent=%.2f free_bytes=%d\n", path, usage.UsedPercent, usage.Free))
					}
					if deps.DiskLowEnabled {
						_ = deps.Notifier.Send(notify.EventDiskLow, map[string]string{"path": path, "used_percent": fmt.Sprintf("%.2f", usage.UsedPercent), "free_bytes": fmt.Sprint(usage.Free)})
					}
				} else {
					diskWarnings.volumes[volume] = state
					diskWarnings.Unlock()
				}
			} else if state.warned && usage.UsedPercent <= threshold-5 {
				delete(diskWarnings.volumes, volume)
				diskWarnings.Unlock()
				if deps.Logger != nil {
					_ = deps.Logger.WriteLine(applog.LevelInfo, fmt.Sprintf("disk space recovered: path=%q used_percent=%.2f free_bytes=%d\n", path, usage.UsedPercent, usage.Free))
				}
			} else {
				diskWarnings.Unlock()
			}
		}
		return nil
	}
}

func cleanupDiskSpace(ctx context.Context, deps Deps, now time.Time) error {
	retentionDays := deps.MaintenanceCleanup.LogRetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}
	for _, logger := range deps.Logs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := logger.PruneArchivesOlderThan(now, time.Duration(retentionDays)*24*time.Hour); err != nil {
			return err
		}
	}
	if deps.TempDir != "" {
		if err := removeExpiredTempEntries(deps.TempDir, deps.TempPrefix, now.Add(-24*time.Hour)); err != nil {
			return err
		}
	}
	if deps.Backup.Dir != "" {
		status, err := backup.Disk(deps.Backup.Dir)
		if err != nil {
			return err
		}
		deleted, err := backup.Apply(deps.Backup.Dir, deps.Backup.Prefix, deps.Backup.Policy(status))
		if err != nil {
			return err
		}
		if len(deleted) > 0 {
			remaining, _ := backup.List(deps.Backup.Dir, deps.Backup.Prefix)
			backup.AuditRetentionCleanup(deps.Backup.Audit, deleted, len(remaining), "disk-pressure cleanup applied configured retention policy")
		}
	}
	return nil
}

func removeExpiredTempEntries(dir, prefix string, cutoff time.Time) error {
	root, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat temporary directory: %w", err)
	}
	if !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("temporary root is not a real directory")
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read temporary directory: %w", err)
	}
	for _, entry := range entries {
		if prefix == "" || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat temporary entry: %w", err)
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove expired temporary entry: %w", err)
		}
	}
	return nil
}

func volumeKey(path string) string {
	clean := filepath.Clean(path)
	partitions, err := disk.Partitions(false)
	if err != nil {
		return clean
	}
	bestMount, device := "", ""
	for _, partition := range partitions {
		mount := filepath.Clean(partition.Mountpoint)
		rel, err := filepath.Rel(mount, clean)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && len(mount) > len(bestMount) {
			bestMount, device = mount, partition.Device
		}
	}
	if device != "" {
		return device
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return clean
}
