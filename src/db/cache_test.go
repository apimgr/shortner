package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadCacheSyncIsRemoteAuthoritative(t *testing.T) {
	ctx := context.Background()
	remote, err := OpenSQLite(filepath.Join(t.TempDir(), "remote.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	local, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if err := EnsureSchema(ctx, remote); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, local); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ExecContext(ctx, `INSERT INTO owners(id) VALUES(?)`, "remote-owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := local.ExecContext(ctx, `INSERT INTO owners(id) VALUES(?)`, "stale-owner"); err != nil {
		t.Fatal(err)
	}
	c := &readCache{db: local.r, writer: local.w, maxAge: time.Hour}
	if err := c.sync(ctx, remote.r); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := local.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id='remote-owner'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remote row count = %d, err = %v", count, err)
	}
	if err := local.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id='stale-owner'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale row count = %d, err = %v", count, err)
	}
}

func TestReadCacheExcludesSecretsAndAuditsAndSyncsChangedRows(t *testing.T) {
	ctx := context.Background()
	remote, err := OpenSQLite(filepath.Join(t.TempDir(), "remote.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	local, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	for _, store := range []*Store{remote, local} {
		if err := EnsureSchema(ctx, store); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := remote.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES('master_key','secret',?)`, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ExecContext(ctx, `INSERT INTO audit_log(category,action) VALUES('auth','login')`); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,severity,component,updated_at) VALUES('sec_one','low','api',1)`); err != nil {
		t.Fatal(err)
	}
	c := &readCache{db: local.r, writer: local.w, maxAge: time.Hour}
	if err := c.sync(ctx, remote.r); err != nil {
		t.Fatal(err)
	}
	for _, table := range excludedCacheTables {
		var count int
		if err := local.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteSQLIdentifier(table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("excluded table %s has %d rows, err=%v", table, count, err)
		}
	}
	var status string
	if err := local.r.QueryRowContext(ctx, `SELECT status FROM security_reports WHERE tracking_id='sec_one'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ExecContext(ctx, `UPDATE security_reports SET status='triaged',updated_at=2 WHERE tracking_id='sec_one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ExecContext(ctx, `INSERT INTO security_thanks(tracking_id,credit,severity,disclosed_on) VALUES('thanks_one','Researcher','low','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(ctx, remote.r); err != nil {
		t.Fatal(err)
	}
	if err := local.r.QueryRowContext(ctx, `SELECT status FROM security_reports WHERE tracking_id='sec_one'`).Scan(&status); err != nil || status != "triaged" {
		t.Fatalf("incremental security report status = %q, err=%v", status, err)
	}
	if _, err := remote.ExecContext(ctx, `DELETE FROM security_thanks WHERE tracking_id='thanks_one'`); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(ctx, remote.r); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := local.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_thanks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted row remains in cache: count=%d err=%v", count, err)
	}
	var syncedAt int64
	var watermark string
	if err := local.r.QueryRowContext(ctx, `SELECT synced_at,watermark FROM cache_meta WHERE table_name='security_reports'`).Scan(&syncedAt, &watermark); err != nil || syncedAt <= 0 || watermark != "2" {
		t.Fatalf("security report cache metadata = (%d,%q), err=%v", syncedAt, watermark, err)
	}
}

func TestRemoteWriteQueuesCacheRefreshAndBypassesDirtyCache(t *testing.T) {
	ctx := context.Background()
	remote, err := OpenSQLite(filepath.Join(t.TempDir(), "remote.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	local, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	for _, store := range []*Store{remote, local} {
		if err := EnsureSchema(ctx, store); err != nil {
			t.Fatal(err)
		}
	}
	c := &readCache{db: local.r, writer: local.w, maxAge: time.Hour, syncRequested: make(chan struct{}, 1), root: remote}
	remote.cache = c
	defer func() { remote.cache = nil }()
	if err := c.syncRemote(ctx, remote.r); err != nil {
		t.Fatal(err)
	}

	if err := remote.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO owners(id) VALUES(?)`, "queued-owner")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !c.dirty.Load() {
		t.Fatal("cache was not marked dirty after a committed remote write")
	}
	select {
	case <-c.syncRequested:
	default:
		t.Fatal("committed remote write did not queue a cache refresh")
	}
	if got := remote.Read(ctx); got != remote.r {
		t.Fatal("dirty cache read did not bypass the local replica")
	}
	var count int
	if err := local.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id='queued-owner'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("local cache was synchronously reconciled: count=%d err=%v", count, err)
	}
	if err := c.syncRemote(ctx, remote.r); err != nil {
		t.Fatal(err)
	}
	if c.dirty.Load() {
		t.Fatal("successful cache reconciliation left the cache dirty")
	}
	if err := local.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id='queued-owner'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remote write was not applied to cache: count=%d err=%v", count, err)
	}

	c.dirty.Store(true)
	c.offline.Store(true)
	remote.route.Store(&Store{w: c.writer, r: c.db, readOnly: true, cache: c})
	if got := remote.Read(ctx); got != remote.r {
		t.Fatal("dirty cache was served in offline mode")
	}
}

func TestOfflineModeControlsStaleReadFallback(t *testing.T) {
	local, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	remote, err := OpenSQLite(filepath.Join(t.TempDir(), "remote.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	for _, offlineMode := range []bool{false, true} {
		c := &readCache{db: local.r, maxAge: time.Minute, offlineMode: offlineMode}
		c.lastSync.Store(time.Now().UnixNano())
		c.remoteUnavailable.Store(true)
		if got := c.reader(context.Background(), remote.r); got != local.r {
			t.Fatal("fresh cache was not preferred")
		}
		c.lastSync.Store(time.Now().Add(-2 * time.Minute).UnixNano())
		if offlineMode {
			if got := c.reader(context.Background(), remote.r); got != local.r {
				t.Fatal("offline_mode=true did not use stale cache")
			}
		} else if got := c.reader(context.Background(), remote.r); got != remote.r {
			t.Fatal("offline_mode=false used stale cache while remote was unavailable")
		}
	}
}

func TestOfflineStoreRoutesWritesToRemoteAfterReconnect(t *testing.T) {
	ctx := context.Background()
	remote, err := OpenSQLite(filepath.Join(t.TempDir(), "remote.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.ExecContext(ctx, `INSERT INTO owners(id) VALUES('remote-before-reconnect')`); err != nil {
		t.Fatal(err)
	}
	local, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, local); err != nil {
		t.Fatal(err)
	}
	if _, err := local.ExecContext(ctx, `INSERT INTO owners(id) VALUES('cached')`); err != nil {
		t.Fatal(err)
	}
	c := &readCache{db: local.r, writer: local.w, maxAge: time.Hour, stop: make(chan struct{}), done: make(chan struct{}), syncRequested: make(chan struct{}, 1), openRemote: func() (*Store, error) { return remote, nil }}
	c.offline.Store(true)
	c.remoteUnavailable.Store(true)
	c.lastSync.Store(time.Now().UnixNano())
	store := &Store{w: local.w, r: local.r, readOnly: true, cache: c}
	c.root = store
	go c.loop(nil, 100*time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for store.current() == store && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.current() == store {
		t.Fatal("store did not route to remote after reconciliation")
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO owners(id) VALUES('remote-write')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waitForCachedOwner(t, local.r, "remote-write")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteSyncRestoresRoutingAfterFailedWrite(t *testing.T) {
	ctx := context.Background()
	remotePath := filepath.Join(t.TempDir(), "remote.db")
	remote, err := OpenSQLite(remotePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, remote); err != nil {
		t.Fatal(err)
	}
	local, err := OpenSQLite(filepath.Join(t.TempDir(), "cache.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, local); err != nil {
		t.Fatal(err)
	}
	c := &readCache{db: local.r, writer: local.w, maxAge: time.Hour, stop: make(chan struct{}), done: make(chan struct{}), syncRequested: make(chan struct{}, 1), root: remote}
	remote.cache = c
	if err := c.sync(ctx, remote.r); err != nil {
		t.Fatal(err)
	}
	c.openRemote = func() (*Store, error) {
		reopened, err := OpenSQLite(remotePath, 1)
		if err != nil {
			return nil, err
		}
		if err := EnsureSchema(ctx, reopened); err != nil {
			_ = reopened.Close()
			return nil, err
		}
		return reopened, nil
	}
	go c.loop(remote.r, 100*time.Millisecond)
	_ = remote.w.Close()
	_ = remote.r.Close()
	if err := remote.Write(ctx, func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("write through closed remote unexpectedly succeeded")
	}
	deadline := time.Now().Add(5 * time.Second)
	for (remote.current() == remote || c.offline.Load()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if remote.current() == remote || c.offline.Load() {
		t.Fatal("successful reconciliation did not restore remote routing")
	}
	if err := remote.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO owners(id) VALUES('after-recovery')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waitForCachedOwner(t, local.r, "after-recovery")
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForCachedOwner(t *testing.T, cache *sql.DB, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := cache.QueryRow(`SELECT COUNT(*) FROM owners WHERE id=?`, id).Scan(&count); err == nil && count == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("owner %q was not applied to the read cache after reconciliation", id)
}

func TestOpenWithCacheFallsBackReadOnlyWhenOfflineModeEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	local, err := OpenSQLite(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(context.Background(), local); err != nil {
		t.Fatal(err)
	}
	if _, err := local.w.Exec(`CREATE TABLE cache_meta (table_name TEXT PRIMARY KEY, synced_at INTEGER NOT NULL, watermark TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for _, table := range cacheTables {
		if _, err := local.w.Exec(`INSERT INTO cache_meta(table_name,synced_at,watermark) VALUES(?,?,?)`, table, time.Now().Unix(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := local.w.Exec(`INSERT INTO owners(id) VALUES('cached-owner')`); err != nil {
		t.Fatal(err)
	}
	if _, err := local.w.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES('key','cached-secret',?)`, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := local.w.Exec(`INSERT INTO audit_log(category,action) VALUES('auth','cached')`); err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenWithCache("libsql://invalid.invalid", DefaultPool(), nil, path, time.Hour, time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode = %04o, want 0600", info.Mode().Perm())
	}
	var owner string
	if err := store.Read(context.Background()).QueryRow(`SELECT id FROM owners`).Scan(&owner); err != nil || owner != "cached-owner" {
		t.Fatalf("offline cached owner = %q, err = %v", owner, err)
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error { return nil }); err == nil {
		t.Fatal("offline cache accepted a write")
	}
	for _, table := range excludedCacheTables {
		var count int
		if err := store.r.QueryRow(`SELECT COUNT(*) FROM ` + quoteSQLIdentifier(table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("offline cache retained excluded table %s: count=%d err=%v", table, count, err)
		}
	}
	if err := store.PingContext(context.Background()); err == nil {
		t.Fatal("offline cache reported the remote database as reachable")
	}
	if err := store.Probe(context.Background()); err == nil {
		t.Fatal("offline cache probe reported the remote database as reachable")
	}
}

func TestOpenInMaintenanceDoesNotServeLocalData(t *testing.T) {
	store, err := OpenInMaintenance("libsql://127.0.0.1:1", DefaultPool(), nil, filepath.Join(t.TempDir(), "maintenance.db"), time.Hour)
	if err != nil {
		t.Fatalf("OpenInMaintenance() error = %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	if !store.Offline() || store.CacheUsable() {
		t.Fatalf("recovery status = %+v, want offline maintenance without fallback reads", store.RecoveryStatus())
	}
	if _, err := store.Exec("INSERT INTO owners(id) VALUES(?)", "must-not-write"); err == nil {
		t.Fatal("maintenance state accepted a local write")
	}
	var count int
	if err := store.QueryRow(`SELECT COUNT(*) FROM owners`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("maintenance store read = (%d, %v), want empty state schema", count, err)
	}
}

func TestOpenInMaintenanceRetriesLocalSQLiteDatabase(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "server.db")
	if err := os.Mkdir(databasePath, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenInMaintenanceWithPolicy(databasePath, DefaultPool(), nil, filepath.Join(t.TempDir(), "maintenance.db"), 20*time.Millisecond, 0)
	if err != nil {
		t.Fatalf("OpenInMaintenanceWithPolicy() error = %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	store.SetRecoveryHook(func(context.Context) error { return nil })
	if !store.Offline() {
		t.Fatal("store started online while the configured SQLite path was a directory")
	}
	if err := os.Remove(databasePath); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for store.Offline() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.Offline() {
		t.Fatal("store did not recover after the configured SQLite path became available")
	}
	if store.current() == store {
		t.Fatal("store did not route to the configured SQLite database")
	}
}

func TestMaintenanceReconnectWaitsForRecoveryHook(t *testing.T) {
	local, err := OpenSQLite(filepath.Join(t.TempDir(), "maintenance.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(t.TempDir(), "remote.db")
	var calls atomic.Int32
	c := &readCache{
		db: local.r, writer: local.w, stop: make(chan struct{}), done: make(chan struct{}),
		maintenanceOnly: true, retryInterval: 20 * time.Millisecond,
	}
	store := &Store{w: local.w, r: local.r, readOnly: true, cache: c}
	c.root = store
	c.openRemote = func() (*Store, error) { return OpenSQLite(remotePath, 1) }
	c.offline.Store(true)
	c.remoteUnavailable.Store(true)
	c.offlineSince.Store(time.Now().UTC().Unix())
	c.recoveryPending.Store(true)
	store.SetRecoveryHook(func(context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("secrets are not initialized")
		}
		return nil
	})
	go c.loop(nil, c.retryInterval)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close maintenance store: %v", err)
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for store.Offline() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if store.Offline() {
		t.Fatalf("store remained offline after recovery hook succeeded; hook calls=%d", calls.Load())
	}
	if calls.Load() < 2 {
		t.Fatalf("recovery hook calls = %d, want retry after first failure", calls.Load())
	}
	if store.current() == store {
		t.Fatal("store did not route to the recovered remote database")
	}
}
