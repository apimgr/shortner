package scheduler

import (
	"fmt"
	"time"

	"github.com/apimgr/shortner/src/backup"
	"github.com/apimgr/shortner/src/notify"
)

// formatBytes renders a byte count the way an operator reads it in an
// email, so `{size}` says "482.3 MB" rather than "505668403".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// suppressedSchedulerError lists the task ids that emit their own AI.md
// PART 17 failure event. It implements PART 17 "scheduler_error" ->
// "Suppression": `backup_failed` suppresses `scheduler_error` for backup
// tasks and `ssl_renewal_failed` does the same for SSL renewal, so an
// operator receives one precise email per failure rather than two
// describing it twice. Tasks with no dedicated failure event
// (token_cleanup, log_rotation, update_check, ...) are absent here and
// therefore still fire `scheduler_error` normally.
var suppressedSchedulerError = map[string]bool{
	"backup_daily":  true,
	"backup_hourly": true,
	"ssl_renewal":   true,
}

// notifySchedulerError sends the AI.md PART 17 `scheduler_error` event for
// a failed task run.
func (s *Scheduler) notifySchedulerError(id, name, errMsg string, next *time.Time) {
	if suppressedSchedulerError[id] {
		return
	}
	nextRun := "not scheduled"
	if next != nil {
		nextRun = next.UTC().Format(time.RFC3339)
	}
	taskName := name
	if taskName == "" {
		taskName = id
	}
	_ = s.notifier.Send(notify.EventSchedulerError, map[string]string{
		"task_name": taskName,
		"error":     errMsg,
		"next_run":  nextRun,
	})
}

// notifyBackupComplete raises the AI.md PART 17 `backup_complete` event
// for a verified archive.
func notifyBackupComplete(n *notify.Notifier, r *backup.Result) {
	if r == nil {
		return
	}
	_ = n.Send(notify.EventBackupComplete, map[string]string{
		"filename": r.Name,
		"size":     formatBytes(r.Size),
	})
}

// notifyBackupFailed raises the AI.md PART 17 `backup_failed` event. name
// may be empty when the run aborted before a filename was chosen (the
// disk-space check), and `{size}` is deliberately empty because nothing
// was written.
func notifyBackupFailed(n *notify.Notifier, name string, err error) {
	if err == nil {
		return
	}
	if name == "" {
		name = "(no file written)"
	}
	_ = n.Send(notify.EventBackupFailed, map[string]string{
		"filename": name,
		"size":     "0 B",
		"error":    err.Error(),
	})
}

// SetNotifier attaches n so failed task runs raise the AI.md PART 17
// `scheduler_error` email event. n may be nil (no SMTP, or notifications
// disabled), in which case nothing is ever sent — per PART 17's SMTP
// requirement there is no queue and no "would have sent" log line.
func (s *Scheduler) SetNotifier(n *notify.Notifier) {
	s.notifier = n
}
