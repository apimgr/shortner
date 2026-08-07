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

func TestStatsSnapshotPrunesOld(t *testing.T) {
	s := NewStats()
	s.mu.Lock()
	s.last24h = []time.Time{time.Now().Add(-25 * time.Hour), time.Now().Add(-1 * time.Hour)}
	s.mu.Unlock()
	s.total = 2

	total, last24h, _ := s.Snapshot()
	if total != 2 {
		t.Errorf("total = %d, want 2 (lifetime total unaffected by pruning)", total)
	}
	if last24h != 1 {
		t.Errorf("last24h = %d, want 1 (stale entry pruned)", last24h)
	}
}
