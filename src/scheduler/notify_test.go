package scheduler

import (
	"errors"
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{5 * 1024 * 1024 * 1024 * 1024, "5.0 TB"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := formatBytes(tc.in); got != tc.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSuppressedSchedulerError(t *testing.T) {
	// AI.md PART 17 "scheduler_error" -> "Suppression": a task that emits
	// its own failure event must not also emit the generic one.
	tests := []struct {
		id   string
		want bool
	}{
		{"backup_daily", true},
		{"backup_hourly", true},
		{"ssl_renewal", true},
		{"token_cleanup", false},
		{"log_rotation", false},
		{"update_check", false},
		{"healthcheck_self", false},
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			if got := suppressedSchedulerError[tc.id]; got != tc.want {
				t.Errorf("suppressedSchedulerError[%q] = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestNotifySchedulerErrorIsNilSafe(t *testing.T) {
	// With no SMTP the Scheduler carries a nil *notify.Notifier; every
	// notification path must stay inert rather than panic (AI.md PART 17
	// "SMTP Requirement": not sent, not attempted).
	s := &Scheduler{}
	next := time.Now().UTC()

	tests := []struct {
		name string
		call func()
	}{
		{"suppressed task", func() { s.notifySchedulerError("backup_daily", "Daily Backup", "boom", &next) }},
		{"unsuppressed task with a next run", func() { s.notifySchedulerError("token_cleanup", "Token Cleanup", "boom", &next) }},
		{"unsuppressed task with no next run", func() { s.notifySchedulerError("token_cleanup", "", "boom", nil) }},
		{"backup complete with no result", func() { notifyBackupComplete(nil, nil) }},
		{"backup failed with no error", func() { notifyBackupFailed(nil, "x.tar.gz", nil) }},
		{"backup failed with no filename", func() { notifyBackupFailed(nil, "", errors.New("disk full")) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.call()
		})
	}
}
