// Public-safe request statistics for the PART 13 health response's
// StatsInfo, per AI.md PART 13 "Field Order & Structure".
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apimgr/shortner/src/db"
)

// statsBuckets is the number of one-minute buckets covering the rolling
// 24h window (24 * 60).
const statsBuckets = 1440

// Stats collects lifetime and rolling-24h request counts plus an in-flight
// active-connection gauge, updated from LoggingMiddleware.
//
// The 24h window is a fixed ring of one-minute counters rather than a
// timestamp per request: memory is then constant (~23 KB) regardless of
// traffic volume and independent of how often Snapshot is called. Storing
// one time.Time per request instead would grow without bound under load,
// since nothing prunes it between Snapshot calls.
type Stats struct {
	total int64
	mu    sync.Mutex
	// counts[i] is the number of requests recorded in minute minutes[i];
	// a bucket whose minute no longer matches is stale and reads as zero.
	counts   [statsBuckets]int64
	minutes  [statsBuckets]int64
	active   int64
	db       db.Handle
	pending  map[int64]int64
	stored   map[int64]int64
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// NewStats returns an empty Stats collector.
func NewStats() *Stats {
	return &Stats{}
}

// AttachDB enables durable request statistics and starts the minute flush
// required by AI.md PART 13. The database schema is created by db.Open.
func (s *Stats) AttachDB(handle db.Handle) error {
	if s == nil || handle == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var total int64
	err := handle.QueryRowContext(ctx, `SELECT requests_total FROM server_stats WHERE id = 1`).Scan(&total)
	if errors.Is(err, sql.ErrNoRows) {
		total, err = 0, nil
	}
	if err != nil {
		return fmt.Errorf("request statistics: load total")
	}
	stored, err := loadStoredRequestHours(ctx, handle)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.db = handle
	s.pending = make(map[int64]int64)
	s.stored = stored
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	atomic.StoreInt64(&s.total, total)
	s.mu.Unlock()
	go s.persistenceLoop()
	return nil
}

// RecordRequest increments the lifetime and 24h counters for one completed
// request.
func (s *Stats) RecordRequest() {
	atomic.AddInt64(&s.total, 1)

	now := time.Now().Unix()
	minute := now / 60
	idx := int(minute % statsBuckets)

	s.mu.Lock()
	if s.db != nil {
		if s.pending == nil {
			s.pending = make(map[int64]int64)
		}
		s.pending[now/3600*3600]++
	} else {
		if s.minutes[idx] != minute {
			s.minutes[idx] = minute
			s.counts[idx] = 0
		}
		s.counts[idx]++
	}
	s.mu.Unlock()
}

// BeginRequest increments the active-connection gauge; the returned func
// decrements it and must be deferred by the caller.
func (s *Stats) BeginRequest() func() {
	atomic.AddInt64(&s.active, 1)
	return func() { atomic.AddInt64(&s.active, -1) }
}

// Snapshot returns (total, last-24h, active) as of now. Buckets older
// than the 24h window are ignored rather than deleted — the ring reuses
// them in place on the next write.
func (s *Stats) Snapshot() (total int64, last24h int64, active int) {
	total = atomic.LoadInt64(&s.total)
	active = int(atomic.LoadInt64(&s.active))

	s.mu.Lock()
	if s.db != nil {
		cutoff := time.Now().Unix()/3600*3600 - 23*3600
		for hour, count := range s.stored {
			if hour >= cutoff {
				last24h += count
			}
		}
		for hour, count := range s.pending {
			if hour >= cutoff {
				last24h += count
			}
		}
		s.mu.Unlock()
		return total, last24h, active
	}
	cutoff := time.Now().Unix()/60 - statsBuckets
	for i, minute := range s.minutes {
		if minute > cutoff {
			last24h += s.counts[i]
		}
	}
	s.mu.Unlock()
	return total, last24h, active
}

func (s *Stats) persistenceLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer close(s.done)
	for {
		select {
		case <-ticker.C:
			_ = s.Flush(context.Background())
		case <-s.stop:
			return
		}
	}
}

// Flush atomically persists the pending total and hourly counts.
func (s *Stats) Flush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	handle := s.db
	pending := make(map[int64]int64, len(s.pending))
	var delta int64
	for hour, count := range s.pending {
		pending[hour] = count
		delta += count
	}
	s.mu.Unlock()
	if handle == nil {
		return nil
	}
	if delta == 0 {
		stored, err := loadStoredRequestHours(ctx, handle)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.stored = stored
		s.mu.Unlock()
		return nil
	}
	err := db.WithTransaction(ctx, handle, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_stats (id, requests_total) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET requests_total = requests_total + excluded.requests_total`, delta); err != nil {
			return err
		}
		for hour, count := range pending {
			if _, err := tx.ExecContext(ctx, `INSERT INTO server_stats_hourly (hour_start, requests) VALUES (?, ?) ON CONFLICT(hour_start) DO UPDATE SET requests = requests + excluded.requests`, hour, count); err != nil {
				return err
			}
		}
		cutoff := time.Now().Unix()/3600*3600 - 23*3600
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_stats_hourly WHERE hour_start < ?`, cutoff); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		s.mu.Lock()
		for hour, count := range pending {
			s.pending[hour] += count
		}
		s.mu.Unlock()
		return fmt.Errorf("request statistics: persist counters")
	}
	s.mu.Lock()
	for hour, count := range pending {
		s.pending[hour] -= count
		if s.pending[hour] <= 0 {
			delete(s.pending, hour)
		}
	}
	s.mu.Unlock()
	stored, err := loadStoredRequestHours(ctx, handle)
	if err == nil {
		s.mu.Lock()
		s.stored = stored
		s.mu.Unlock()
	}
	return err
}

// Stop ends the periodic writer and persists the final batch during shutdown.
func (s *Stats) Stop(ctx context.Context) error {
	if s == nil || s.stop == nil {
		return nil
	}
	s.stopOnce.Do(func() { close(s.stop) })
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Flush(ctx)
}

func loadStoredRequestHours(ctx context.Context, handle db.Handle) (map[int64]int64, error) {
	cutoff := time.Now().Unix()/3600*3600 - 23*3600
	rows, err := handle.QueryContext(ctx, `SELECT hour_start, requests FROM server_stats_hourly WHERE hour_start >= ?`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("request statistics: load hourly counters")
	}
	defer rows.Close()
	stored := make(map[int64]int64)
	for rows.Next() {
		var hour, count int64
		if err := rows.Scan(&hour, &count); err != nil {
			return nil, fmt.Errorf("request statistics: read hourly counters")
		}
		stored[hour] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("request statistics: read hourly counters")
	}
	return stored, nil
}
