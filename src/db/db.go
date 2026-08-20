// Package db implements the database layer described in AI.md PART 10
// "Database": connection pooling, idempotent self-creating schema, query
// timeouts, and transaction helpers, plus the Link/Click/api_tokens/
// app_secrets models required by IDEA.md's business logic and AI.md
// PART 11's "API Token Model" / "Cryptographic Keys". The only supported
// driver is modernc.org/sqlite (pure Go, no CGO) — see AI.md PART 10
// "Connection Pooling" and the backend-rules.md CRITICAL rule against CGO
// drivers. libsql/Turso support is deferred (tracked in TODO.AI.md; it
// depends on the full server.yml database schema in AI.md PART 12).
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Pool holds connection-pool tuning, per AI.md PART 10 "Connection
// Pooling" -> "Pool Configuration". SQLite is a single-writer database;
// these defaults match the spec's "Development" tier.
type Pool struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	MaxIdleTime time.Duration
}

// DefaultPool returns the AI.md PART 10 "Development" pool tier — the
// safe default until AI.md PART 12 defines full server.yml pool config.
func DefaultPool() Pool {
	return Pool{
		MaxOpen:     5,
		MaxIdle:     2,
		MaxLifetime: 5 * time.Minute,
		MaxIdleTime: 1 * time.Minute,
	}
}

// escapeDSNPath percent-encodes a filesystem path for use as the file
// component of a SQLite URI DSN. url.PathEscape is not used directly
// because it also escapes "/", which must stay literal in a path.
func escapeDSNPath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// Open opens a SQLite database at path, configures the connection pool,
// verifies connectivity, and applies the idempotent schema. Per AI.md
// PART 10 "Implementation" / "Schema Updates".
func Open(path string, pool Pool) (*sql.DB, error) {
	// The path is percent-encoded before interpolation: a raw "?" or "#"
	// in the configured database path would otherwise terminate the file
	// component and let the remainder be parsed as extra DSN parameters,
	// silently overriding the pragmas set below.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)",
		escapeDSNPath(path))
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}

	sqlDB.SetMaxOpenConns(pool.MaxOpen)
	sqlDB.SetMaxIdleConns(pool.MaxIdle)
	sqlDB.SetConnMaxLifetime(pool.MaxLifetime)
	sqlDB.SetConnMaxIdleTime(pool.MaxIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: ping %s: %w", path, err)
	}

	if err := EnsureSchema(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return sqlDB, nil
}

// createStatements are the idempotent CREATE TABLE / CREATE INDEX
// statements applied on every startup, per AI.md PART 10 "Schema Updates
// (Idempotent Approach)". Table shapes: links/clicks from IDEA.md
// "Data models"; api_tokens verbatim from AI.md PART 8 "API Tokens"
// schema; app_secrets from AI.md PART 11 "Cryptographic Keys".
var createStatements = []string{
	`CREATE TABLE IF NOT EXISTS links (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		short_code      TEXT NOT NULL UNIQUE,
		destination_url TEXT NOT NULL,
		created_at      INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		expires_at      INTEGER,
		click_count     INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_links_short_code ON links(short_code)`,
	`CREATE INDEX IF NOT EXISTS idx_links_expires_at ON links(expires_at)`,

	`CREATE TABLE IF NOT EXISTS clicks (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		link_id    INTEGER NOT NULL REFERENCES links(id),
		timestamp  INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		ip         TEXT,
		user_agent TEXT,
		referrer   TEXT,
		country    TEXT,
		region     TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_clicks_link_id ON clicks(link_id)`,
	`CREATE INDEX IF NOT EXISTS idx_clicks_timestamp ON clicks(timestamp)`,

	`CREATE TABLE IF NOT EXISTS api_tokens (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		token_hash     TEXT NOT NULL UNIQUE,
		token_prefix   TEXT NOT NULL,
		resource_type  TEXT NOT NULL,
		resource_id    TEXT NOT NULL,
		created_at     INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		expires_at     INTEGER,
		last_used_at   INTEGER,
		revoked_at     INTEGER,
		revoked_reason TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash     ON api_tokens(token_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix   ON api_tokens(token_prefix)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_resource ON api_tokens(resource_type, resource_id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_active   ON api_tokens(revoked_at) WHERE revoked_at IS NULL`,

	`CREATE TABLE IF NOT EXISTS app_secrets (
		name       TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		rotated_at INTEGER
	)`,

	// Scheduler task state and history, per AI.md PART 18 "Scheduler
	// State (Persistent)" and PART 10's literal schema.
	`CREATE TABLE IF NOT EXISTS scheduler_tasks (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		enabled     INTEGER NOT NULL DEFAULT 1,
		schedule    TEXT NOT NULL,
		last_run    INTEGER,
		next_run    INTEGER,
		last_status TEXT,
		last_error  TEXT,
		run_count   INTEGER NOT NULL DEFAULT 0,
		fail_count  INTEGER NOT NULL DEFAULT 0
	)`,

	`CREATE TABLE IF NOT EXISTS scheduler_history (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id     TEXT NOT NULL,
		started_at  INTEGER NOT NULL,
		finished_at INTEGER,
		status      TEXT NOT NULL,
		error       TEXT,
		duration_ms INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS idx_scheduler_history_task    ON scheduler_history(task_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scheduler_history_started ON scheduler_history(started_at)`,
}

// schemaUpdates holds idempotent ALTER TABLE / index additions applied
// after createStatements, per AI.md PART 10 "Schema Updates". Empty for
// now — this project has no prior schema version to migrate from; new
// entries are appended here (never edited in place) as the schema grows.
var schemaUpdates []string

// EnsureSchema creates tables and applies schema updates. Safe to run on
// every startup (idempotent), per AI.md PART 10 "EnsureSchema".
func EnsureSchema(ctx context.Context, sqlDB *sql.DB) error {
	for _, stmt := range createStatements {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("db: create table: %w", err)
		}
	}
	for _, stmt := range schemaUpdates {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil && !isColumnExistsError(err) {
			return fmt.Errorf("db: schema update: %w", err)
		}
	}
	return nil
}

// isColumnExistsError reports whether err is SQLite's "duplicate column"
// error, per AI.md PART 10 "Schema Updates (Idempotent Approach)".
func isColumnExistsError(err error) bool {
	return strings.Contains(err.Error(), "duplicate column")
}

// WithTransaction runs fn inside a transaction with a 30s timeout,
// committing on success and rolling back on error, per AI.md PART 10
// "Transaction Patterns" -> "Basic Transaction".
func WithTransaction(ctx context.Context, sqlDB *sql.DB, fn func(*sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit transaction: %w", err)
	}
	return nil
}

// HandleQueryError normalizes a raw database/sql error into a stable,
// caller-checkable error, per AI.md PART 10 "Handling Timeouts".
func HandleQueryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("TIMEOUT: request timed out: %w", err)
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("CANCELED: request was canceled: %w", err)
	default:
		return fmt.Errorf("SERVER_ERROR: database error: %w", err)
	}
}

// ErrNotFound is returned by lookups that find no matching row.
var ErrNotFound = errors.New("db: not found")

// isSerializationError reports whether err represents a SQLite busy /
// locked condition, per AI.md PART 10 "Retry on Serialization Failure".
func isSerializationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

// WithSerializableRetry retries fn up to maxRetries times on a SQLite
// busy/locked error, with linear backoff, per AI.md PART 10 "Retry on
// Serialization Failure".
func WithSerializableRetry(ctx context.Context, sqlDB *sql.DB, maxRetries int, fn func(*sql.Tx) error) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("db: begin transaction: %w", err)
		}

		err = fn(tx)
		if err != nil {
			_ = tx.Rollback()
			if isSerializationError(err) && attempt < maxRetries-1 {
				time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
				lastErr = err
				continue
			}
			return err
		}

		if err := tx.Commit(); err != nil {
			if isSerializationError(err) && attempt < maxRetries-1 {
				lastErr = err
				continue
			}
			return fmt.Errorf("db: commit transaction: %w", err)
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("db: max retries exceeded: %w", lastErr)
	}
	return errors.New("db: max retries exceeded")
}
