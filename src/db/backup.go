package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// BackupMetadata contains the history fields required by AI.md PART 10's
// backups table. Checksum is the SHA-256 digest of the resulting archive.
type BackupMetadata struct {
	Filename  string
	Filepath  string
	SizeBytes int64
	Type      string
	CreatedAt time.Time
	Checksum  string
	Notes     string
}

// RecordBackupAt writes metadata into the server database. It creates only
// the backup schema here so it can safely update an existing database from
// before that table was added without attempting unrelated migrations.
func RecordBackupAt(ctx context.Context, dbPath string, record BackupMetadata) (resultErr error) {
	if dbPath == "" || record.Filename == "" || filepath.Base(record.Filename) != record.Filename || record.Filepath == "" || record.SizeBytes < 0 {
		return fmt.Errorf("db: invalid backup metadata")
	}
	if record.Type == "" {
		record.Type = "auto"
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	sqlDB, err := OpenSQLite(dbPath, 1)
	if err != nil {
		return fmt.Errorf("db: open backup metadata database: %w", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("db: close backup metadata database: %w", err))
		}
	}()
	if err := sqlDB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS backups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		filename TEXT NOT NULL UNIQUE,
		filepath TEXT NOT NULL,
		size_bytes INTEGER NOT NULL,
		type TEXT NOT NULL DEFAULT 'auto',
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		checksum TEXT,
		notes TEXT
	)`)
		return err
	}); err != nil {
		return fmt.Errorf("db: create backup metadata table: %w", err)
	}
	if err := sqlDB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_backups_created ON backups(created_at)`)
		return err
	}); err != nil {
		return fmt.Errorf("db: create backup history index: %w", err)
	}
	if err := sqlDB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO backups(filename, filepath, size_bytes, type, created_at, checksum, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(filename) DO UPDATE SET filepath=excluded.filepath, size_bytes=excluded.size_bytes,
			type=excluded.type, created_at=excluded.created_at, checksum=excluded.checksum, notes=excluded.notes`,
			record.Filename, record.Filepath, record.SizeBytes, record.Type, record.CreatedAt.UTC().Unix(), nullableString(record.Checksum), nullableString(record.Notes))
		return err
	}); err != nil {
		return fmt.Errorf("db: record backup metadata: %w", err)
	}
	return nil
}
