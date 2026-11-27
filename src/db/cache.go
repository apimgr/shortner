package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// readCache is a local, disposable replica. Remote libSQL remains authoritative.
type readCache struct {
	db                 *sql.DB
	writer             *sql.DB
	maxAge             time.Duration
	retryInterval      time.Duration
	maxAttempts        uint64
	lastSync           atomic.Int64
	dirty              atomic.Bool
	writeEpoch         atomic.Uint64
	syncRequested      chan struct{}
	syncFailureLogged  atomic.Bool
	stop               chan struct{}
	done               chan struct{}
	once               sync.Once
	openRemote         func() (*Store, error)
	offline            atomic.Bool
	remoteUnavailable  atomic.Bool
	usable             atomic.Bool
	offlineSince       atomic.Int64
	retryAttempts      atomic.Uint64
	recoveryExhausted  atomic.Bool
	lastAttempt        atomic.Int64
	offlineMode        bool
	root               *Store
	remoteMu           sync.Mutex
	remoteStore        *Store
	recoveryMu         sync.RWMutex
	recoveryHook       func(context.Context) error
	recoveryPending    atomic.Bool
	exhaustionHook     func(uint64)
	exhaustionReported atomic.Bool
	maintenanceHook    func(bool, RecoveryStatus)
	maintenanceOnly    bool
}

var cacheTables = []string{"server_stats", "server_stats_hourly", "owners", "links", "clicks", "api_tokens", "captcha_challenges", "scheduler_tasks", "scheduler_history", "backups", "security_reports", "security_thanks", "pgp_keys"}

var excludedCacheTables = []string{"app_secrets", "audit_log"}

func attachReadCache(remote *Store, path string, interval, maxAge, retryInterval time.Duration, maxAttempts int, offlineMode bool, openRemote func() (*Store, error)) error {
	local, err := openLocalCache(path, 2)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := prepareCache(ctx, local); err != nil {
		_ = local.Close()
		return err
	}
	c := &readCache{db: local.r, writer: local.w, maxAge: maxAge, retryInterval: retryInterval, maxAttempts: uint64(maxAttempts), stop: make(chan struct{}), done: make(chan struct{}), syncRequested: make(chan struct{}, 1), openRemote: openRemote, root: remote, offlineMode: offlineMode}
	remote.cache = c
	if err := c.sync(ctx, remote.r); err != nil {
		remote.cache = nil
		_ = local.Close()
		return err
	}
	c.usable.Store(true)
	go c.loop(remote.r, interval)
	return nil
}

func openLocalCache(path string, readers int) (*Store, error) {
	directory := filepath.Dir(path)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return OpenSQLite(path, readers)
}

func (c *readCache) reader(ctx context.Context, remote *sql.DB) *sql.DB {
	if c.dirty.Load() {
		if remote == c.db && c.root != nil {
			return c.root.r
		}
		return remote
	}
	if c.offline.Load() || remote == c.db {
		return c.db
	}
	stale := time.Since(time.Unix(0, c.lastSync.Load())) > c.maxAge
	if stale && (!c.offlineMode || !c.remoteUnavailable.Load()) {
		return remote
	}
	return c.db
}

func (c *readCache) requestSync() {
	if c.syncRequested == nil {
		return
	}
	select {
	case c.syncRequested <- struct{}{}:
	default:
	}
}

func (c *readCache) syncRemote(ctx context.Context, remote *sql.DB) error {
	epoch := c.writeEpoch.Load()
	if err := c.sync(ctx, remote); err != nil {
		return err
	}
	if c.writeEpoch.Load() != epoch {
		c.dirty.Store(true)
		c.requestSync()
	}
	c.syncFailureLogged.Store(false)
	return nil
}

func (c *readCache) syncOnce(remote *sql.DB, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	err := c.syncRemote(ctx, remote)
	cancel()
	if err != nil {
		c.dirty.Store(true)
		pingCtx, pingCancel := context.WithTimeout(context.Background(), time.Second)
		pingErr := remote.PingContext(pingCtx)
		pingCancel()
		if pingErr != nil {
			c.setOffline()
			return false
		}
		if c.syncFailureLogged.CompareAndSwap(false, true) {
			log.Print("WARN: database cache reconciliation failed")
		}
		return true
	}
	if c.remoteUnavailable.Load() && !c.recoveryPending.Load() {
		c.setOnline()
	}
	return true
}

func (c *readCache) setOffline() {
	wasOffline := c.remoteUnavailable.Swap(true)
	if c.offlineSince.Load() == 0 {
		c.offlineSince.Store(time.Now().UTC().Unix())
	}
	if c.offlineMode {
		c.offline.Store(true)
	}
	if c.root != nil && c.offlineMode {
		c.root.route.Store(&Store{w: c.writer, r: c.db, readOnly: true, cache: c})
	}
	if !wasOffline {
		c.notifyMaintenance(true)
	}
}

func (c *readCache) setOnline() {
	wasOffline := c.remoteUnavailable.Swap(false)
	c.offline.Store(false)
	c.usable.Store(true)
	c.offlineSince.Store(0)
	c.retryAttempts.Store(0)
	c.lastAttempt.Store(0)
	c.recoveryExhausted.Store(false)
	c.remoteMu.Lock()
	remote := c.remoteStore
	c.remoteMu.Unlock()
	if c.root != nil {
		if remote != nil {
			c.root.route.Store(remote)
		} else {
			c.root.route.Store(c.root)
		}
	}
	if wasOffline {
		c.notifyMaintenance(false)
	}
}

func (c *readCache) notifyMaintenance(offline bool) {
	c.recoveryMu.RLock()
	hook := c.maintenanceHook
	c.recoveryMu.RUnlock()
	if hook != nil && c.root != nil {
		hook(offline, c.root.RecoveryStatus())
	}
}

func (c *readCache) markRecoveryExhausted() {
	c.recoveryExhausted.Store(true)
	c.recoveryMu.RLock()
	hook := c.exhaustionHook
	c.recoveryMu.RUnlock()
	if hook != nil && c.exhaustionReported.CompareAndSwap(false, true) {
		hook(c.retryAttempts.Load())
	}
}

func (c *readCache) loop(remote *sql.DB, interval time.Duration) {
	defer close(c.done)
	if interval <= 0 {
		interval = 30 * time.Second
	}
	retryInterval := c.retryInterval
	if retryInterval <= 0 {
		retryInterval = interval
	}
	syncTicker := time.NewTicker(interval)
	retryTicker := time.NewTicker(retryInterval)
	defer syncTicker.Stop()
	defer retryTicker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-c.syncRequested:
			if remote != nil && !c.maintenanceOnly && !c.syncOnce(remote, max(interval, 30*time.Second)) {
				remote = nil
			}
		case <-syncTicker.C:
			if remote == nil || c.maintenanceOnly {
				continue
			}
			if !c.syncOnce(remote, interval) {
				remote = nil
			}
		case <-retryTicker.C:
			if c.recoveryExhausted.Load() {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), retryInterval)
			if c.remoteUnavailable.Load() {
				if c.maxAttempts > 0 && c.retryAttempts.Load() >= c.maxAttempts {
					c.markRecoveryExhausted()
					cancel()
					continue
				}
				c.retryAttempts.Add(1)
				c.lastAttempt.Store(time.Now().UTC().Unix())
			}
			if remote == nil {
				if reopened, err := c.openRemote(); err == nil {
					if !c.maintenanceOnly {
						err = c.syncRemote(ctx, reopened.r)
					}
					if err == nil {
						reopened.cache = c
						c.remoteMu.Lock()
						if c.remoteStore != nil {
							c.remoteStore.cache = nil
							_ = c.remoteStore.Close()
						}
						c.remoteStore = reopened
						c.remoteMu.Unlock()
						remote = reopened.r
					} else {
						_ = reopened.Close()
						c.dirty.Store(true)
						if c.syncFailureLogged.CompareAndSwap(false, true) {
							log.Print("WARN: database cache reconciliation failed")
						}
					}
				}
			}
			if remote != nil && c.remoteUnavailable.Load() {
				if !c.maintenanceOnly {
					if err := remote.PingContext(ctx); err != nil {
						cancel()
						continue
					}
				}
				if !c.recoveryPending.Load() || c.recover(ctx) == nil {
					c.recoveryPending.Store(false)
					c.setOnline()
				}
			}
			cancel()
		}
	}
}

func (c *readCache) recover(ctx context.Context) error {
	c.recoveryMu.RLock()
	hook := c.recoveryHook
	c.recoveryMu.RUnlock()
	if hook == nil {
		return errors.New("db: recovery initialization is not ready")
	}
	return hook(ctx)
}

func (c *readCache) sync(ctx context.Context, remote *sql.DB) error {
	tx, err := c.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	remoteTx, err := remote.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin cache source snapshot: %w", err)
	}
	defer remoteTx.Rollback()
	if err := ensureCacheMetadata(ctx, tx); err != nil {
		return err
	}
	for _, table := range excludedCacheTables {
		present, err := sqliteTableExists(ctx, c.db, table, false)
		if err != nil {
			return err
		}
		if present {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteSQLIdentifier(table)); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS _cache_metadata`); err != nil {
		return err
	}
	columnsByTable := make(map[string][]string, len(cacheTables))
	updatedByTable := make(map[string]bool, len(cacheTables))
	keysByTable := make(map[string][]string, len(cacheTables))
	watermarks := make(map[string]sql.NullString, len(cacheTables))
	for _, table := range cacheTables {
		columns, keys, err := sqliteTableColumns(ctx, c.db, table)
		if err != nil {
			return err
		}
		columnsByTable[table], keysByTable[table] = columns, keys
		for _, column := range columns {
			if column == "updated_at" {
				updatedByTable[table] = true
			}
		}
		var watermark string
		err = tx.QueryRowContext(ctx, `SELECT watermark FROM cache_meta WHERE table_name=?`, table).Scan(&watermark)
		if err == nil {
			watermarks[table] = sql.NullString{String: watermark, Valid: true}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	for i := len(cacheTables) - 1; i >= 0; i-- {
		table := cacheTables[i]
		if !updatedByTable[table] || !watermarks[table].Valid {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteSQLIdentifier(table)); err != nil {
				return err
			}
		}
	}
	oldestSync := time.Now().UTC().Unix()
	for _, table := range cacheTables {
		columns, keys := columnsByTable[table], keysByTable[table]
		if len(keys) == 0 {
			return fmt.Errorf("db: cache table %s has no primary key", table)
		}
		projection := make([]string, len(columns))
		for i, column := range columns {
			projection[i] = quoteSQLIdentifier(column)
		}
		query := `SELECT ` + strings.Join(projection, ",") + ` FROM ` + quoteSQLIdentifier(table)
		incremental := updatedByTable[table] && watermarks[table].Valid
		if incremental {
			query += ` WHERE "updated_at" >= ? ORDER BY "updated_at"`
		}
		args := []any{}
		if incremental {
			args = append(args, watermarks[table].String)
		}
		rows, err := remoteTx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("db: cache changed-row query failed for %s: %w", table, err)
		}
		if err := applyCacheRows(ctx, tx, rows, table, columns, keys); err != nil {
			return err
		}
		if err := reconcileCacheDeletes(ctx, tx, remoteTx, table, keys); err != nil {
			return err
		}
		watermark := ""
		if updatedByTable[table] {
			var value any
			if err := remoteTx.QueryRowContext(ctx, `SELECT MAX("updated_at") FROM `+quoteSQLIdentifier(table)).Scan(&value); err != nil {
				return fmt.Errorf("db: read cache watermark for %s: %w", table, err)
			}
			if value != nil {
				watermark = fmt.Sprint(value)
			}
		}
		now := time.Now().UTC().Unix()
		if _, err := tx.ExecContext(ctx, `INSERT INTO cache_meta(table_name,synced_at,watermark) VALUES(?,?,?) ON CONFLICT(table_name) DO UPDATE SET synced_at=excluded.synced_at,watermark=excluded.watermark`, table, now, watermark); err != nil {
			return err
		}
		if now < oldestSync {
			oldestSync = now
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.lastSync.Store(time.Unix(oldestSync, 0).UnixNano())
	c.dirty.Store(false)
	return nil
}

func prepareCache(ctx context.Context, local *Store) error {
	if err := EnsureSchema(ctx, local); err != nil {
		return err
	}
	tx, err := local.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureCacheMetadata(ctx, tx); err != nil {
		return err
	}
	for _, table := range excludedCacheTables {
		present, err := sqliteTableExists(ctx, local.r, table, false)
		if err != nil {
			return err
		}
		if present {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteSQLIdentifier(table)); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS _cache_metadata`); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureCacheMetadata(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS cache_meta (table_name TEXT PRIMARY KEY, synced_at INTEGER NOT NULL, watermark TEXT NOT NULL DEFAULT '')`)
	return err
}

func sqliteTableColumns(ctx context.Context, db *sql.DB, table string) ([]string, []string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+quoteSQLIdentifier(table)+`)`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	type columnInfo struct {
		name string
		pk   int
	}
	var columns []string
	var keys []columnInfo
	for rows.Next() {
		var cid, notnull, pk int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notnull, &defaultValue, &pk); err != nil {
			return nil, nil, err
		}
		columns = append(columns, name)
		if pk > 0 {
			keys = append(keys, columnInfo{name: name, pk: pk})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].pk < keys[j].pk })
	primary := make([]string, len(keys))
	for i := range keys {
		primary[i] = keys[i].name
	}
	return columns, primary, nil
}

func applyCacheRows(ctx context.Context, tx *sql.Tx, rows *sql.Rows, table string, columns, keys []string) error {
	defer rows.Close()
	quotedColumns := make([]string, len(columns))
	for i, column := range columns {
		quotedColumns[i] = quoteSQLIdentifier(column)
	}
	quotedKeys := make([]string, len(keys))
	keySet := make(map[string]bool, len(keys))
	for i, key := range keys {
		quotedKeys[i] = quoteSQLIdentifier(key)
		keySet[key] = true
	}
	updates := make([]string, 0, len(columns)-len(keys))
	for _, column := range columns {
		if !keySet[column] {
			quoted := quoteSQLIdentifier(column)
			updates = append(updates, quoted+`=excluded.`+quoted)
		}
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	statement := `INSERT INTO ` + quoteSQLIdentifier(table) + ` (` + strings.Join(quotedColumns, ",") + `) VALUES (` + marks + `) ON CONFLICT (` + strings.Join(quotedKeys, ",") + `) DO UPDATE SET ` + strings.Join(updates, ",")
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		for i, value := range values {
			blob, emptyBlob := value.([]byte)
			if table == "security_reports" && columns[i] == "body_enc" && (value == nil || (emptyBlob && blob == nil)) {
				values[i] = []byte{}
			}
		}
		if _, err := tx.ExecContext(ctx, statement, values...); err != nil {
			return fmt.Errorf("db: apply cache row for %s: %w", table, err)
		}
	}
	return rows.Err()
}

func reconcileCacheDeletes(ctx context.Context, tx *sql.Tx, remote *sql.Tx, table string, keys []string) error {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = quoteSQLIdentifier(key)
	}
	selectKeys := `SELECT ` + strings.Join(quoted, ",") + ` FROM ` + quoteSQLIdentifier(table)
	remoteRows, err := remote.QueryContext(ctx, selectKeys)
	if err != nil {
		return err
	}
	remoteKeys := make(map[string]struct{})
	for remoteRows.Next() {
		values := make([]any, len(keys))
		dest := make([]any, len(keys))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := remoteRows.Scan(dest...); err != nil {
			_ = remoteRows.Close()
			return err
		}
		remoteKeys[cacheRowKey(values)] = struct{}{}
	}
	if err := remoteRows.Err(); err != nil {
		_ = remoteRows.Close()
		return err
	}
	if err := remoteRows.Close(); err != nil {
		return err
	}
	localRows, err := tx.QueryContext(ctx, selectKeys)
	if err != nil {
		return err
	}
	var deleted [][]any
	for localRows.Next() {
		values := make([]any, len(keys))
		dest := make([]any, len(keys))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := localRows.Scan(dest...); err != nil {
			_ = localRows.Close()
			return err
		}
		if _, exists := remoteKeys[cacheRowKey(values)]; !exists {
			deleted = append(deleted, values)
		}
	}
	if err := localRows.Err(); err != nil {
		_ = localRows.Close()
		return err
	}
	if err := localRows.Close(); err != nil {
		return err
	}
	where := make([]string, len(keys))
	for i, key := range keys {
		where[i] = quoteSQLIdentifier(key) + `=?`
	}
	statement := `DELETE FROM ` + quoteSQLIdentifier(table) + ` WHERE ` + strings.Join(where, ` AND `)
	for _, values := range deleted {
		if _, err := tx.ExecContext(ctx, statement, values...); err != nil {
			return err
		}
	}
	return nil
}

func cacheRowKey(values []any) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%T:%v", value, value)
	}
	return strings.Join(parts, "\x00")
}

func sqliteTableExists(ctx context.Context, db *sql.DB, table string, remote bool) (bool, error) {
	for _, name := range []string{table, "srv_" + table} {
		var found int
		err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if !remote {
			break
		}
	}
	return false, nil
}

func (c *readCache) close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() { close(c.stop); <-c.done })
	c.remoteMu.Lock()
	var remoteErr error
	if c.remoteStore != nil {
		c.remoteStore.cache = nil
		remoteErr = c.remoteStore.Close()
		c.remoteStore = nil
	}
	c.remoteMu.Unlock()
	return errors.Join(remoteErr, c.db.Close(), c.writer.Close())
}
