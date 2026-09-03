package security

import (
	"bytes"
	"testing"
)

func TestDeriveKeyDeterministic(t *testing.T) {
	salt := bytes.Repeat([]byte{0x42}, SaltLen)
	a := DeriveKey("hunter2", salt)
	b := DeriveKey("hunter2", salt)
	if !bytes.Equal(a, b) {
		t.Error("DeriveKey not deterministic for the same password and salt")
	}
	if len(a) != DeriveKeyLen {
		t.Errorf("len(DeriveKey) = %d, want %d", len(a), DeriveKeyLen)
	}
}

func TestDeriveKeyDiffersByPassword(t *testing.T) {
	salt := bytes.Repeat([]byte{0x42}, SaltLen)
	a := DeriveKey("hunter2", salt)
	b := DeriveKey("hunter3", salt)
	if bytes.Equal(a, b) {
		t.Error("DeriveKey identical for different passwords, want different keys")
	}
}

func TestDeriveKeyDiffersBySalt(t *testing.T) {
	saltA := bytes.Repeat([]byte{0x42}, SaltLen)
	saltB := bytes.Repeat([]byte{0x24}, SaltLen)
	a := DeriveKey("hunter2", saltA)
	b := DeriveKey("hunter2", saltB)
	if bytes.Equal(a, b) {
		t.Error("DeriveKey identical for different salts, want different keys")
	}
}

func TestVerifyPasswordRejectsWrongFieldCount(t *testing.T) {
	if _, err := VerifyPassword("x", "$argon2id$v=19$m=1,t=1,p=1$onlyfourfields"); err == nil {
		t.Error("VerifyPassword() error = nil, want error for wrong field count")
	}
}

func TestVerifyPasswordRejectsBadSalt(t *testing.T) {
	encoded := "$argon2id$v=19$m=65536,t=3,p=2$not-valid-base64!!$AAAA"
	if _, err := VerifyPassword("x", encoded); err == nil {
		t.Error("VerifyPassword() error = nil, want error for malformed salt")
	}
}

func TestVerifyPasswordRejectsBadHash(t *testing.T) {
	salt := "AAAAAAAAAAAAAAAAAAAAAA"
	encoded := "$argon2id$v=19$m=65536,t=3,p=2$" + salt + "$not-valid-base64!!"
	if _, err := VerifyPassword("x", encoded); err == nil {
		t.Error("VerifyPassword() error = nil, want error for malformed hash")
	}
}

func TestVerifyPasswordRejectsBadParams(t *testing.T) {
	encoded := "$argon2id$v=19$not-params$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAA"
	if _, err := VerifyPassword("whatever", encoded); err == nil {
		t.Error("VerifyPassword() error = nil, want error for malformed params")
	}
}
