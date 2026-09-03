package backup

import (
	"errors"

	"github.com/apimgr/shortner/src/applog"
)

// Auditor is the subset of *applog.AuditLogger this package uses. Taking
// an interface keeps the backup package testable without opening a log
// file, and lets callers pass nil to disable auditing entirely.
type Auditor interface {
	Write(applog.Entry) error
}

// auditCategory is the AI.md PART 11 audit category every PART 21 event
// is filed under.
const auditCategory = "backup"

// The AI.md PART 21 "Audit Events" table, verbatim.
const (
	EventCreated            = "backup.created"
	EventRestored           = "backup.restored"
	EventDeleted            = "backup.deleted"
	EventFailed             = "backup.failed"
	EventRetentionCleanup   = "backup.retention_cleanup"
	EventVerificationFailed = "backup.verification_failed"
	EventDailyUpdated       = "backup.daily_updated"
	EventSkippedDiskFull    = "backup.skipped_disk_full"
)

// localActor identifies a backup action taken by the server process
// itself (CLI subcommand or scheduler task) rather than by a remote
// client.
func localActor() applog.Actor {
	return applog.Actor{IP: "127.0.0.1", UserID: "operator"}
}

// write emits one audit entry, tolerating a nil Auditor. Audit failures
// never abort a backup: losing the log line is strictly better than
// losing the backup.
func write(a Auditor, event string, severity applog.Severity, result applog.Result, name string, details map[string]any, reason string) {
	if a == nil {
		return
	}
	entry := applog.Entry{
		Event:    event,
		Category: auditCategory,
		Severity: severity,
		Actor:    localActor(),
		Details:  details,
		Reason:   reason,
		Result:   result,
	}
	if name != "" {
		entry.Target = &applog.Target{Type: "backup", ID: name}
	}
	_ = a.Write(entry)
}

// AuditCreated records `backup.created` with the filename, size, encrypted
// flag, and verification status PART 21's table requires.
func AuditCreated(a Auditor, r *Result) {
	write(a, EventCreated, applog.SeverityInfo, applog.ResultSuccess, r.Name, map[string]any{
		"filename":    r.Name,
		"size":        r.Size,
		"encrypted":   r.Encrypted,
		"kind":        string(r.Manifest.Kind),
		"verified":    true,
		"file_count":  len(r.Manifest.Files),
		"app_version": r.Manifest.AppVersion,
	}, "")
}

// AuditDailyUpdated records `backup.daily_updated` for the replace-in-
// place incremental, carrying how many files changed since the base full.
func AuditDailyUpdated(a Auditor, r *Result) {
	write(a, EventDailyUpdated, applog.SeverityInfo, applog.ResultSuccess, r.Name, map[string]any{
		"filename":        r.Name,
		"size":            r.Size,
		"encrypted":       r.Encrypted,
		"changed_files":   len(r.Manifest.Files),
		"base_checksum":   r.Manifest.BaseChecksum,
		"since_full_kind": string(r.Manifest.Kind),
	}, "")
}

// AuditRestored records `backup.restored`.
func AuditRestored(a Auditor, name string, r *RestoreResult) {
	details := map[string]any{"filename": name, "restored_files": len(r.Restored)}
	if len(r.Warnings) > 0 {
		details["warnings"] = r.Warnings
	}
	write(a, EventRestored, applog.SeverityWarn, applog.ResultSuccess, name, details, "")
}

// AuditDeleted records `backup.deleted` for a single removed archive.
func AuditDeleted(a Auditor, f File, reason string) {
	write(a, EventDeleted, applog.SeverityInfo, applog.ResultSuccess, f.Name, map[string]any{
		"filename": f.Name,
		"size":     f.Size,
	}, reason)
}

// AuditRetentionCleanup records `backup.retention_cleanup` with the
// deleted files, the reason, and the remaining count.
func AuditRetentionCleanup(a Auditor, deleted []File, remaining int, reason string) {
	names := make([]string, 0, len(deleted))
	for _, f := range deleted {
		names = append(names, f.Name)
	}
	write(a, EventRetentionCleanup, applog.SeverityInfo, applog.ResultSuccess, "", map[string]any{
		"deleted":         names,
		"deleted_count":   len(names),
		"remaining_count": remaining,
	}, reason)
}

// AuditFailure records `backup.failed`, or `backup.verification_failed`
// when err is a verification failure — PART 21 requires the failing check
// name to be logged with the failure.
func AuditFailure(a Auditor, name string, err error) {
	var verr *VerificationError
	if errors.As(err, &verr) {
		write(a, EventVerificationFailed, applog.SeverityError, applog.ResultFailure, verr.Path, map[string]any{
			"filename": name,
			"check":    string(verr.Check),
			"error":    verr.Err.Error(),
		}, "backup deleted: verification check failed")
		return
	}
	write(a, EventFailed, applog.SeverityError, applog.ResultFailure, name, map[string]any{
		"filename": name,
		"error":    err.Error(),
	}, "")
}

// AuditSkippedDiskFull records `backup.skipped_disk_full` with the free
// space, disk usage percent, and threshold PART 21's table requires.
func AuditSkippedDiskFull(a Auditor, e *DiskFullError) {
	write(a, EventSkippedDiskFull, applog.SeverityError, applog.ResultFailure, "", map[string]any{
		"free_bytes":     e.FreeBytes,
		"disk_used_pct":  e.UsedPercent,
		"threshold_pct":  e.Threshold,
		"required_bytes": e.RequiredBytes,
	}, e.Error())
}
