package httpserver

import (
	"testing"
	"time"
)

func TestStatsRecordRequest(t *testing.T) {
	s := NewStats()
	s.RecordRequest()
	s.RecordRequest()

	total, last24h, _ := s.Snapshot()
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if last24h != 2 {
		t.Errorf("last24h = %d, want 2", last24h)
	}
}

func TestStatsBeginRequestTracksActive(t *testing.T) {
	s := NewStats()

	end1 := s.BeginRequest()
	_, _, active := s.Snapshot()
	if active != 1 {
		t.Errorf("active = %d, want 1", active)
	}

	end2 := s.BeginRequest()
	_, _, active = s.Snapshot()
	if active != 2 {
		t.Errorf("active = %d, want 2", active)
	}

	end1()
	_, _, active = s.Snapshot()
	if active != 1 {
		t.Errorf("active = %d, want 1 after one end", active)
	}

	end2()
	_, _, active = s.Snapshot()
	if active != 0 {
		t.Errorf("active = %d, want 0 after both ends", active)
	}
}

func TestStatsSnapshotIgnoresStaleBuckets(t *testing.T) {
	s := NewStats()
	nowMinute := time.Now().Unix() / 60

	// One bucket from 26h ago (outside the window) and one from 1h ago
	// (inside it). 26h and 1h are 1500 minutes apart, which is not a
	// multiple of statsBuckets, so they land in distinct ring slots.
	stale := nowMinute - 26*60
	fresh := nowMinute - 60
	s.mu.Lock()
	s.minutes[int(stale%statsBuckets)] = stale
	s.counts[int(stale%statsBuckets)] = 1
	s.minutes[int(fresh%statsBuckets)] = fresh
	s.counts[int(fresh%statsBuckets)] = 1
	s.mu.Unlock()
	s.total = 2

	total, last24h, _ := s.Snapshot()
	if total != 2 {
		t.Errorf("total = %d, want 2 (lifetime total unaffected by windowing)", total)
	}
	if last24h != 1 {
		t.Errorf("last24h = %d, want 1 (stale bucket ignored)", last24h)
	}
}

func TestStatsMemoryIsBounded(t *testing.T) {
	s := NewStats()
	for i := 0; i < 10000; i++ {
		s.RecordRequest()
	}
	if len(s.counts) != statsBuckets || len(s.minutes) != statsBuckets {
		t.Fatalf("ring size changed: counts=%d minutes=%d, want %d",
			len(s.counts), len(s.minutes), statsBuckets)
	}
	total, last24h, _ := s.Snapshot()
	if total != 10000 {
		t.Errorf("total = %d, want 10000", total)
	}
	if last24h != 10000 {
		t.Errorf("last24h = %d, want 10000", last24h)
	}
}
