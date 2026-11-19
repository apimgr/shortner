package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLiteConcurrentReadersAndWritersNoLockErrors(t *testing.T) {
	store := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE concurrency_probe (writer_id INTEGER PRIMARY KEY, value INTEGER NOT NULL)`)
		return err
	}); err != nil {
		t.Fatalf("create concurrency table: %v", err)
	}

	stop := make(chan struct{})
	var stopOnce sync.Once
	var workers sync.WaitGroup
	firstErr := make(chan error, 1)
	recordErr := func(err error) {
		select {
		case <-stop:
			return
		default:
		}
		select {
		case firstErr <- err:
		default:
		}
		stopOnce.Do(func() { close(stop) })
		cancel()
	}

	for writer := 0; writer < 20; writer++ {
		writerID := writer
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := store.Write(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO concurrency_probe(writer_id,value) VALUES(?,1) ON CONFLICT(writer_id) DO UPDATE SET value=value+1`, writerID)
					return err
				})
				if err != nil {
					recordErr(fmt.Errorf("writer %d: %w", writerID, err))
					return
				}
			}
		}()
	}

	for reader := 0; reader < 20; reader++ {
		readerID := reader
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				var total int64
				err := store.Read(ctx).QueryRowContext(ctx, `SELECT COALESCE(SUM(value),0) FROM concurrency_probe`).Scan(&total)
				if err != nil {
					recordErr(fmt.Errorf("reader %d: %w", readerID, err))
					return
				}
			}
		}()
	}

	timer := time.NewTimer(10 * time.Second)
	select {
	case err := <-firstErr:
		timer.Stop()
		workers.Wait()
		if strings.Contains(strings.ToLower(err.Error()), "locked") || strings.Contains(strings.ToLower(err.Error()), "busy") {
			t.Fatalf("concurrent SQLite access returned a lock error: %v", err)
		}
		t.Fatalf("concurrent SQLite access failed: %v", err)
	case <-timer.C:
		stopOnce.Do(func() { close(stop) })
		cancel()
		workers.Wait()
	}
	select {
	case err := <-firstErr:
		t.Fatalf("concurrent SQLite access failed during shutdown: %v", err)
	default:
	}
}
