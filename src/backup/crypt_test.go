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
