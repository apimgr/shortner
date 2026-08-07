// Public-safe request statistics for the PART 13 health response's
// StatsInfo, per AI.md PART 13 "Field Order & Structure".
package httpserver

import (
	"sync"
	"sync/atomic"
	"time"
)

// Stats collects lifetime and rolling-24h request counts plus an in-flight
// active-connection gauge, updated from LoggingMiddleware.
type Stats struct {
	total   int64
	mu      sync.Mutex
	last24h []time.Time
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
	s.mu.Lock()
	s.last24h = append(s.last24h, time.Now())
	s.mu.Unlock()
}

// BeginRequest increments the active-connection gauge; the returned func
// decrements it and must be deferred by the caller.
func (s *Stats) BeginRequest() func() {
	atomic.AddInt64(&s.active, 1)
	return func() { atomic.AddInt64(&s.active, -1) }
}

// Snapshot returns (total, last-24h, active) as of now, pruning entries
// older than 24h.
func (s *Stats) Snapshot() (total int64, last24h int64, active int) {
	total = atomic.LoadInt64(&s.total)
	active = int(atomic.LoadInt64(&s.active))

	cutoff := time.Now().Add(-24 * time.Hour)
	s.mu.Lock()
	kept := s.last24h[:0]
	for _, t := range s.last24h {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	s.last24h = kept
	last24h = int64(len(kept))
	s.mu.Unlock()
	return total, last24h, active
}
