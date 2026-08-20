package httpserver

import (
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
)

// TestAllowlistLookupContains covers a matching CIDR, a non-matching IP,
// an unparseable IP, an empty lookup, and a malformed config entry that
// must be skipped rather than crashing NewAllowlistLookup.
func TestAllowlistLookupContains(t *testing.T) {
	l := NewAllowlistLookup([]config.AllowlistEntry{
		{CIDR: "10.0.0.0/8"},
		{CIDR: "not-a-cidr"},
	})

	if !l.Contains("10.1.2.3") {
		t.Error("expected 10.1.2.3 to be allowlisted under 10.0.0.0/8")
	}
	if l.Contains("192.168.1.1") {
		t.Error("expected 192.168.1.1 to not be allowlisted")
	}
	if l.Contains("not-an-ip") {
		t.Error("expected an unparseable address to never match")
	}

	empty := NewAllowlistLookup(nil)
	if empty.Contains("10.1.2.3") {
		t.Error("expected an empty allowlist to match nothing")
	}
	var nilLookup *AllowlistLookup
	if nilLookup.Contains("10.1.2.3") {
		t.Error("expected a nil *AllowlistLookup to match nothing")
	}
}

// TestBlockStorePermanentBlock proves a config-file entry blocks matching
// IPs and carries its configured reason.
func TestBlockStorePermanentBlock(t *testing.T) {
	s := NewBlockStore([]config.BlockedIPEntry{
		{CIDR: "203.0.113.0/24", Reason: "known spammer"},
	}, nil)

	block, blocked := s.Blocked("203.0.113.5", time.Now())
	if !blocked {
		t.Fatal("expected 203.0.113.5 to be blocked by the permanent entry")
	}
	if block.Type != BlockTypePermanent {
		t.Errorf("Type = %q, want permanent", block.Type)
	}
	if block.Reason != "known spammer" {
		t.Errorf("Reason = %q, want %q", block.Reason, "known spammer")
	}

	if _, blocked := s.Blocked("198.51.100.1", time.Now()); blocked {
		t.Error("expected an unrelated IP to not be blocked")
	}
}

// TestBlockStoreTemporaryBlockExpiry proves a temporary block is active
// before its window ends and is treated as released at read time (even
// before ReleaseExpired runs), and that BlockTemporarily bumps the offense
// count across calls for the same IP.
func TestBlockStoreTemporaryBlockExpiry(t *testing.T) {
	s := NewBlockStore(nil, nil)
	now := time.Now()

	s.BlockTemporarily("9.9.9.9", "request flood", time.Minute, now)
	if _, blocked := s.Blocked("9.9.9.9", now.Add(30*time.Second)); !blocked {
		t.Error("expected the IP to still be blocked inside its window")
	}
	if _, blocked := s.Blocked("9.9.9.9", now.Add(2*time.Minute)); blocked {
		t.Error("expected the IP to be released once its window has passed, even without a sweep")
	}

	// A fresh IP is used here so the count starts from zero — offense
	// counts are cumulative per IP and the address above already has one.
	s.BlockTemporarily("9.9.9.10", "request flood", time.Minute, now)
	block, _ := s.Blocked("9.9.9.10", now)
	if block.OffenseCount != 1 {
		t.Errorf("first BlockTemporarily OffenseCount = %d, want 1", block.OffenseCount)
	}
	s.BlockTemporarily("9.9.9.10", "request flood", time.Minute, now)
	block2, _ := s.Blocked("9.9.9.10", now)
	if block2.OffenseCount != 2 {
		t.Errorf("second BlockTemporarily OffenseCount = %d, want 2 (cumulative)", block2.OffenseCount)
	}
}

// TestBlockStoreUnblock covers removing an existing temporary block and
// the false-return no-op when the IP was never blocked.
func TestBlockStoreUnblock(t *testing.T) {
	s := NewBlockStore(nil, nil)
	now := time.Now()
	s.BlockTemporarily("9.9.9.9", "flood", time.Hour, now)

	if !s.Unblock("9.9.9.9", "manual release") {
		t.Fatal("expected Unblock to report true for an existing temporary block")
	}
	if _, blocked := s.Blocked("9.9.9.9", now); blocked {
		t.Error("expected the IP to be unblocked")
	}
	if s.Unblock("9.9.9.9", "manual release") {
		t.Error("expected Unblock to report false for an already-released IP")
	}
}

// TestBlockStoreReleaseExpired proves the sweep releases only expired
// entries and reports the correct count.
func TestBlockStoreReleaseExpired(t *testing.T) {
	s := NewBlockStore(nil, nil)
	now := time.Now()
	s.BlockTemporarily("1.1.1.1", "flood", time.Minute, now)
	s.BlockTemporarily("2.2.2.2", "flood", time.Hour, now)

	released := s.ReleaseExpired(now.Add(2 * time.Minute))
	if released != 1 {
		t.Fatalf("ReleaseExpired() = %d, want 1", released)
	}
	if _, blocked := s.Blocked("1.1.1.1", now.Add(2*time.Minute)); blocked {
		t.Error("expected 1.1.1.1 to be released")
	}
	if _, blocked := s.Blocked("2.2.2.2", now.Add(2*time.Minute)); !blocked {
		t.Error("expected 2.2.2.2 to still be blocked")
	}

	if got := s.ReleaseExpired(now.Add(2 * time.Minute)); got != 0 {
		t.Errorf("second ReleaseExpired() = %d, want 0 (already released)", got)
	}
}

// TestBlockStoreReleaseAllowlisted proves an IP that is added to the
// allowlist has its temporary block released, and that a nil allowlist is
// a safe no-op.
func TestBlockStoreReleaseAllowlisted(t *testing.T) {
	s := NewBlockStore(nil, nil)
	now := time.Now()
	s.BlockTemporarily("5.5.5.5", "flood", time.Hour, now)

	if got := s.ReleaseAllowlisted(nil); got != 0 {
		t.Errorf("ReleaseAllowlisted(nil) = %d, want 0", got)
	}
	if _, blocked := s.Blocked("5.5.5.5", now); !blocked {
		t.Fatal("expected the block to still be present after a nil allowlist call")
	}

	allow := NewAllowlistLookup([]config.AllowlistEntry{{CIDR: "5.5.5.5/32"}})
	if got := s.ReleaseAllowlisted(allow); got != 1 {
		t.Errorf("ReleaseAllowlisted() = %d, want 1", got)
	}
	if _, blocked := s.Blocked("5.5.5.5", now); blocked {
		t.Error("expected 5.5.5.5 to be released once allowlisted")
	}
}

// TestAbuseDetectorNilWhenDisabled proves NewAbuseDetector returns nil
// when disabled or given a non-positive rate limit, and that a nil
// detector's methods are safe to call.
func TestAbuseDetectorNilWhenDisabled(t *testing.T) {
	if d := NewAbuseDetector(config.AbuseDetection{Enabled: false}, 100); d != nil {
		t.Error("expected nil detector when abuse detection is disabled")
	}
	if d := NewAbuseDetector(config.AbuseDetection{Enabled: true}, 0); d != nil {
		t.Error("expected nil detector when the per-minute limit is non-positive")
	}

	var nilDetector *AbuseDetector
	if nilDetector.Record("1.2.3.4", time.Now()) {
		t.Error("expected a nil detector's Record to return false")
	}
	if nilDetector.BlockDuration() != 0 {
		t.Error("expected a nil detector's BlockDuration to be 0")
	}
}

// TestAbuseDetectorRecordTriggersAtThreshold proves Record returns true
// exactly once, on the request that crosses the threshold, and false
// before and after.
func TestAbuseDetectorRecordTriggersAtThreshold(t *testing.T) {
	d := NewAbuseDetector(config.AbuseDetection{
		Enabled:      true,
		AutoBlockIP:  true,
		RequestFlood: config.RequestFlood{Multiplier: 2, BlockDuration: "1h"},
	}, 5)
	if d == nil {
		t.Fatal("expected a non-nil detector")
	}
	// threshold = 5 * 2 = 10
	now := time.Now()
	for i := 0; i < 9; i++ {
		if d.Record("1.2.3.4", now) {
			t.Fatalf("Record() returned true early on request %d", i+1)
		}
	}
	if !d.Record("1.2.3.4", now) {
		t.Error("expected Record() to return true on the 10th request (threshold crossed)")
	}
	if d.Record("1.2.3.4", now) {
		t.Error("expected Record() to return false once past the threshold")
	}
	if d.BlockDuration() != time.Hour {
		t.Errorf("BlockDuration() = %v, want 1h", d.BlockDuration())
	}
}

// TestAbuseDetectorRecordWithoutAutoBlock proves the threshold is still
// tracked but Record never reports true when AutoBlockIP is off.
func TestAbuseDetectorRecordWithoutAutoBlock(t *testing.T) {
	d := NewAbuseDetector(config.AbuseDetection{
		Enabled:      true,
		AutoBlockIP:  false,
		RequestFlood: config.RequestFlood{Multiplier: 1, BlockDuration: "1h"},
	}, 1)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if d.Record("1.2.3.4", now) {
			t.Errorf("Record() returned true on request %d with AutoBlockIP disabled", i+1)
		}
	}
}

// TestAbuseDetectorWindowReset proves the per-window counter resets, so a
// steady legal rate never accumulates a block across windows.
func TestAbuseDetectorWindowReset(t *testing.T) {
	d := NewAbuseDetector(config.AbuseDetection{
		Enabled:      true,
		AutoBlockIP:  true,
		RequestFlood: config.RequestFlood{Multiplier: 1, BlockDuration: "1h"},
	}, 2)
	// threshold = 2
	now := time.Now()
	if d.Record("1.2.3.4", now) {
		t.Fatal("first request should not trigger")
	}
	// Advance past the one-minute window before the second request.
	later := now.Add(2 * time.Minute)
	if d.Record("1.2.3.4", later) {
		t.Error("expected the counter to reset after the window elapsed, so the 2nd request overall does not trigger")
	}
}
