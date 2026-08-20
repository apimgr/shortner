package security

import (
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := []byte("01234567890123456789012345678901"[:32])
	plaintext := []byte("the quick brown fox jumps")

	blob, err := Seal(key, plaintext)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	got, err := Open(key, blob)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("Open() = %q, want %q", got, plaintext)
	}
}

func TestSealOpenEmptyPlaintext(t *testing.T) {
	key := make([]byte, 32)
	blob, err := Seal(key, nil)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	got, err := Open(key, blob)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Open() = %q, want empty", got)
	}
}

func TestSealProducesDistinctBlobs(t *testing.T) {
	key := make([]byte, 32)
	a, err := Seal(key, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	b, err := Seal(key, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if a == b {
		t.Errorf("two Seal() calls on identical plaintext produced identical blobs: %q", a)
	}
}

func TestSealRejectsWrongKeySize(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33, 64} {
		if _, err := Seal(make([]byte, size), []byte("x")); err != ErrSealKeySize {
			t.Errorf("Seal() with %d-byte key: error = %v, want ErrSealKeySize", size, err)
		}
	}
}

func TestOpenRejectsWrongKeySize(t *testing.T) {
	if _, err := Open(make([]byte, 10), "anything"); err != ErrSealKeySize {
		t.Errorf("Open() with wrong key size: error = %v, want ErrSealKeySize", err)
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 1

	blob, err := Seal(key1, []byte("secret"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := Open(key2, blob); err == nil {
		t.Error("Open() with wrong key = nil error, want error")
	}
}

func TestOpenRejectsMalformedBase64(t *testing.T) {
	key := make([]byte, 32)
	if _, err := Open(key, "not valid base64!!!"); err == nil {
		t.Error("Open() with malformed base64 = nil error, want error")
	}
}

func TestOpenRejectsTruncatedBlob(t *testing.T) {
	key := make([]byte, 32)
	// Fewer bytes than the GCM nonce size (12), but valid base64.
	if _, err := Open(key, "YWJj"); err == nil {
		t.Error("Open() with truncated blob = nil error, want error")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	blob, err := Seal(key, []byte("tamper me"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	// Flip the last character of the base64 payload to corrupt the tag.
	tampered := blob[:len(blob)-1]
	if strings.HasSuffix(blob, "A") {
		tampered += "B"
	} else {
		tampered += "A"
	}

	if _, err := Open(key, tampered); err == nil {
		t.Error("Open() with tampered ciphertext = nil error, want error")
	}
}
