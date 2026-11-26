package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/backup"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/notify"
)

// BackupDeps carries everything the AI.md PART 21 backup tasks need. Dir is
// the backup directory resolved and cached at startup step 7 — PART 21
// "Backup Cleanup Logic" requires it never be re-resolved at run time.
type BackupDeps struct {
	Dir        string
	Prefix     string
	ConfigFile string
	DBPath     string
	ConfigDir  string
	DataDir    string
	AppVersion string

	Cfg config.Backup
	// Compliance mirrors server.compliance.enabled: when set, an
	// unencrypted backup is refused outright.
	Compliance bool
	// Audit receives the PART 21 audit events. Nil disables auditing.
	Audit backup.Auditor
}

// configured reports whether enough is known to attempt a backup.
func (b BackupDeps) configured() bool {
	return b.Dir != "" && b.Prefix != "" && b.ConfigFile != "" && b.DBPath != ""
}

// password returns the configured backup password, which is the only
// non-interactive source a scheduled backup can use (AI.md PART 21
// "Setting/Changing Backup Password": `backup.encryption_password` in
// `server.yml`).
func (b BackupDeps) password() string {
	return b.Cfg.EncryptionPassword
}

// encrypt reports whether scheduled backups should be encrypted, per the
// AI.md PART 21 "Backup Encryption" compliance/password matrix. Compliance
// mode forces encryption on; otherwise the explicit toggle or the mere
// presence of a password enables it.
func (b BackupDeps) encrypt() bool {
	if b.password() == "" {
		return false
	}
	return b.Compliance || b.Cfg.Encryption.Enabled
}

// Policy resolves the retention block into an absolute-byte Policy for the
// given volume, per AI.md PART 21 "Backup Retention".
func (b BackupDeps) Policy(status backup.DiskStatus) backup.Policy {
	r := b.Cfg.Retention
	size, err := config.ParseRetentionSize(r.MaxTotalSize, 0)
	var maxTotal int64
	if err == nil && !size.Disabled {
		maxTotal = backup.ResolveMaxTotalBytes(size.Percent, size.Bytes, status)
	}
	return backup.Policy{
		MaxBackups:    r.MaxBackups,
		KeepWeekly:    r.KeepWeekly,
		KeepMonthly:   r.KeepMonthly,
		KeepYearly:    r.KeepYearly,
		MaxTotalBytes: maxTotal,
	}
}

// options builds the shared backup.Options for one archive of the given
// kind.
func (b BackupDeps) options(kind backup.Kind) backup.Options {
	return backup.Options{
		Dir:        b.Dir,
		Prefix:     b.Prefix,
		Kind:       kind,
		ConfigFile: b.ConfigFile,
		DBPath:     b.DBPath,
		ConfigDir:  b.ConfigDir,
		DataDir:    b.DataDir,
		Encrypt:    b.encrypt(),
		Password:   b.password(),
		Compliance: b.Compliance,
		CreatedBy:  "scheduler",
		AppVersion: b.AppVersion,
		// Pinned here rather than left to backup.Create so the failure
		// audit event can name the file that would have been written.
		Now: time.Now().UTC(),
	}
}

// backupDailyTask implements AI.md PART 21 "Backup Creation Flow
// (backup_daily task at 02:00)" steps 1-8 in order: retention sweep, free
// space check, full backup, verify, daily incremental, verify, then apply
// retention only once everything verified.
func backupDailyTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		return runBackupCycle(deps.Backup, deps.Notifier, deps.Logger)
	}
}

// backupHourlyTask implements the optional hourly incremental of AI.md
// PART 21 "Backup Files Created" — a single always-replaced file diffed
// against the newest full backup. It never touches retention: PART 21
// excludes incrementals from the count-based policy.
func backupHourlyTask(deps Deps) TaskFunc {
	return func(ctx context.Context) error {
		b := deps.Backup
		if !b.configured() {
			return nil
		}
		files, err := backup.List(b.Dir, b.Prefix)
		if err != nil {
			return fmt.Errorf("backup_hourly: %w", err)
		}
		opts := b.options(backup.KindHourly)
		opts.Base, opts.BaseName = newestFull(b, files)
		if opts.Base == nil {
			return nil
		}
		res, err := backup.Create(opts)
		if err != nil {
			name := backup.FileName(b.Prefix, backup.KindHourly, opts.Now, b.encrypt())
			backup.AuditFailure(b.Audit, name, err)
			notifyBackupFailed(deps.Notifier, name, err)
			return fmt.Errorf("backup_hourly: %w", err)
		}
		backup.AuditDailyUpdated(b.Audit, res)
		return nil
	}
}

// runBackupCycle performs the AI.md PART 21 daily flow end to end.
func runBackupCycle(b BackupDeps, n *notify.Notifier, logger *applog.Logger) error {
	if !b.configured() {
		return nil
	}

	// Step 1 removes only temporary partial files left by an interrupted write.
	if err := backup.RemovePartialFiles(b.Dir, b.Prefix); err != nil {
		return fmt.Errorf("backup_daily: pre-run partial sweep: %w", err)
	}
	status, err := backup.Disk(b.Dir)
	if err != nil {
		return fmt.Errorf("backup_daily: %w", err)
	}
	policy := b.Policy(status)
	// Step 2: abort when free space is short or the disk is over threshold.
	files, err := backup.List(b.Dir, b.Prefix)
	if err != nil {
		return fmt.Errorf("backup_daily: %w", err)
	}
	var lastSize int64
	if newest := backup.Newest(files); newest != nil {
		lastSize = newest.Size
	}
	if err := backup.CheckDiskSpace(status, lastSize, b.Cfg.DiskThreshold); err != nil {
		var full *backup.DiskFullError
		if errors.As(err, &full) {
			backup.AuditSkippedDiskFull(b.Audit, full)
		}
		notifyBackupFailed(n, "", err)
		return fmt.Errorf("backup_daily: %w", err)
	}

	// The incremental captures changes from the previous full through this
	// run's snapshot instant. The first run has no previous full, so it skips
	// the daily incremental as required by PART 21.
	incOpts := b.options(backup.KindDaily)
	fullOpts := b.options(backup.KindFull)
	fullOpts.Now = incOpts.Now
	baseFile := newestFullFile(files)
	if baseFile != nil && backup.IsEncryptedName(baseFile.Name) {
		header, headerErr := backup.ReadEncryptionHeader(filepath.Join(b.Dir, baseFile.Name))
		if headerErr != nil {
			if logger != nil {
				_ = logger.WriteLine(applog.LevelWarn, fmt.Sprintf("backup_daily: corrupt incremental base %q; skipping incremental: %v\n", baseFile.Name, headerErr))
			}
		} else if keyID, keyErr := backup.KeyIDForPassword(b.password(), header); keyErr != nil || keyID != header.KeyID {
			if logger != nil {
				_ = logger.WriteLine(applog.LevelInfo, fmt.Sprintf("backup_daily: backup password changed for base %q; skipping incremental\n", baseFile.Name))
			}
		} else {
			incOpts.Base, incOpts.BaseName = newestFull(b, []backup.File{*baseFile})
		}
	} else {
		incOpts.Base, incOpts.BaseName = newestFull(b, files)
	}
	if incOpts.Base != nil {
		inc, err := backup.Create(incOpts)
		if err != nil {
			name := backup.FileName(b.Prefix, backup.KindDaily, incOpts.Now, b.encrypt())
			backup.AuditFailure(b.Audit, name, err)
			notifyBackupFailed(n, name, err)
			return fmt.Errorf("backup_daily: incremental: %w", err)
		}
		backup.AuditDailyUpdated(b.Audit, inc)
	}

	// The full at the same T0 follows the cumulative incremental.
	full, err := backup.Create(fullOpts)
	if err != nil {
		name := backup.FileName(b.Prefix, backup.KindFull, fullOpts.Now, b.encrypt())
		backup.AuditFailure(b.Audit, name, err)
		notifyBackupFailed(n, name, err)
		return fmt.Errorf("backup_daily: %w", err)
	}
	backup.AuditCreated(b.Audit, full)
	notifyBackupComplete(n, full)

	// Step 7: both verified, so old backups may now be pruned.
	deleted, err := backup.Apply(b.Dir, b.Prefix, policy)
	if err != nil {
		return fmt.Errorf("backup_daily: retention: %w", err)
	}
	if len(deleted) > 0 {
		remaining, _ := backup.List(b.Dir, b.Prefix)
		backup.AuditRetentionCleanup(b.Audit, deleted, len(remaining), "retention policy applied after verified backup")
	}
	return nil
}

// newestFull loads the newest full backup and returns its filename for the
// encrypted header and incremental manifest.
func newestFull(b BackupDeps, files []backup.File) (*backup.Manifest, string) {
	newest := newestFullFile(files)
	if newest == nil {
		return nil, ""
	}
	m, err := backup.LoadManifest(filepath.Join(b.Dir, newest.Name), b.password())
	if err != nil {
		return nil, ""
	}
	return m, newest.Name
}

func newestFullFile(files []backup.File) *backup.File {
	var newest *backup.File
	for i := range files {
		candidate := &files[i]
		if candidate.Kind.Incremental() {
			continue
		}
		if newest == nil || candidate.Date.After(newest.Date) || (candidate.Date.Equal(newest.Date) && candidate.Name > newest.Name) {
			newest = candidate
		}
	}
	return newest
}
