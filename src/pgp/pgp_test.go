package pgp

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testKey generates one keypair for a test, failing fast on error.
func testKey(t *testing.T) *Keypair {
	t.Helper()
	key, err := Generate("Shortner", "security@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	return key
}

func TestIdentity(t *testing.T) {
	tests := []struct {
		name    string
		app     string
		email   string
		want    string
		wantSub string
	}{
		{name: "full", app: "Shortner", email: "security@example.com", want: "Shortner Security <security@example.com>"},
		{name: "no email", app: "Shortner", email: "", want: "Shortner Security"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Identity(tt.app, tt.email); got != tt.want {
				t.Errorf("Identity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGenerateProducesUsableKeypair(t *testing.T) {
	now := time.Now().UTC()
	key, err := Generate("Shortner", "security@example.com", now)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if key.Fingerprint == "" {
		t.Error("Fingerprint is empty")
	}
	if key.PrimaryEmail() != "security@example.com" {
		t.Errorf("PrimaryEmail() = %q", key.PrimaryEmail())
	}
	wantExpiry := now.AddDate(KeyLifetimeYears, 0, 0)
	if diff := key.ExpiresAt.Sub(wantExpiry); diff > time.Hour || diff < -time.Hour {
		t.Errorf("ExpiresAt = %v, want about %v", key.ExpiresAt, wantExpiry)
	}
}

func TestArmorRoundTrip(t *testing.T) {
	key := testKey(t)

	pub, err := key.ArmorPublic()
	if err != nil {
		t.Fatalf("ArmorPublic() error = %v", err)
	}
	if !strings.HasPrefix(pub, "-----BEGIN PGP PUBLIC KEY BLOCK-----") {
		t.Errorf("public armor has wrong header: %.40q", pub)
	}
	if _, err := ParsePublic(pub); err != nil {
		t.Fatalf("ParsePublic() error = %v", err)
	}

	priv, err := key.ArmorPrivate()
	if err != nil {
		t.Fatalf("ArmorPrivate() error = %v", err)
	}
	parsed, err := ParsePrivate(priv)
	if err != nil {
		t.Fatalf("ParsePrivate() error = %v", err)
	}
	if parsed.Fingerprint != key.Fingerprint {
		t.Errorf("fingerprint changed across round trip: %s vs %s", parsed.Fingerprint, key.Fingerprint)
	}
}

func TestSealPrivateRoundTrip(t *testing.T) {
	key := testKey(t)
	armored, err := key.ArmorPrivate()
	if err != nil {
		t.Fatalf("ArmorPrivate() error = %v", err)
	}

	sealed, err := SealPrivate(armored, "installation-secret")
	if err != nil {
		t.Fatalf("SealPrivate() error = %v", err)
	}
	if strings.Contains(string(sealed), "PGP PRIVATE KEY") {
		t.Error("sealed blob leaks the armored key")
	}

	opened, err := OpenPrivate(sealed, "installation-secret")
	if err != nil {
		t.Fatalf("OpenPrivate() error = %v", err)
	}
	if opened != armored {
		t.Error("OpenPrivate() did not return the original armor")
	}

	if _, err := OpenPrivate(sealed, "wrong-secret"); err == nil {
		t.Error("OpenPrivate() with the wrong secret should fail")
	}
	if _, err := SealPrivate(armored, ""); err == nil {
		t.Error("SealPrivate() with no installation secret should fail")
	}
}

func TestEncryptDecryptArmored(t *testing.T) {
	key := testKey(t)
	pub, err := key.ArmorPublic()
	if err != nil {
		t.Fatalf("ArmorPublic() error = %v", err)
	}
	recipients, err := ParsePublic(pub)
	if err != nil {
		t.Fatalf("ParsePublic() error = %v", err)
	}

	plaintext := []byte(`{"summary":"an example finding"}`)
	armored, err := EncryptArmored(recipients, plaintext)
	if err != nil {
		t.Fatalf("EncryptArmored() error = %v", err)
	}
	if !IsArmoredMessage(armored) {
		t.Errorf("EncryptArmored() output is not an armored message: %.40q", armored)
	}
	if strings.Contains(armored, "an example finding") {
		t.Error("ciphertext contains the plaintext")
	}

	got, err := DecryptArmored(key, armored)
	if err != nil {
		t.Fatalf("DecryptArmored() error = %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("DecryptArmored() = %q, want %q", got, plaintext)
	}

	if _, err := EncryptArmored(nil, plaintext); err == nil {
		t.Error("EncryptArmored() with no recipients should fail")
	}
}

func TestStoreWriteReadDelete(t *testing.T) {
	store := NewStore(t.TempDir())
	if store.HasKeypair() {
		t.Fatal("HasKeypair() true on an empty store")
	}
	if _, err := store.ReadPublicKey(); err == nil {
		t.Error("ReadPublicKey() on an empty store should fail")
	}

	key := testKey(t)
	if err := store.Write(key, "installation-secret"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !store.HasKeypair() {
		t.Fatal("HasKeypair() false after Write()")
	}
	if filepath.Base(store.PublicKeyPath()) != PublicKeyName {
		t.Errorf("PublicKeyPath() = %s", store.PublicKeyPath())
	}

	pub, err := store.ReadPublicKey()
	if err != nil {
		t.Fatalf("ReadPublicKey() error = %v", err)
	}
	if _, err := ParsePublic(pub); err != nil {
		t.Fatalf("stored public key does not parse: %v", err)
	}

	priv, err := store.ReadPrivateKey("installation-secret")
	if err != nil {
		t.Fatalf("ReadPrivateKey() error = %v", err)
	}
	if priv.Fingerprint != key.Fingerprint {
		t.Errorf("fingerprint = %s, want %s", priv.Fingerprint, key.Fingerprint)
	}

	if err := store.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if store.HasKeypair() {
		t.Error("HasKeypair() true after Delete()")
	}
}

func TestStoreRetireKeepsOldKeyReadable(t *testing.T) {
	store := NewStore(t.TempDir())
	old := testKey(t)
	if err := store.Write(old, "installation-secret"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	now := time.Now().UTC()
	if err := store.Retire(now); err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	newKey := testKey(t)
	if err := store.Write(newKey, "installation-secret"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	prev, err := store.PreviousKey("installation-secret", now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("PreviousKey() error = %v", err)
	}
	if prev.Fingerprint != old.Fingerprint {
		t.Errorf("PreviousKey() fingerprint = %s, want %s", prev.Fingerprint, old.Fingerprint)
	}

	past := now.AddDate(0, 0, PreviousKeyGraceDays+1)
	if _, err := store.PreviousKey("installation-secret", past); err == nil {
		t.Error("PreviousKey() past the grace window should fail")
	}
}

func TestSignPublicKeyOf(t *testing.T) {
	old := testKey(t)
	next := testKey(t)
	if err := SignPublicKeyOf(next, old, time.Now().UTC()); err != nil {
		t.Fatalf("SignPublicKeyOf() error = %v", err)
	}
	armored, err := next.ArmorPublic()
	if err != nil {
		t.Fatalf("ArmorPublic() error = %v", err)
	}
	if _, err := ParsePublic(armored); err != nil {
		t.Fatalf("cross-signed key does not parse: %v", err)
	}
}

func TestUploadURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "bare host", in: "keys.openpgp.org", want: "https://keys.openpgp.org/vks/v1/upload"},
		{name: "https host", in: "https://keys.openpgp.org", want: "https://keys.openpgp.org/vks/v1/upload"},
		{name: "explicit path", in: "https://example.com/submit", want: "https://example.com/submit"},
		{name: "http rejected", in: "http://keys.openpgp.org", wantErr: true},
		{name: "empty rejected", in: "   ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UploadURL(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("UploadURL(%q) error = nil, want an error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("UploadURL(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("UploadURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
