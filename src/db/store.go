package db

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/metrics"
)

// Store owns the independent read and write pools for one SQLite file.
type Store struct {
	w            *sql.DB
	r            *sql.DB
	busyObserver func(time.Duration, string)
	readOnly     bool
}

// SetBusyObserver connects wait telemetry without coupling storage to logging.
func (s *Store) SetBusyObserver(observer func(time.Duration, string)) {
	if observer == nil {
		return
	}
	previous := s.busyObserver
	s.busyObserver = func(wait time.Duration, caller string) {
		if previous != nil {
			previous(wait, caller)
		}
		observer(wait, caller)
	}
}

// Handle is the query surface accepted by legacy model helpers. New code should
// use Store.Read and Store.Write so reads and mutations choose the right pool.
type Handle interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	PingContext(context.Context) error
}

type transactionBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// ExecContext routes single-statement mutations through the serialized writer.
func (s *Store) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var result sql.Result
	err := s.Write(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = tx.ExecContext(ctx, query, args...)
		return err
	})
	return result, err
}

// Exec is a convenience wrapper for code that does not carry a context.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.ExecContext(context.Background(), query, args...)
}

// QueryContext reads from the query-only pool.
func (s *Store) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.r.QueryContext(ctx, query, args...)
}

// Query is a convenience wrapper for code that does not carry a context.
func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.QueryContext(context.Background(), query, args...)
}

// QueryRowContext reads from the query-only pool.
func (s *Store) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.r.QueryRowContext(ctx, query, args...)
}

// QueryRow is a convenience wrapper for code that does not carry a context.
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.QueryRowContext(context.Background(), query, args...)
}

// PingContext verifies the query-only pool can reach the SQLite file.
func (s *Store) PingContext(ctx context.Context) error { return s.r.PingContext(ctx) }

// Probe verifies a trivial query succeeds through both independently pooled
// database handles, so a read-only cache cannot mask a failed writer.
func (s *Store) Probe(ctx context.Context) error {
	writeCtx, cancelWrite := context.WithTimeout(ctx, 5*time.Second)
	var writeValue int
	err := s.w.QueryRowContext(writeCtx, `SELECT 1`).Scan(&writeValue)
	cancelWrite()
	if err != nil {
		return fmt.Errorf("write handle probe failed: %w", err)
	}
	readCtx, cancelRead := context.WithTimeout(ctx, 5*time.Second)
	defer cancelRead()
	var readValue int
	if err := s.r.QueryRowContext(readCtx, `SELECT 1`).Scan(&readValue); err != nil {
		return fmt.Errorf("read handle probe failed: %w", err)
	}
	return nil
}

// IssueCaptchaChallenge persists only the answer digest for a single-use challenge.
func (s *Store) IssueCaptchaChallenge(ctx context.Context, id, answerHash string, createdAt, expiresAt time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO captcha_challenges(id,answer_hash,created_at,expires_at) VALUES(?,?,?,?)`, id, answerHash, createdAt.UTC().Unix(), expiresAt.UTC().Unix())
		return err
	})
}

// ConsumeCaptchaChallenge atomically consumes a valid, unexpired challenge.
func (s *Store) ConsumeCaptchaChallenge(ctx context.Context, id, answerHash string, now time.Time) (bool, error) {
	consumed := false
	err := s.Write(ctx, func(tx *sql.Tx) error {
		consumed = false
		var stored string
		var expiry int64
		err := tx.QueryRowContext(ctx, `SELECT answer_hash,expires_at FROM captcha_challenges WHERE id=?`, id).Scan(&stored, &expiry)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		found := err == nil
		if _, err := tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE id=?`, id); err != nil {
			return err
		}
		if found && expiry > now.UTC().Unix() && len(stored) == len(answerHash) {
			consumed = subtle.ConstantTimeCompare([]byte(stored), []byte(answerHash)) == 1
		}
		return nil
	})
	return consumed, err
}

// PruneCaptchaChallenges deletes expired rows in bounded batches.
func (s *Store) PruneCaptchaChallenges(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > 500 {
		limit = 500
	}
	var deleted int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE id IN (SELECT id FROM captcha_challenges WHERE expires_at<=? ORDER BY expires_at LIMIT ?)`, now.UTC().Unix(), limit)
		if err != nil {
			return err
		}
		deleted, err = res.RowsAffected()
		return err
	})
	return deleted, err
}

// OpenSQLite opens a SQLite store with separate read and write handles.
func OpenSQLite(path string, readers int) (*Store, error) {
	return openSQLiteWithDriver(path, readers, "sqlite")
}

func openSQLiteWithMetrics(path string, readers int, m *metrics.Metrics) (*Store, error) {
	driverName := "sqlite"
	if m != nil {
		tmp, err := sql.Open("sqlite", "file::memory:")
		if err == nil {
			driverName = m.RegisterInstrumentedDriver("sqlite", tmp.Driver())
			_ = tmp.Close()
		}
	}
	s, err := openSQLiteWithDriver(path, readers, driverName)
	if err != nil {
		return nil, err
	}
	if m != nil {
		s.SetBusyObserver(func(wait time.Duration, _ string) {
			m.DBBusyTotal.Inc()
			m.DBWriteWaitSeconds.Observe(wait.Seconds())
		})
	}
	return s, nil
}

// OpenReadOnlySQLite opens two independent read-only handles without changing
// the database journal mode. It is intended for backup validation and restore
// authorization, where opening the supplied file must not mutate it.
func OpenReadOnlySQLite(path string, readers int) (*Store, error) {
	if readers < 1 {
		readers = 1
	}
	base := "file:" + escapeDSNPath(path) + "?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(1)"
	w, err := sql.Open("sqlite", base)
	if err != nil {
		return nil, fmt.Errorf("db: open read-only handle %s: %w", path, err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	r, err := sql.Open("sqlite", base)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("db: open read pool %s: %w", path, err)
	}
	r.SetMaxOpenConns(readers)
	r.SetMaxIdleConns(readers)
	r.SetConnMaxLifetime(0)
	s := &Store{w: w, r: r, readOnly: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, handle := range []*sql.DB{w, r} {
		var queryOnly, busyTimeout int
		if err := handle.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
			_ = s.Close()
			if err != nil {
				return nil, fmt.Errorf("db: query-only check for %s: %w", path, err)
			}
			return nil, fmt.Errorf("db: handle for %s is not query-only", path)
		}
		if err := handle.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil || busyTimeout != 10000 {
			_ = s.Close()
			if err != nil {
				return nil, fmt.Errorf("db: busy timeout check for %s: %w", path, err)
			}
			return nil, fmt.Errorf("db: busy timeout for %s is %d ms, want 10000", path, busyTimeout)
		}
	}
	return s, nil
}

func openSQLiteWithDriver(path string, readers int, driverName string) (*Store, error) {
	if readers < 1 {
		readers = 1
	}
	base := "file:" + escapeDSNPath(path) + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	w, err := sql.Open(driverName, base+"&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("db: open write handle %s: %w", path, err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	r, err := sql.Open(driverName, base+"&_pragma=query_only(1)")
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("db: open read handle %s: %w", path, err)
	}
	r.SetMaxOpenConns(readers)
	r.SetMaxIdleConns(readers)
	r.SetConnMaxLifetime(0)
	s := &Store{w: w, r: r}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mode string
	if err := w.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		_ = s.Close()
		if err != nil {
			return nil, fmt.Errorf("db: journal mode check for %s: %w", path, err)
		}
		return nil, fmt.Errorf("db: journal mode for %s is %q, want wal", path, mode)
	}
	var queryOnly int
	if err := r.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
		_ = s.Close()
		if err != nil {
			return nil, fmt.Errorf("db: query-only check for %s: %w", path, err)
		}
		return nil, fmt.Errorf("db: read handle for %s is not query-only", path)
	}
	for _, handle := range []*sql.DB{w, r} {
		var busyTimeout, synchronous int
		if err := handle.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("db: busy timeout check for %s: %w", path, err)
		}
		if busyTimeout != 10000 {
			_ = s.Close()
			return nil, fmt.Errorf("db: busy timeout for %s is %d ms, want 10000", path, busyTimeout)
		}
		if err := handle.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("db: synchronous mode check for %s: %w", path, err)
		}
		if synchronous != 1 {
			_ = s.Close()
			return nil, fmt.Errorf("db: synchronous mode for %s is %d, want NORMAL", path, synchronous)
		}
	}
	return s, nil
}

// SnapshotSQLite produces a consistent SQLite snapshot with VACUUM INTO.
func SnapshotSQLite(ctx context.Context, source, destination string) error {
	s, err := OpenSQLite(source, 1)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.VacuumInto(ctx, destination)
}

// Read returns the query-only database handle. Callers must issue only reads.
func (s *Store) Read(context.Context) *sql.DB { return s.r }

// Write runs a callback in a bounded immediate transaction, retrying lock errors.
func (s *Store) Write(parent context.Context, fn func(*sql.Tx) error) error {
	if s.readOnly {
		return errors.New("db: write attempted on read-only store")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	var busyWait time.Duration
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		attemptStarted := time.Now()
		err = s.writeOnce(ctx, fn)
		if isBusy(err) {
			busyWait += time.Since(attemptStarted)
		}
		if !isBusy(err) || ctx.Err() != nil {
			if busyWait > 0 && s.busyObserver != nil {
				s.busyObserver(busyWait, writeCaller())
			}
			if ctx.Err() != nil && err != nil {
				return ctx.Err()
			}
			return err
		}
		wait := time.Duration(25<<attempt)*time.Millisecond + time.Duration(rand.Intn(25))*time.Millisecond
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			if busyWait > 0 && s.busyObserver != nil {
				s.busyObserver(busyWait, writeCaller())
			}
			return ctx.Err()
		}
	}
	if busyWait > 0 && s.busyObserver != nil {
		s.busyObserver(busyWait, writeCaller())
	}
	return err
}

func writeCaller() string {
	pcs := make([]uintptr, 12)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "/src/db/store.go") {
			return fmt.Sprintf("%s:%d", frame.File, frame.Line)
		}
		if !more {
			break
		}
	}
	return "unknown"
}

func (s *Store) writeOnce(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Checkpoint truncates the WAL after request draining.
func (s *Store) Checkpoint(ctx context.Context) error {
	return s.w.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(new(int), new(int), new(int))
}

// VacuumInto creates a consistent standalone snapshot through SQLite.
func (s *Store) VacuumInto(ctx context.Context, destination string) error {
	_, err := s.w.ExecContext(ctx, "VACUUM INTO ?", destination)
	return err
}

// Close shuts down the read pool before the write pool.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	rerr := s.r.Close()
	werr := s.w.Close()
	return errors.Join(rerr, werr)
}

// isBusy recognizes SQLite primary and extended busy/locked errors.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToUpper(err.Error())
	return strings.Contains(text, "SQLITE_BUSY") || strings.Contains(text, "SQLITE_LOCKED") || strings.Contains(text, "DATABASE IS LOCKED")
}
