package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. Chosen per OWASP's current minimum recommendation
// for Argon2id (m=19MiB is the legacy minimum; this project uses a higher
// memory cost since it only hashes rare, low-volume secrets — config and
// backup passwords, never per-request auth). See AI.md PART 11 "Data
// Protection": "Config passwords (backup, etc.) ... Argon2id."
const (
	argon2Time    = 3
	argon2MemoryK = 64 * 1024 // 64 MiB, in KiB
	argon2Threads = 2
	argon2KeyLen  = 32
	argon2SaltLen = 16
)

// HashPassword returns an Argon2id hash of password in the encoded form
// "$argon2id$v=19$m=<mem>,t=<time>,p=<threads>$<salt>$<hash>", suitable
// for storage and later verification with VerifyPassword. Never use this
// for API tokens — those are SHA-256 (see HashToken); Argon2id is
// reserved for password-like secrets per AI.md PART 11.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("security: generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2MemoryK, argon2Threads, argon2KeyLen)

	encoded := fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2MemoryK, argon2Time, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash))
	return encoded, nil
}

// VerifyPassword reports whether password matches the Argon2id-encoded
// hash produced by HashPassword. Comparison is constant-time.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("security: invalid argon2id encoding")
	}

	var memoryK uint32
	var timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memoryK, &timeCost, &threads); err != nil {
		return false, fmt.Errorf("security: parse argon2id params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("security: decode salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("security: decode hash: %w", err)
	}

	got := argon2.IDKey([]byte(password), salt, timeCost, memoryK, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
