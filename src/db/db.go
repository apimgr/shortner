// Package db implements the database layer described in AI.md PART 10
// "Database": connection pooling, idempotent self-creating schema, query
// timeouts, and transaction helpers, plus the Link/Click/api_tokens/
// app_secrets models required by IDEA.md's business logic and AI.md
// PART 11's "API Token Model" / "Cryptographic Keys". SQLite uses
// modernc.org/sqlite (pure Go, no CGO); remote libSQL uses its pure-Go
// database/sql client. See AI.md PART 10 "Connection Pooling" and
// PART 12 "Connection Strings".
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

	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/pgp"
	"github.com/google/uuid"
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
// PART 10 "Implementation" / "Schema Updates". m may be nil (e.g. metrics
// disabled), in which case the plain "sqlite" driver is used with no
// instrumentation.
func Open(path string, pool Pool, m *metrics.Metrics, authTokens ...string) (*Store, error) {
	if isWindowsDrivePath(path) {
		return openAndMigrateSQLite(path, pool, m)
	}
	u, parseErr := url.Parse(strings.TrimSpace(path))
	if parseErr != nil {
		return nil, errors.New("db: invalid database connection string")
	}
	switch strings.ToLower(u.Scheme) {
	case "":
		return openAndMigrateSQLite(path, pool, m)
	case "sqlite", "sqlite2", "sqlite3", "file":
		filePath, err := sqlitePath(u)
		if err != nil {
			return nil, err
		}
		return openAndMigrateSQLite(filePath, pool, m)
	case "libsql", "https", "http":
		var authToken string
		if len(authTokens) > 0 {
			authToken = authTokens[0]
		}
		store, err := openLibSQL(path, pool, authToken)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := EnsureSchema(ctx, store); err != nil {
			_ = store.Close()
			return nil, errors.New("db: database connection failed")
		}
		return store, nil
	default:
		return nil, errors.New("db: unsupported database connection scheme")
	}
}

// OpenWithCache opens the configured database and attaches an optional local
// read replica. Cache errors never change the remote database contract.
func OpenWithCache(path string, pool Pool, m *metrics.Metrics, cachePath string, syncInterval, maxAge time.Duration, offlineMode bool, authTokens ...string) (*Store, error) {
	return OpenWithCacheRecovery(path, pool, m, cachePath, syncInterval, maxAge, syncInterval, 0, offlineMode, authTokens...)
}

// OpenWithCacheRecovery also configures maintenance recovery cadence and cap.
func OpenWithCacheRecovery(path string, pool Pool, m *metrics.Metrics, cachePath string, syncInterval, maxAge, retryInterval time.Duration, maxAttempts int, offlineMode bool, authTokens ...string) (*Store, error) {
	u, err := url.Parse(strings.TrimSpace(path))
	if err != nil {
		return nil, errors.New("db: invalid database connection string")
	}
	if u.Scheme == "" || strings.EqualFold(u.Scheme, "sqlite") || strings.EqualFold(u.Scheme, "file") || strings.EqualFold(u.Scheme, "sqlite2") || strings.EqualFold(u.Scheme, "sqlite3") {
		return nil, errors.New("db: SQLite cache requires a remote libSQL database")
	}
	if syncInterval <= 0 {
		syncInterval = 30 * time.Second
	}
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	if retryInterval <= 0 {
		retryInterval = 30 * time.Second
	}
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	openRemote := func() (*Store, error) { return Open(path, pool, m, authTokens...) }
	store, err := openRemote()
	if err != nil {
		local, cacheErr := openLocalCache(cachePath, pool.MaxOpen)
		if cacheErr != nil {
			return nil, fmt.Errorf("db: remote unavailable and maintenance cache could not be opened: %w", cacheErr)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cacheErr = prepareCache(ctx, local)
		cancel()
		if cacheErr != nil {
			_ = local.Close()
			return nil, errors.New("db: remote unavailable and no usable local cache")
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cacheTables)), ",")
		args := make([]any, len(cacheTables))
		for i, table := range cacheTables {
			args[i] = table
		}
		var tableCount int
		var oldestSync int64
		cacheErr = local.r.QueryRow(`SELECT COUNT(*), COALESCE(MIN(synced_at),0) FROM cache_meta WHERE table_name IN (`+placeholders+`)`, args...).Scan(&tableCount, &oldestSync)
		usable := cacheErr == nil && tableCount == len(cacheTables) && oldestSync > 0 && offlineMode
		c := &readCache{db: local.r, writer: local.w, maxAge: maxAge, retryInterval: retryInterval, maxAttempts: uint64(maxAttempts), stop: make(chan struct{}), done: make(chan struct{}), openRemote: openRemote, offlineMode: offlineMode}
		if cacheErr == nil && tableCount == len(cacheTables) && oldestSync > 0 {
			c.lastSync.Store(time.Unix(oldestSync, 0).UnixNano())
		}
		c.usable.Store(usable)
		c.offline.Store(true)
		c.remoteUnavailable.Store(true)
		c.offlineSince.Store(time.Now().UTC().Unix())
		c.recoveryPending.Store(true)
		store = &Store{w: local.w, r: local.r, readOnly: true, cache: c}
		c.root = store
		go c.loop(nil, syncInterval)
		return store, nil
	}
	if err := attachReadCache(store, cachePath, syncInterval, maxAge, retryInterval, maxAttempts, offlineMode, openRemote); err != nil {
		_ = store.Close()
		return nil, errors.New("db: local SQLite cache initialization failed")
	}
	return store, nil
}

// OpenInMaintenance returns a read-only health store that retries a database
// connection without enabling or serving a local data cache.
func OpenInMaintenance(path string, pool Pool, m *metrics.Metrics, statePath string, retryInterval time.Duration, authTokens ...string) (*Store, error) {
	return OpenInMaintenanceWithPolicy(path, pool, m, statePath, retryInterval, 0, authTokens...)
}

// OpenInMaintenanceWithPolicy sets the retry policy for an offline startup.
func OpenInMaintenanceWithPolicy(path string, pool Pool, m *metrics.Metrics, statePath string, retryInterval time.Duration, maxAttempts int, authTokens ...string) (*Store, error) {
	if retryInterval <= 0 {
		retryInterval = 30 * time.Second
	}
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	local, err := openLocalCache(statePath, pool.MaxOpen)
	if err != nil {
		return nil, fmt.Errorf("db: maintenance state store could not be opened: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	err = prepareCache(ctx, local)
	cancel()
	if err != nil {
		_ = local.Close()
		return nil, fmt.Errorf("db: maintenance state store could not be initialized: %w", err)
	}
	openRemote := func() (*Store, error) { return Open(path, pool, m, authTokens...) }
	c := &readCache{
		db: local.r, writer: local.w, retryInterval: retryInterval, maxAttempts: uint64(maxAttempts),
		stop: make(chan struct{}), done: make(chan struct{}),
		openRemote: openRemote, maintenanceOnly: true,
	}
	c.offline.Store(true)
	c.remoteUnavailable.Store(true)
	c.offlineSince.Store(time.Now().UTC().Unix())
	c.recoveryPending.Store(true)
	store := &Store{w: local.w, r: local.r, readOnly: true, cache: c}
	c.root = store
	go c.loop(nil, retryInterval)
	return store, nil
}

func openAndMigrateSQLite(path string, pool Pool, m *metrics.Metrics) (*Store, error) {
	// The path is percent-encoded before interpolation: a raw "?" or "#"
	// in the configured database path would otherwise terminate the file
	// component and let the remainder be parsed as extra DSN parameters,
	// silently overriding the pragmas set below.
	store, err := openSQLiteWithMetrics(path, pool.MaxOpen, m)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := EnsureSchema(ctx, store); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func sqlitePath(u *url.URL) (string, error) {
	value := u.Path
	if value == "" {
		value = u.Opaque
	}
	if u.Host != "" {
		if u.Host != "localhost" {
			return "", errors.New("db: invalid SQLite file URL")
		}
		value = "//localhost" + value
	}
	if value == "" {
		return "", errors.New("db: invalid SQLite file URL")
	}
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", errors.New("db: invalid SQLite file URL")
	}
	if rawScheme := strings.ToLower(u.Scheme); rawScheme != "file" && rawScheme != "sqlite" && rawScheme != "sqlite2" && rawScheme != "sqlite3" {
		return "", errors.New("db: invalid SQLite file URL")
	}
	return decoded, nil
}

func isWindowsDrivePath(path string) bool {
	return len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

// createStatements are the idempotent CREATE TABLE / CREATE INDEX
// statements applied on every startup, per AI.md PART 10 "Schema Updates
// (Idempotent Approach)". Table shapes: links/clicks from IDEA.md
// "Data models"; api_tokens verbatim from AI.md PART 8 "API Tokens"
// schema; app_secrets from AI.md PART 11 "Cryptographic Keys"; pgp_keys
// stores the non-secret metadata from AI.md PART 11 "Keypair properties".
var createStatements = []string{
	`CREATE TABLE IF NOT EXISTS server_stats (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		requests_total INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS server_stats_hourly (
		hour_start INTEGER PRIMARY KEY,
		requests INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS links (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		short_code      TEXT NOT NULL UNIQUE,
		destination_url TEXT NOT NULL,
		created_at      INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		expires_at      INTEGER,
		click_count     INTEGER NOT NULL DEFAULT 0,
		owner_id        TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE
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

	`CREATE TABLE IF NOT EXISTS owners (
		id TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		last_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	)`,
	`CREATE TABLE IF NOT EXISTS api_tokens (
		id TEXT PRIMARY KEY,
		owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE,
		kind TEXT NOT NULL CHECK (kind IN ('primary', 'linked')),
		scope TEXT NOT NULL DEFAULT 'read-write' CHECK (scope IN ('read', 'read-write', 'global')),
		name TEXT,
		token_hash TEXT NOT NULL UNIQUE,
		token_prefix TEXT NOT NULL,
		token_last4 TEXT NOT NULL DEFAULT '',
		token_enc TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		rotated_at INTEGER,
		expires_at INTEGER,
		last_used_at INTEGER,
		revoked_at INTEGER,
		revoked_reason TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash     ON api_tokens(token_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix   ON api_tokens(token_prefix)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(owner_id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_active   ON api_tokens(revoked_at) WHERE revoked_at IS NULL`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_primary ON api_tokens(owner_id) WHERE kind = 'primary' AND revoked_at IS NULL`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_linked_name ON api_tokens(owner_id, name) WHERE kind = 'linked' AND name IS NOT NULL`,

	`CREATE TABLE IF NOT EXISTS app_secrets (
		name       TEXT PRIMARY KEY,
		value      TEXT NOT NULL,
		version    INTEGER NOT NULL DEFAULT 1,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS captcha_challenges (
		id VARCHAR(32) PRIMARY KEY,
		answer_hash VARCHAR(64) NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_captcha_challenges_expiry ON captcha_challenges(expires_at)`,

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

	// Backup history and metadata, per AI.md PART 10 "Backups" and PART 21
	// "What Is in server.db". The checksum is the archive's SHA-256 digest.
	`CREATE TABLE IF NOT EXISTS backups (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		filename    TEXT NOT NULL UNIQUE,
		filepath    TEXT NOT NULL,
		size_bytes  INTEGER NOT NULL,
		type        TEXT NOT NULL DEFAULT 'auto',
		created_at  INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		checksum    TEXT,
		notes       TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_backups_created ON backups(created_at)`,

	// Coordinated-disclosure security reports, per AI.md PART 11 "Security
	// Reports" -> "Submission Flow". Report and contact payloads are
	// independently encrypted; clear columns contain triage metadata only.
	`CREATE TABLE IF NOT EXISTS security_reports (
		tracking_id TEXT PRIMARY KEY,
		security_id TEXT NOT NULL DEFAULT '',
		received_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		severity    TEXT NOT NULL,
		component   TEXT NOT NULL,
		status      TEXT NOT NULL DEFAULT 'received',
		body_enc BLOB NOT NULL DEFAULT X'',
		body_alg TEXT NOT NULL DEFAULT '',
		contact_enc BLOB,
		credit_pref TEXT NOT NULL DEFAULT 'no',
		credit_name TEXT,
		researcher_comment TEXT NOT NULL DEFAULT '',
		disclosure_date TEXT,
		cve_requested INTEGER NOT NULL DEFAULT 0,
		token_hash TEXT NOT NULL DEFAULT '',
		token_last_used TEXT,
		closed_at INTEGER NOT NULL DEFAULT 0,
		last_viewed_at INTEGER NOT NULL DEFAULT 0,
		disclosure_at INTEGER NOT NULL DEFAULT 0,
		comments TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_security_reports_received ON security_reports(received_at)`,
	`CREATE TABLE IF NOT EXISTS security_thanks (
		tracking_id TEXT PRIMARY KEY,
		credit TEXT NOT NULL,
		severity TEXT NOT NULL,
		disclosed_on TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS pgp_keys (
		fingerprint TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		last_rotated_at INTEGER,
		keyservers_published TEXT NOT NULL DEFAULT '',
		revoked INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp   INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		level       TEXT NOT NULL DEFAULT 'info',
		category    TEXT NOT NULL,
		action      TEXT NOT NULL,
		actor_ip    TEXT,
		target_type TEXT,
		target_id   TEXT,
		details     TEXT,
		success     INTEGER NOT NULL DEFAULT 1
	)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_category ON audit_log(category)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_actor_ip ON audit_log(actor_ip)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_log(target_type, target_id)`,
}

// schemaUpdates holds idempotent ALTER TABLE / index additions applied
// after createStatements, per AI.md PART 10 "Schema Updates". New entries
// are appended here (never edited in place) as the schema grows.
var schemaUpdates = []string{
	`ALTER TABLE app_secrets ADD COLUMN version INTEGER NOT NULL DEFAULT 1`,
	`ALTER TABLE app_secrets ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`,
	`UPDATE app_secrets SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE updated_at = ''`,
	`ALTER TABLE security_reports ADD COLUMN closed_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE security_reports ADD COLUMN last_viewed_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE security_reports ADD COLUMN disclosure_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE security_reports ADD COLUMN comments TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE security_reports ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0`,
	`UPDATE security_reports SET updated_at = received_at WHERE updated_at = 0`,
	`ALTER TABLE security_reports ADD COLUMN security_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE security_reports ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE security_reports ADD COLUMN body_enc BLOB NOT NULL DEFAULT X''`,
	`ALTER TABLE security_reports ADD COLUMN body_alg TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE security_reports ADD COLUMN contact_enc BLOB`,
	`ALTER TABLE security_reports ADD COLUMN credit_pref TEXT NOT NULL DEFAULT 'no'`,
	`ALTER TABLE security_reports ADD COLUMN credit_name TEXT`,
	`ALTER TABLE security_reports ADD COLUMN researcher_comment TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE security_reports ADD COLUMN disclosure_date TEXT`,
	`ALTER TABLE security_reports ADD COLUMN cve_requested INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE security_reports ADD COLUMN token_hash TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE security_reports ADD COLUMN token_last_used TEXT`,
	`UPDATE security_reports SET created_at = received_at WHERE created_at = 0`,
	`UPDATE security_reports SET researcher_comment = comments WHERE researcher_comment = '' AND comments <> ''`,
	`UPDATE security_reports SET disclosure_date = date(disclosure_at, 'unixepoch') WHERE disclosure_date IS NULL AND disclosure_at > 0`,
	`UPDATE security_reports SET token_last_used = date(last_viewed_at, 'unixepoch') WHERE token_last_used IS NULL AND last_viewed_at > 0`,
	`ALTER TABLE api_tokens ADD COLUMN token_last4 TEXT NOT NULL DEFAULT ''`,
}

// EnsureSchema creates tables and applies schema updates. Safe to run on
// every startup (idempotent), per AI.md PART 10 "EnsureSchema".
func EnsureSchema(ctx context.Context, sqlDB Handle) error {
	if err := migrateLegacyOwnerSchema(ctx, sqlDB); err != nil {
		return err
	}
	for _, stmt := range createStatements {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("db: create table: %w", err)
		}
	}
	if err := ensureTokenPublicIDs(ctx, sqlDB); err != nil {
		return err
	}
	for _, stmt := range schemaUpdates {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil && !isColumnExistsError(err) {
			return fmt.Errorf("db: schema update: %w", err)
		}
	}
	if columnExists(ctx, sqlDB, "security_reports", "sealed") {
		if err := migrateLegacySecurityReportBodies(ctx, sqlDB); err != nil {
			return err
		}
		if _, err := sqlDB.ExecContext(ctx, `ALTER TABLE security_reports DROP COLUMN sealed`); err != nil {
			return fmt.Errorf("db: remove legacy security report body column: %w", err)
		}
	}
	if _, err := sqlDB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_security_reports_status ON security_reports(status, created_at)`); err != nil {
		return fmt.Errorf("db: create security report status index: %w", err)
	}
	return nil
}

func ensureTokenPublicIDs(ctx context.Context, sqlDB Handle) error {
	integerKey := false
	rows, err := sqlDB.QueryContext(ctx, `PRAGMA table_info(api_tokens)`)
	if err != nil {
		return fmt.Errorf("db: inspect token id schema: %w", err)
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			return fmt.Errorf("db: scan token id schema: %w", err)
		}
		if name == "id" && strings.EqualFold(kind, "INTEGER") {
			integerKey = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("db: read token id schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db: close token id schema: %w", err)
	}
	if integerKey && !columnExists(ctx, sqlDB, "api_tokens", "public_id") {
		if _, err := sqlDB.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN public_id TEXT`); err != nil {
			return fmt.Errorf("db: add token public id: %w", err)
		}
	}
	if !columnExists(ctx, sqlDB, "api_tokens", "scope") {
		if _, err := sqlDB.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT 'read-write' CHECK (scope IN ('read', 'read-write', 'global'))`); err != nil {
			return fmt.Errorf("db: add token scope: %w", err)
		}
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE api_tokens SET scope='global' WHERE kind='primary'`); err != nil {
		return fmt.Errorf("db: set primary token scope: %w", err)
	}
	if !integerKey {
		return nil
	}
	rows, err = sqlDB.QueryContext(ctx, `SELECT id FROM api_tokens WHERE public_id IS NULL OR public_id=''`)
	if err != nil {
		return fmt.Errorf("db: list tokens missing public ids: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("db: scan token missing public id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("db: iterate tokens missing public ids: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db: close tokens missing public ids: %w", err)
	}
	for _, id := range ids {
		publicID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("db: generate token public id: %w", err)
		}
		if _, err := sqlDB.ExecContext(ctx, `UPDATE api_tokens SET public_id=? WHERE id=? AND (public_id IS NULL OR public_id='')`, publicID.String(), id); err != nil {
			return fmt.Errorf("db: backfill token public id: %w", err)
		}
	}
	if _, err := sqlDB.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_public_id ON api_tokens(public_id)`); err != nil {
		return fmt.Errorf("db: index token public id: %w", err)
	}
	return nil
}

func migrateLegacySecurityReportBodies(ctx context.Context, sqlDB Handle) error {
	rows, err := sqlDB.QueryContext(ctx, `SELECT tracking_id, sealed FROM security_reports WHERE body_enc IS NULL OR length(body_enc) = 0`)
	if err != nil {
		return fmt.Errorf("db: read legacy security report bodies: %w", err)
	}
	type legacyReportBody struct{ id, body string }
	var reports []legacyReportBody
	for rows.Next() {
		var report legacyReportBody
		if err := rows.Scan(&report.id, &report.body); err != nil {
			return errors.Join(fmt.Errorf("db: scan legacy security report body: %w", err), rows.Close())
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return errors.Join(fmt.Errorf("db: iterate legacy security report bodies: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, report := range reports {
		algorithm := "aes-256-gcm"
		if pgp.IsArmoredMessage(report.body) {
			algorithm = "pgp"
		}
		if _, err := sqlDB.ExecContext(ctx, `UPDATE security_reports SET body_enc = CAST(? AS BLOB), body_alg = ? WHERE tracking_id = ?`, report.body, algorithm, report.id); err != nil {
			return fmt.Errorf("db: migrate legacy security report body: %w", err)
		}
	}
	return nil
}

func migrateLegacyOwnerSchema(ctx context.Context, sqlDB Handle) (resultErr error) {
	if _, err := sqlDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS owners (
		id TEXT PRIMARY KEY,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		last_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	)`); err != nil {
		return fmt.Errorf("db: create owner table: %w", err)
	}
	if !tableExists(ctx, sqlDB, "api_tokens") {
		return nil
	}
	if !columnExists(ctx, sqlDB, "api_tokens", "resource_type") || columnExists(ctx, sqlDB, "api_tokens", "owner_id") {
		if !columnExists(ctx, sqlDB, "api_tokens", "owner_id") {
			return fmt.Errorf("db: api_tokens table has an unsupported schema")
		}
		if !columnExists(ctx, sqlDB, "links", "owner_id") {
			if _, err := sqlDB.ExecContext(ctx, `ALTER TABLE links ADD COLUMN owner_id TEXT REFERENCES owners(id) ON DELETE CASCADE`); err != nil {
				return fmt.Errorf("db: add links owner id: %w", err)
			}
		}
		return backfillLinkOwners(ctx, sqlDB)
	}
	if store, ok := sqlDB.(*Store); ok {
		return store.Write(ctx, func(tx *sql.Tx) error {
			return migrateLegacyTokenSchema(ctx, tx)
		})
	}
	beginner, ok := sqlDB.(transactionBeginner)
	if !ok {
		return fmt.Errorf("db: handle does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin owner migration: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			resultErr = errors.Join(resultErr, fmt.Errorf("db: roll back owner migration: %w", err))
		}
	}()
	if err := migrateLegacyTokenSchema(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit owner migration: %w", err)
	}
	return nil
}

func migrateLegacyTokenSchema(ctx context.Context, tx *sql.Tx) error {
	if !columnExistsTx(ctx, tx, "links", "owner_id") {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE links ADD COLUMN owner_id TEXT REFERENCES owners(id) ON DELETE CASCADE`); err != nil {
			return fmt.Errorf("db: add links owner id: %w", err)
		}
	}
	additions := []struct{ name, declaration string }{
		{"owner_id", `TEXT REFERENCES owners(id) ON DELETE CASCADE`},
		{"kind", `TEXT NOT NULL DEFAULT 'linked' CHECK (kind IN ('primary', 'linked'))`},
		{"name", `TEXT`},
		{"scope", `TEXT NOT NULL DEFAULT 'read-write' CHECK (scope IN ('read', 'read-write', 'global'))`},
		{"token_last4", `TEXT NOT NULL DEFAULT ''`},
		{"token_enc", `TEXT NOT NULL DEFAULT ''`},
		{"rotated_at", `INTEGER`},
		{"public_id", `TEXT`},
	}
	for _, addition := range additions {
		if !columnExistsTx(ctx, tx, "api_tokens", addition.name) {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN `+addition.name+` `+addition.declaration); err != nil {
				return fmt.Errorf("db: add legacy api_tokens.%s: %w", addition.name, err)
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, token_hash, token_prefix, resource_type, resource_id, created_at, expires_at, last_used_at, revoked_at, revoked_reason FROM api_tokens ORDER BY id`)
	if err != nil {
		return fmt.Errorf("db: read legacy tokens: %w", err)
	}
	type legacyToken struct {
		id                                     int64
		hash, prefix, resourceType, resourceID string
		created                                int64
		expires, lastUsed, revoked             sql.NullInt64
		reason                                 sql.NullString
	}
	var tokens []legacyToken
	for rows.Next() {
		var item legacyToken
		if err := rows.Scan(&item.id, &item.hash, &item.prefix, &item.resourceType, &item.resourceID, &item.created, &item.expires, &item.lastUsed, &item.revoked, &item.reason); err != nil {
			return errors.Join(fmt.Errorf("db: scan legacy token: %w", err), rows.Close())
		}
		tokens = append(tokens, item)
	}
	if err := rows.Err(); err != nil {
		return errors.Join(fmt.Errorf("db: iterate legacy tokens: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db: close legacy token rows: %w", err)
	}
	ownerByLink := map[string]string{}
	for _, token := range tokens {
		ownerID := fmt.Sprintf("legacy-token-%d", token.id)
		if token.resourceType == "link" {
			ownerID = ownerByLink[token.resourceID]
			if ownerID == "" {
				ownerID = "legacy-link-" + token.resourceID
				ownerByLink[token.resourceID] = ownerID
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO owners(id) VALUES (?)`, ownerID); err != nil {
			return fmt.Errorf("db: migrate legacy owner: %w", err)
		}
		kind := "primary"
		var activePrimary int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'primary' AND revoked_at IS NULL`, ownerID).Scan(&activePrimary); err != nil {
			return err
		}
		if activePrimary > 0 {
			kind = "linked"
		}
		publicID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("db: create legacy token public id: %w", err)
		}
		scope := "read-write"
		if kind == "primary" {
			scope = "global"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET owner_id=?,kind=?,scope=?,token_enc=COALESCE(token_enc,''),public_id=? WHERE id=?`, ownerID, kind, scope, publicID.String(), token.id); err != nil {
			return fmt.Errorf("db: migrate legacy token row: %w", err)
		}
	}
	for linkID, ownerID := range ownerByLink {
		if _, err := tx.ExecContext(ctx, `UPDATE links SET owner_id = ? WHERE id = ?`, ownerID, linkID); err != nil {
			return fmt.Errorf("db: assign legacy link owner: %w", err)
		}
	}
	if err := fillUnownedLinks(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(owner_id)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix ON api_tokens(token_prefix)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_api_tokens_active ON api_tokens(revoked_at) WHERE revoked_at IS NULL`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_primary ON api_tokens(owner_id) WHERE kind = 'primary' AND revoked_at IS NULL`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_linked_name ON api_tokens(owner_id, name) WHERE kind = 'linked' AND name IS NOT NULL`); err != nil {
		return err
	}
	return nil
}

func backfillLinkOwners(ctx context.Context, sqlDB Handle) error {
	return WithTransaction(ctx, sqlDB, func(tx *sql.Tx) error {
		return fillUnownedLinks(ctx, tx)
	})
}

func fillUnownedLinks(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM links WHERE owner_id IS NULL OR owner_id = ''`)
	if err != nil {
		return fmt.Errorf("db: find unowned links: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("db: close unowned link rows: %w", err)
	}
	for _, id := range ids {
		ownerID := fmt.Sprintf("legacy-link-%d", id)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO owners(id) VALUES (?)`, ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE links SET owner_id = ? WHERE id = ?`, ownerID, id); err != nil {
			return err
		}
	}
	return nil
}

func tableExists(ctx context.Context, db Handle, table string) bool {
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	return count > 0
}

func columnExists(ctx context.Context, db Handle, table, column string) (exists bool) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false
	}
	defer func() {
		if err := rows.Close(); err != nil {
			exists = false
		}
	}()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if rows.Scan(&cid, &name, &typ, &notnull, &def, &pk) == nil && name == column {
			return true
		}
	}
	return false
}

func columnExistsTx(ctx context.Context, tx *sql.Tx, table, column string) (exists bool) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false
	}
	defer func() {
		if err := rows.Close(); err != nil {
			exists = false
		}
	}()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if rows.Scan(&cid, &name, &typ, &notnull, &def, &pk) == nil && name == column {
			return true
		}
	}
	return false
}

// isColumnExistsError reports whether err is SQLite's "duplicate column"
// error, per AI.md PART 10 "Schema Updates (Idempotent Approach)".
func isColumnExistsError(err error) bool {
	return strings.Contains(err.Error(), "duplicate column")
}

// WithTransaction runs fn inside a transaction with a 30s timeout,
// committing on success and rolling back on error, per AI.md PART 10
// "Transaction Patterns" -> "Basic Transaction".
func WithTransaction(ctx context.Context, sqlDB Handle, fn func(*sql.Tx) error) error {
	if store, ok := sqlDB.(*Store); ok {
		return store.Write(ctx, fn)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	beginner, ok := sqlDB.(transactionBeginner)
	if !ok {
		return fmt.Errorf("db: handle does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx, nil)
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
func WithSerializableRetry(ctx context.Context, sqlDB Handle, maxRetries int, fn func(*sql.Tx) error) error {
	if store, ok := sqlDB.(*Store); ok {
		return store.Write(ctx, fn)
	}
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		beginner, ok := sqlDB.(transactionBeginner)
		if !ok {
			return fmt.Errorf("db: handle does not support transactions")
		}
		tx, err := beginner.BeginTx(ctx, nil)
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
