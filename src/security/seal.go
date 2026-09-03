package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrSealKeySize is returned when the supplied at-rest key is not the
// 32 bytes AES-256-GCM requires.
var ErrSealKeySize = errors.New("security: at-rest key must be 32 bytes")

// Seal encrypts plaintext with AES-256-GCM under key and returns a
// base64 (standard encoding) blob of nonce||ciphertext. This is the
// at-rest encryption AI.md PART 11 "Security Reports" -> "Submission
// Flow" step 3 mandates when no PGP keypair exists: plaintext is never
// written to disk.
func Seal(key, plaintext []byte) (string, error) {
	gcm, err := sealAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("security: nonce: %w", err)
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, plaintext, nil)), nil
}

// Open reverses Seal. It returns an error when the key is wrong, the
// blob is malformed, or the ciphertext has been tampered with.
func Open(key []byte, blob string) ([]byte, error) {
	gcm, err := sealAEAD(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, fmt.Errorf("security: decode sealed blob: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("security: sealed blob truncated")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("security: open sealed blob: %w", err)
	}
	return out, nil
}

// sealAEAD builds the AES-256-GCM AEAD shared by Seal and Open.
func sealAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrSealKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("security: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("security: gcm: %w", err)
	}
	return gcm, nil
}
