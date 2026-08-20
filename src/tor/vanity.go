package tor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base32"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

// Onion v3 address encoding constants, from rend-spec-v3 §6: the address is
// base32(pubkey || checksum || version) and the checksum is the first two
// bytes of SHA3-256 over a fixed prefix, the key, and the version byte.
const (
	// onionVersion is the v3 address version byte.
	onionVersion = byte(0x03)
	// onionChecksumPrefix is the constant SHA3-256 domain separator.
	onionChecksumPrefix = ".onion checksum"
	// onionSuffix is appended to every encoded address.
	onionSuffix = ".onion"
)

// Key file headers Tor expects under HiddenServiceDir. Each is padded with
// NUL bytes to 32 octets, then followed by the key material.
const (
	secretKeyHeader = "== ed25519v1-secret: type0 =="
	publicKeyHeader = "== ed25519v1-public: type0 =="
)

// onionEncoding is base32 without padding; onion addresses are lowercase.
var onionEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrVanityNotFound is returned when a vanity search ends — by timeout or
// cancellation — without matching the requested prefix.
var ErrVanityNotFound = errors.New("tor: no vanity address found before the search was cancelled")

// OnionAddressFor derives the v3 .onion address for an ed25519 public key.
func OnionAddressFor(pub ed25519.PublicKey) string {
	h := sha3.New256()
	h.Write([]byte(onionChecksumPrefix))
	h.Write(pub)
	h.Write([]byte{onionVersion})
	checksum := h.Sum(nil)

	buf := make([]byte, 0, ed25519.PublicKeySize+3)
	buf = append(buf, pub...)
	buf = append(buf, checksum[0], checksum[1])
	buf = append(buf, onionVersion)
	return strings.ToLower(onionEncoding.EncodeToString(buf)) + onionSuffix
}

// ExpandedSecretKey converts an ed25519 seed into the 64-byte expanded
// secret key Tor stores in hs_ed25519_secret_key: SHA-512 of the seed with
// the standard ed25519 clamping applied to the scalar half.
func ExpandedSecretKey(seed []byte) []byte {
	digest := sha512.Sum512(seed)
	expanded := make([]byte, 64)
	copy(expanded, digest[:])
	expanded[0] &= 248
	expanded[31] &= 127
	expanded[31] |= 64
	return expanded
}

// keyFile renders one of Tor's HiddenServiceDir key files: a 32-byte
// NUL-padded header followed by the key material.
func keyFile(header string, material []byte) []byte {
	out := make([]byte, 32, 32+len(material))
	copy(out, header)
	return append(out, material...)
}

// SecretKeyFile renders hs_ed25519_secret_key for an ed25519 private key.
func SecretKeyFile(priv ed25519.PrivateKey) []byte {
	return keyFile(secretKeyHeader, ExpandedSecretKey(priv.Seed()))
}

// PublicKeyFile renders hs_ed25519_public_key for an ed25519 public key.
func PublicKeyFile(pub ed25519.PublicKey) []byte {
	return keyFile(publicKeyHeader, pub)
}

// ValidateSecretKeyFile checks that data is a well-formed
// hs_ed25519_secret_key: the exact header Tor writes, followed by a 64-byte
// expanded key whose clamping bits are correct. Importing a malformed key
// would leave Tor unable to start with no useful diagnostic, so this runs
// before any import is written to disk.
func ValidateSecretKeyFile(data []byte) error {
	if len(data) != 96 {
		return fmt.Errorf("expected a 96-byte hs_ed25519_secret_key, got %d bytes", len(data))
	}
	if string(data[:len(secretKeyHeader)]) != secretKeyHeader {
		return errors.New("missing the \"== ed25519v1-secret: type0 ==\" header")
	}
	for _, b := range data[len(secretKeyHeader):32] {
		if b != 0 {
			return errors.New("header is not NUL-padded to 32 bytes")
		}
	}
	key := data[32:]
	if key[0]&7 != 0 || key[31]&64 == 0 || key[31]&128 != 0 {
		return errors.New("expanded secret key is not correctly clamped")
	}
	return nil
}

// ValidVanityPrefix reports whether prefix can ever appear at the start of
// a v3 onion address. Base32 has no 0, 1, 8 or 9, so a prefix containing
// one of them would search forever.
func ValidVanityPrefix(prefix string) error {
	if prefix == "" {
		return errors.New("prefix must not be empty")
	}
	if len(prefix) > 16 {
		return errors.New("prefix longer than 16 characters is not searchable in practical time")
	}
	for _, r := range strings.ToLower(prefix) {
		if (r < 'a' || r > 'z') && (r < '2' || r > '7') {
			return fmt.Errorf("character %q cannot appear in an onion address (base32 uses a-z and 2-7 only)", r)
		}
	}
	return nil
}

// VanityResult is one matched vanity identity, held in memory until the
// operator saves it.
type VanityResult struct {
	// Address is the full .onion address that was found.
	Address string
	// Public is the matching ed25519 public key.
	Public ed25519.PublicKey
	// Private is the matching ed25519 private key.
	Private ed25519.PrivateKey
	// Tried is how many candidate keys were generated before the match.
	Tried uint64
}

// SearchVanity generates ed25519 identities until one produces an onion
// address starting with prefix, or until ctx is cancelled. It fans out
// across every CPU because the search is embarrassingly parallel and each
// additional prefix character multiplies the expected work by 32.
func SearchVanity(ctx context.Context, prefix string) (*VanityResult, error) {
	if err := ValidVanityPrefix(prefix); err != nil {
		return nil, err
	}
	want := strings.ToLower(prefix)

	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	found := make(chan *VanityResult, workers)
	var tried atomic.Uint64

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for i := 0; i < workers; i++ {
		go func() {
			for {
				if ctx.Err() != nil {
					return
				}
				pub, priv, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					return
				}
				n := tried.Add(1)
				addr := OnionAddressFor(pub)
				if strings.HasPrefix(addr, want) {
					select {
					case found <- &VanityResult{Address: addr, Public: pub, Private: priv, Tried: n}:
					default:
					}
					return
				}
			}
		}()
	}

	select {
	case <-ctx.Done():
		return nil, ErrVanityNotFound
	case res := <-found:
		return res, nil
	}
}

// VanityDir returns the staging directory a searched identity is saved to.
// Results are staged rather than installed directly so a search can run
// while the server is up and be applied later, deliberately.
func VanityDir(dataDir, address string) string {
	return filepath.Join(dataDir, "tor", "vanity", address)
}

// SaveVanity writes a matched identity to its staging directory in exactly
// the layout Tor expects under HiddenServiceDir, so applying it later is a
// plain file copy.
func SaveVanity(dataDir string, res *VanityResult) (string, error) {
	dir := VanityDir(dataDir, res.Address)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := chownSelf(dir); err != nil {
		return "", err
	}
	files := map[string][]byte{
		"hs_ed25519_secret_key": SecretKeyFile(res.Private),
		"hs_ed25519_public_key": PublicKeyFile(res.Public),
		"hostname":              []byte(res.Address + "\n"),
	}
	for name, content := range files {
		if err := writeSecret(filepath.Join(dir, name), content); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// ListVanity returns the addresses currently staged under the vanity
// directory, oldest search order not guaranteed.
func ListVanity(dataDir string) []string {
	entries, err := os.ReadDir(filepath.Join(dataDir, "tor", "vanity"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
