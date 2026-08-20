package security

import (
	"testing"
	"time"
)

func TestSecurityIDDeterministic(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	a := SecurityID("s3cr3t", at)
	b := SecurityID("s3cr3t", at)
	if a != b {
		t.Errorf("SecurityID not deterministic: %q != %q", a, b)
	}
	if len(a) != securityIDLength {
		t.Errorf("len(SecurityID) = %d, want %d", len(a), securityIDLength)
	}
}

func TestSecurityIDEmptySecret(t *testing.T) {
	if got := SecurityID("", time.Now()); got != "" {
		t.Errorf("SecurityID(empty secret) = %q, want empty", got)
	}
}

func TestSecurityIDDiffersAcrossWindows(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	later := now.Add(49 * time.Hour)
	if SecurityID("s3cr3t", now) == SecurityID("s3cr3t", later) {
		t.Error("SecurityID identical across a 49h gap, want different windows")
	}
}

func TestValidSecurityIDCurrentWindow(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	id := SecurityID("s3cr3t", at)
	if !ValidSecurityID("s3cr3t", id, at) {
		t.Error("ValidSecurityID(current window) = false, want true")
	}
}

func TestValidSecurityIDPreviousWindow(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	id := SecurityID("s3cr3t", at)
	// A moment just after the window rolled over: the id computed at `at`
	// should still validate against the following window's "previous".
	next := time.Unix(at.Unix()+securityIDWindow+1, 0)
	if !ValidSecurityID("s3cr3t", id, next) {
		t.Error("ValidSecurityID(previous window) = false, want true")
	}
}

func TestValidSecurityIDRejectsTooOld(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	id := SecurityID("s3cr3t", at)
	// Two full windows later, the id is neither current nor previous.
	tooLate := time.Unix(at.Unix()+2*securityIDWindow+1, 0)
	if ValidSecurityID("s3cr3t", id, tooLate) {
		t.Error("ValidSecurityID(2 windows later) = true, want false")
	}
}

func TestValidSecurityIDRejectsWrongLength(t *testing.T) {
	at := time.Now()
	if ValidSecurityID("s3cr3t", "short", at) {
		t.Error("ValidSecurityID(wrong length id) = true, want false")
	}
}

func TestValidSecurityIDRejectsWrongSecret(t *testing.T) {
	at := time.Now()
	id := SecurityID("s3cr3t", at)
	if ValidSecurityID("different-secret", id, at) {
		t.Error("ValidSecurityID(wrong secret) = true, want false")
	}
}

func TestValidSecurityIDRejectsEmptySecret(t *testing.T) {
	at := time.Now()
	id := SecurityID("s3cr3t", at)
	if ValidSecurityID("", id, at) {
		t.Error("ValidSecurityID(empty secret) = true, want false")
	}
}
