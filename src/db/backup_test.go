package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRecordBackupAt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "server.db")
	createdAt := time.Date(2025, time.January, 15, 10, 30, 0, 0, time.UTC)
	record := BackupMetadata{
		Filename:  "shortner_backup_2025-01-15.tar.gz",
		Filepath:  "/var/lib/shortner/backups/shortner_backup_2025-01-15.tar.gz",
		SizeBytes: 1234,
		Type:      "auto",
		CreatedAt: createdAt,
		Checksum:  "sha256:abc123",
	}
	if err := RecordBackupAt(context.Background(), dbPath, record); err != nil {
		t.Fatalf("RecordBackupAt() error = %v", err)
	}

	sqlDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	var filename, path, kind, checksum string
	var size, timestamp int64
	if err := sqlDB.QueryRow(`SELECT filename, filepath, size_bytes, type, created_at, checksum FROM backups`).
		Scan(&filename, &path, &size, &kind, &timestamp, &checksum); err != nil {
		t.Fatalf("read backup row: %v", err)
	}
	if filename != record.Filename || path != record.Filepath || size != record.SizeBytes || kind != record.Type || timestamp != createdAt.Unix() || checksum != record.Checksum {
		t.Fatalf("backup row = %q, %q, %d, %q, %d, %q", filename, path, size, kind, timestamp, checksum)
	}

	record.SizeBytes = 4321
	if err := RecordBackupAt(context.Background(), dbPath, record); err != nil {
		t.Fatalf("RecordBackupAt() update error = %v", err)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM backups`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backup row count after same-name update = %d, want 1", count)
	}
}

func TestRecordBackupAtRejectsInvalidMetadata(t *testing.T) {
	if err := RecordBackupAt(context.Background(), filepath.Join(t.TempDir(), "server.db"), BackupMetadata{
		Filename: "../backup.tar.gz",
		Filepath: "/tmp/backup.tar.gz",
	}); err == nil {
		t.Fatal("RecordBackupAt() accepted a path-like filename")
	}
}
