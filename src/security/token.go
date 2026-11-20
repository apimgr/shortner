// Package security implements the cryptographic and identity primitives
// required by AI.md PART 11 "Security & Logging": the resource-owner /
// operator token model, constant-time comparisons, project-level secret
// generation, click-analytics IP anonymization, and short-code/slug
// validation (IDEA.md "Business rules").
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// tokenAlphabet is the URL-safe base62 alphabet used for generated tokens,
// matching AI.md PART 11's Token Catalogue.
const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// TokenLength is the number of random alphabet characters after the prefix.
const TokenLength = 32

// GenerateToken returns an owner token, per AI.md's Token Catalogue.
func GenerateToken() (string, error) {
	return GenerateTokenWithPrefix("own")
}

// GenerateTokenWithPrefix creates a base62 bearer token for the supplied
// catalogue prefix.
func GenerateTokenWithPrefix(prefix string) (string, error) {
	if prefix != "adm" && prefix != "own" && prefix != "mtr" {
		return "", fmt.Errorf("security: unsupported token prefix")
	}
	body, err := RandomString(tokenAlphabet, TokenLength)
	if err != nil {
		return "", fmt.Errorf("security: generate token: %w", err)
	}
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteByte('_')
	b.WriteString(body)
	return b.String(), nil
}

// RandomString returns n characters drawn uniformly at random from
// alphabet using crypto/rand.
//
// Rejection sampling is required for uniformity: mapping a raw byte with
// `b % len(alphabet)` is only unbiased when len(alphabet) divides 256. For
// the 62-character base62 alphabet it does not (256 = 4*62 + 8), so the
// first 8 characters would be drawn 25% more often than the rest. Bytes
// at or above the largest multiple of len(alphabet) are discarded and
// redrawn instead.
func RandomString(alphabet string, n int) (string, error) {
	size := len(alphabet)
	if size == 0 || size > 256 {
		return "", fmt.Errorf("security: random string: alphabet size %d out of range", size)
	}
	// Largest multiple of size that fits in a byte; values >= limit are
	// rejected so every accepted value maps to exactly one character. When
	// size divides 256 evenly this equals 256 and nothing is ever rejected.
	limit := 256 - (256 % size)

	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, v := range buf {
			if int(v) >= limit {
				continue
			}
			out = append(out, alphabet[int(v)%size])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// HashToken returns the lowercase hex SHA-256 digest of raw, the form
// persisted to the api_tokens table per AI.md PART 11 "API Token Model":
// "The server stores only the SHA-256 hash; the raw token is shown once
// ... and never retrievable again."
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// TokenPrefix returns the first 12 characters of raw, used for log
// identification and DB lookup per AI.md PART 11 "API Tokens" schema
// (token_prefix column).
func TokenPrefix(raw string) string {
	if len(raw) <= 12 {
		return raw
	}
	return raw[:12]
}

// CompareTokenHash reports whether raw hashes to storedHash, using a
// constant-time comparison per AI.md PART 11 "Authentication & Identity
// Rules": "Use crypto/subtle.ConstantTimeCompare for ALL token ...
// comparisons. Never compare with ==, bytes.Equal, or strings.EqualFold."
func CompareTokenHash(raw, storedHash string) bool {
	got := HashToken(raw)
	// ConstantTimeCompare requires equal-length slices; hex-encoded
	// SHA-256 digests are always 64 bytes, but guard defensively so a
	// malformed stored hash never short-circuits into a fast/slow path
	// that leaks length via timing.
	if len(got) != len(storedHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}

// CompareServerToken reports whether raw matches the operator token
// (server.token), by SHA-256-hashing both sides and comparing in constant
// time. Per AI.md PART 11: "Validated by SHA-256-hashing the inbound
// token and comparing against SHA-256(server.token) with
// subtle.ConstantTimeCompare."
func CompareServerToken(raw, serverToken string) bool {
	if serverToken == "" {
		return false
	}
	return CompareTokenHash(raw, HashToken(serverToken))
}

// GenerateSecret returns n cryptographically random bytes, for use as a
// project-level secret (installation_secret, cookie_signing_key,
// csrf_token_secret — AI.md PART 11 "Cryptographic Keys").
func GenerateSecret(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("security: generate secret: %w", err)
	}
	return buf, nil
}
