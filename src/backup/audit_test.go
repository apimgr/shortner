package backup

import (
	"errors"
	"testing"

	"github.com/apimgr/shortner/src/applog"
)

type auditRecorder struct {
	entries []applog.Entry
	err     error
}

func (r *auditRecorder) Write(entry applog.Entry) error {
	r.entries = append(r.entries, entry)
	return r.err
}

func TestAuditEvents(t *testing.T) {
	logger := &auditRecorder{err: errors.New("audit disk unavailable")}
	result := &Result{
		Name:      "backup.tar.gz",
		Size:      42,
		Encrypted: true,
		Manifest: Manifest{
			Kind:         KindFull,
			AppVersion:   "1.2.3",
			Files:        map[string]string{"server.yml": "sha256:abc"},
			BaseChecksum: "sha256:base",
		},
	}

	AuditCreated(logger, result)
	AuditDailyUpdated(logger, result)
	AuditRestored(logger, result.Name, &RestoreResult{Restored: []string{"server.yml"}, Warnings: []string{"version differs"}})
	AuditDeleted(logger, File{Name: result.Name, Size: result.Size}, "operator requested deletion")
	AuditRetentionCleanup(logger, []File{{Name: result.Name}}, 2, "retention policy")
	AuditFailure(logger, result.Name, errors.New("archive write failed"))
	AuditFailure(logger, result.Name, &VerificationError{Path: result.Name, Check: CheckChecksum, Err: errors.New("checksum mismatch")})
	AuditSkippedDiskFull(logger, &DiskFullError{FreeBytes: 10, UsedPercent: 91.5, Threshold: 90, RequiredBytes: 20})

	if len(logger.entries) != 8 {
		t.Fatalf("recorded %d audit entries, want 8", len(logger.entries))
	}
	wantEvents := []string{
		EventCreated,
		EventDailyUpdated,
		EventRestored,
		EventDeleted,
		EventRetentionCleanup,
		EventFailed,
		EventVerificationFailed,
		EventSkippedDiskFull,
	}
	for index, want := range wantEvents {
		entry := logger.entries[index]
		if entry.Event != want || entry.Category != auditCategory {
			t.Errorf("entry %d = (%q, %q), want (%q, %q)", index, entry.Event, entry.Category, want, auditCategory)
		}
		if entry.Actor.IP != "127.0.0.1" || entry.Actor.UserID != "operator" {
			t.Errorf("entry %d actor = %+v, want local operator", index, entry.Actor)
		}
	}
	if logger.entries[0].Target == nil || logger.entries[0].Target.ID != result.Name {
		t.Errorf("created target = %+v, want backup %q", logger.entries[0].Target, result.Name)
	}
	if logger.entries[2].Details["warnings"] == nil {
		t.Error("restored event omitted warnings")
	}
	if logger.entries[4].Target != nil {
		t.Errorf("retention target = %+v, want nil", logger.entries[4].Target)
	}
	if logger.entries[6].Details["check"] != string(CheckChecksum) {
		t.Errorf("verification check = %v, want %q", logger.entries[6].Details["check"], CheckChecksum)
	}
	if logger.entries[7].Details["required_bytes"] != int64(20) {
		t.Errorf("required bytes = %v, want 20", logger.entries[7].Details["required_bytes"])
	}
}

func TestAuditNilLoggerAndZeroValueOptions(t *testing.T) {
	AuditCreated(nil, &Result{})
	AuditDailyUpdated(nil, &Result{})
	AuditRestored(nil, "", &RestoreResult{})
	AuditDeleted(nil, File{}, "")
	AuditRetentionCleanup(nil, nil, 0, "")
	AuditFailure(nil, "", errors.New("failure"))
	AuditSkippedDiskFull(nil, &DiskFullError{})

	logger := &auditRecorder{}
	write(logger, "event", applog.SeverityInfo, applog.ResultSuccess, "", nil, "")
	if len(logger.entries) != 1 || logger.entries[0].Target != nil {
		t.Fatalf("entry = %+v, want one entry without a target", logger.entries)
	}
}
