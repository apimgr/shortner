// Public-safe request statistics for the PART 13 health response's
// StatsInfo, per AI.md PART 13 "Field Order & Structure".
package httpserver

import (
	"sync"
	"sync/atomic"
	"time"
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
	counts  [statsBuckets]int64
	minutes [statsBuckets]int64
	active  int64
}

// NewStats returns an empty Stats collector.
func NewStats() *Stats {
	return &Stats{}
}

// RecordRequest increments the lifetime and 24h counters for one completed
// request.
func (s *Stats) RecordRequest() {
	atomic.AddInt64(&s.total, 1)

	minute := time.Now().Unix() / 60
	idx := int(minute % statsBuckets)

	s.mu.Lock()
	if s.minutes[idx] != minute {
		s.minutes[idx] = minute
		s.counts[idx] = 0
	}
	s.counts[idx]++
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

	cutoff := time.Now().Unix()/60 - statsBuckets
	s.mu.Lock()
	for i, minute := range s.minutes {
		if minute > cutoff {
			last24h += s.counts[i]
		}
	}
	s.mu.Unlock()
	return total, last24h, active
}
