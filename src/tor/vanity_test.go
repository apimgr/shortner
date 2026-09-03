package tor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha3"
	"encoding/base32"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// OnionAddressFor must produce a 56-character v3 address ending in
// ".onion", and decoding it back must recover the exact public key,
// version byte, and SHA3-256 checksum per rend-spec-v3 §6 — the on-disk
// format Tor itself parses.
func TestOnionAddressForRoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	addr := OnionAddressFor(pub)

	if !strings.HasSuffix(addr, ".onion") {
		t.Fatalf("address %q does not end in .onion", addr)
	}
	const wantLen = 56 + len(".onion")
	if len(addr) != wantLen {
		t.Fatalf("address length = %d, want %d (%q)", len(addr), wantLen, addr)
	}
	if addr != strings.ToLower(addr) {
		t.Errorf("address %q is not lowercase", addr)
	}

	encoded := strings.ToUpper(strings.TrimSuffix(addr, ".onion"))
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(encoded)
	if err != nil {
		t.Fatalf("failed to decode address body: %v", err)
	}
	if len(decoded) != ed25519.PublicKeySize+3 {
		t.Fatalf("decoded length = %d, want %d", len(decoded), ed25519.PublicKeySize+3)
	}

	gotPub := decoded[:ed25519.PublicKeySize]
	gotChecksum := decoded[ed25519.PublicKeySize : ed25519.PublicKeySize+2]
	gotVersion := decoded[ed25519.PublicKeySize+2]

	if !ed25519.PublicKey(gotPub).Equal(pub) {
		t.Error("decoded public key does not match the original")
	}
	if gotVersion != 0x03 {
		t.Errorf("decoded version byte = %#x, want 0x03", gotVersion)
	}

	h := sha3.New256()
	h.Write([]byte(".onion checksum"))
	h.Write(pub)
	h.Write([]byte{0x03})
	wantChecksum := h.Sum(nil)[:2]
	if string(gotChecksum) != string(wantChecksum) {
		t.Errorf("decoded checksum = %x, want %x", gotChecksum, wantChecksum)
	}
}

// ExpandedSecretKey must clamp the scalar half exactly as ed25519 requires:
// the low 3 bits of byte 0 clear, bit 254 (bit 6 of byte 31) set, bit 255
// (bit 7 of byte 31) clear — otherwise Tor would load a malformed key.
func TestExpandedSecretKeyClamping(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	expanded := ExpandedSecretKey(seed)
	if len(expanded) != 64 {
		t.Fatalf("len(expanded) = %d, want 64", len(expanded))
	}
	if expanded[0]&7 != 0 {
		t.Errorf("byte 0 low bits not cleared: %08b", expanded[0])
	}
	if expanded[31]&64 == 0 {
		t.Errorf("byte 31 bit 6 not set: %08b", expanded[31])
	}
	if expanded[31]&128 != 0 {
		t.Errorf("byte 31 bit 7 not cleared: %08b", expanded[31])
	}
}

// SecretKeyFile and PublicKeyFile must each start with Tor's exact 32-byte
// NUL-padded header, followed by the key material verbatim.
func TestSecretAndPublicKeyFileHeaders(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	sec := SecretKeyFile(priv)
	if len(sec) != 96 {
		t.Fatalf("len(SecretKeyFile) = %d, want 96", len(sec))
	}
	if string(sec[:len("== ed25519v1-secret: type0 ==")]) != "== ed25519v1-secret: type0 ==" {
		t.Errorf("secret key file header = %q", sec[:30])
	}
	for _, b := range sec[len("== ed25519v1-secret: type0 =="):32] {
		if b != 0 {
			t.Fatal("secret key header not NUL-padded to 32 bytes")
		}
	}
	if string(sec[32:]) != string(ExpandedSecretKey(priv.Seed())) {
		t.Error("secret key material does not match ExpandedSecretKey(priv.Seed())")
	}

	pubFile := PublicKeyFile(pub)
	if len(pubFile) != 32+ed25519.PublicKeySize {
		t.Fatalf("len(PublicKeyFile) = %d, want %d", len(pubFile), 32+ed25519.PublicKeySize)
	}
	if string(pubFile[:len("== ed25519v1-public: type0 ==")]) != "== ed25519v1-public: type0 ==" {
		t.Errorf("public key file header = %q", pubFile[:30])
	}
	if !ed25519.PublicKey(pubFile[32:]).Equal(pub) {
		t.Error("public key material does not match the original")
	}
}

// ValidateSecretKeyFile must accept a file this package generated and
// reject truncated or garbage input, since an unvalidated import would
// leave Tor unable to start with no useful diagnostic.
func TestValidateSecretKeyFile(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	valid := SecretKeyFile(priv)

	if err := ValidateSecretKeyFile(valid); err != nil {
		t.Errorf("ValidateSecretKeyFile(valid) error = %v", err)
	}

	cases := map[string][]byte{
		"empty":        {},
		"too short":    valid[:50],
		"too long":     append(append([]byte{}, valid...), 0xff),
		"wrong header": append([]byte("== not a real header ========="), valid[32:]...),
		"non-nul header pad": func() []byte {
			bad := append([]byte{}, valid...)
			bad[len("== ed25519v1-secret: type0 ==")] = 0x01
			return bad
		}(),
		"bad clamping": func() []byte {
			bad := append([]byte{}, valid...)
			bad[32] |= 1
			return bad
		}(),
		"all zero": make([]byte, 96),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSecretKeyFile(data); err == nil {
				t.Errorf("ValidateSecretKeyFile(%s) accepted invalid data", name)
			}
		})
	}
}

// ValidVanityPrefix must accept only characters that can appear in base32
// (a-z, 2-7), reject the empty and over-long cases, and reject 0/1/8/9
// specifically because they can never appear in an onion address.
func TestValidVanityPrefix(t *testing.T) {
	valid := []string{"a", "z", "abc", "234567", strings.Repeat("a", 16)}
	for _, p := range valid {
		if err := ValidVanityPrefix(p); err != nil {
			t.Errorf("ValidVanityPrefix(%q) error = %v, want nil", p, err)
		}
	}

	invalid := map[string]string{
		"empty":       "",
		"too long":    strings.Repeat("a", 17),
		"digit 0":     "0",
		"digit 1":     "1",
		"digit 8":     "8",
		"digit 9":     "9",
		"punctuation": "a-b",
	}
	for name, p := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidVanityPrefix(p); err == nil {
				t.Errorf("ValidVanityPrefix(%q) = nil, want an error", p)
			}
		})
	}
}

// SearchVanity with a one-character prefix must find a match quickly
// (expected ~32 tries) and the returned key pair must actually derive the
// reported address.
func TestSearchVanityFindsOneCharPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	res, err := SearchVanity(ctx, "a")
	if err != nil {
		t.Fatalf("SearchVanity() error = %v", err)
	}
	if !strings.HasPrefix(res.Address, "a") {
		t.Errorf("Address %q does not start with the requested prefix", res.Address)
	}
	if got := OnionAddressFor(res.Public); got != res.Address {
		t.Errorf("OnionAddressFor(result.Public) = %q, want %q", got, res.Address)
	}
	if res.Tried == 0 {
		t.Error("Tried should be greater than 0")
	}
	if len(res.Private) != ed25519.PrivateKeySize {
		t.Errorf("len(Private) = %d, want %d", len(res.Private), ed25519.PrivateKeySize)
	}
}

// SearchVanity must return ErrVanityNotFound, not hang, when the context is
// already cancelled before a match is found — the operator-facing "search
// cancelled" path.
func TestSearchVanityCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// A prefix long enough that it will not be found before the already
	// cancelled context is observed.
	_, err := SearchVanity(ctx, "abcdefgh")
	if !errors.Is(err, ErrVanityNotFound) {
		t.Errorf("SearchVanity() error = %v, want ErrVanityNotFound", err)
	}
}

// SearchVanity must reject an invalid prefix before spawning any workers.
func TestSearchVanityInvalidPrefix(t *testing.T) {
	_, err := SearchVanity(t.Context(), "019")
	if err == nil {
		t.Fatal("expected an error for a prefix containing non-base32 digits")
	}
}

// VanityDir, SaveVanity and ListVanity must round-trip: a saved result
// lands under the documented staging path in Tor's own key-file layout,
// and ListVanity must report it back.
func TestVanitySaveAndList(t *testing.T) {
	dataDir := t.TempDir()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	addr := OnionAddressFor(pub)
	res := &VanityResult{Address: addr, Public: pub, Private: priv, Tried: 42}

	wantDir := VanityDir(dataDir, addr)
	gotDir, err := SaveVanity(dataDir, res)
	if err != nil {
		t.Fatalf("SaveVanity() error = %v", err)
	}
	if gotDir != wantDir {
		t.Errorf("SaveVanity() dir = %q, want %q", gotDir, wantDir)
	}

	for _, name := range []string{"hs_ed25519_secret_key", "hs_ed25519_public_key", "hostname"} {
		if _, statErr := os.Stat(filepath.Join(gotDir, name)); statErr != nil {
			t.Errorf("expected %s to exist under %s: %v", name, gotDir, statErr)
		}
	}

	// Save a second identity to confirm ListVanity reports every staged
	// entry, not just the most recent.
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	addr2 := OnionAddressFor(pub2)
	res2 := &VanityResult{Address: addr2, Public: pub2, Private: priv2, Tried: 1}
	if _, err := SaveVanity(dataDir, res2); err != nil {
		t.Fatalf("SaveVanity() second result error = %v", err)
	}

	got := ListVanity(dataDir)
	sort.Strings(got)
	want := []string{addr, addr2}
	sort.Strings(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ListVanity() = %v, want %v", got, want)
	}
}

// ListVanity must return nil rather than error when nothing has ever been
// staged, since it backs a status listing that must not fail on a fresh
// install.
func TestListVanityEmpty(t *testing.T) {
	dataDir := t.TempDir()
	got := ListVanity(dataDir)
	if got != nil {
		t.Errorf("ListVanity() on empty dir = %v, want nil", got)
	}
}
