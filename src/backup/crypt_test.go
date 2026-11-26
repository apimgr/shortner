package backup

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	plain := []byte("the quick brown fox jumps over the lazy dog")

	sealed, err := Encrypt(plain, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("ciphertext contains the plaintext")
	}
	if !strings.HasPrefix(string(sealed), encMagic) {
		t.Fatal("sealed data does not start with the magic header")
	}

	got, err := Decrypt(sealed, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Decrypt = %q, want %q", got, plain)
	}
}

func TestEncryptionHeaderContainsKDFAndBaseMetadata(t *testing.T) {
	sealed, err := Encrypt([]byte("secret payload"), "pw", "shortner_backup_2026-11-26.tar.gz.enc")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	header, err := ParseEncryptionHeader(sealed)
	if err != nil {
		t.Fatalf("ParseEncryptionHeader: %v", err)
	}
	if header.MemoryKiB != backupArgonM || header.Iterations != backupArgonT || header.Parallelism != backupArgonP {
		t.Fatalf("Argon2 parameters = (%d,%d,%d), want (%d,%d,%d)", header.MemoryKiB, header.Iterations, header.Parallelism, backupArgonM, backupArgonT, backupArgonP)
	}
	if header.BaseName != "shortner_backup_2026-11-26.tar.gz.enc" {
		t.Fatalf("BaseName = %q", header.BaseName)
	}
	if len(header.KeyID) != backupKeyIDLen {
		t.Fatalf("KeyID length = %d, want %d", len(header.KeyID), backupKeyIDLen)
	}
	keyID, err := KeyIDForPassword("pw", header)
	if err != nil || keyID != header.KeyID {
		t.Fatalf("KeyIDForPassword = %q, %v; want %q", keyID, err, header.KeyID)
	}
	other, err := KeyIDForPassword("other", header)
	if err != nil {
		t.Fatalf("KeyIDForPassword with a different password should derive an ID: %v", err)
	}
	if other == header.KeyID {
		t.Fatal("different passwords produced the same key ID")
	}
}

func TestEncryptRejectsUnsafeBaseName(t *testing.T) {
	if _, err := Encrypt([]byte("payload"), "pw", "../outside.tar.gz"); err == nil {
		t.Fatal("Encrypt accepted a path as the base filename")
	}
}

func TestParseEncryptionHeaderRejectsOversizedBaseName(t *testing.T) {
	sealed, err := Encrypt([]byte("payload"), "pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	baseLengthOffset := len(encMagic) + 16 + 12 + backupNonceLen + backupKeyIDLen
	sealed[baseLengthOffset] = 0x01
	sealed[baseLengthOffset+1] = 0x00
	if _, err := ParseEncryptionHeader(sealed); !errors.Is(err, ErrNotEncrypted) {
		t.Fatalf("ParseEncryptionHeader = %v, want ErrNotEncrypted", err)
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	sealed, err := Encrypt([]byte("secret"), "right")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(sealed, "wrong"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("Decrypt with wrong password = %v, want ErrInvalidPassword", err)
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	sealed, err := Encrypt([]byte("secret payload"), "pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	sealed[len(sealed)-1] ^= 0xff
	if _, err := Decrypt(sealed, "pw"); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
}

func TestDecryptTamperedHeaderIsAuthenticated(t *testing.T) {
	sealed, err := Encrypt([]byte("secret payload"), "pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip a salt byte: the header is GCM additional data, so this must
	// fail authentication rather than silently decrypt.
	sealed[len(encMagic)] ^= 0x01
	if _, err := Decrypt(sealed, "pw"); err == nil {
		t.Fatal("Decrypt accepted a tampered header")
	}
}

func TestEncryptRequiresPassword(t *testing.T) {
	if _, err := Encrypt([]byte("x"), ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("Encrypt with empty password = %v, want ErrPasswordRequired", err)
	}
	if _, err := Decrypt([]byte("x"), ""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("Decrypt with empty password = %v, want ErrPasswordRequired", err)
	}
}

func TestDecryptNotEncrypted(t *testing.T) {
	if _, err := Decrypt([]byte("not a sealed archive at all"), "pw"); !errors.Is(err, ErrNotEncrypted) {
		t.Fatalf("Decrypt of plain data = %v, want ErrNotEncrypted", err)
	}
}

func TestIsEncryptedName(t *testing.T) {
	cases := map[string]bool{
		"shortner_backup_2025-01-15.tar.gz":      false,
		"shortner_backup_2025-01-15.tar.gz.enc":  true,
		"/var/backups/shortner-daily.tar.gz.enc": true,
		"shortner-daily.tar.gz":                  false,
	}
	for name, want := range cases {
		if got := IsEncryptedName(name); got != want {
			t.Errorf("IsEncryptedName(%q) = %t, want %t", name, got, want)
		}
	}
}
