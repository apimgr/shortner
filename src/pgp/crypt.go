package pgp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/apimgr/shortner/src/security"
)

// privMagic prefixes every sealed private key so a wrong-format file is
// rejected with a clear error instead of a meaningless GCM auth failure.
const privMagic = "SHRTPGP1"

// ErrPrivateKeyFormat is returned when the sealed private key file is not
// in the format SealPrivate produces.
var ErrPrivateKeyFormat = errors.New("pgp: encrypted private key is malformed or not produced by this server")

// ErrPrivateKeyUndecryptable is returned when the sealed private key does
// not authenticate under the supplied installation_secret — which, per
// AI.md PART 11 "Cryptographic Keys", means the secret was rotated or lost
// and the key can no longer be recovered.
var ErrPrivateKeyUndecryptable = errors.New("pgp: encrypted private key cannot be decrypted with the current installation_secret")

// ErrNoInstallationSecret is returned when no installation_secret is
// available to derive the private-key encryption key from.
var ErrNoInstallationSecret = errors.New("pgp: installation_secret is required to protect the private key")

// SealPrivate encrypts the ASCII-armored private key with AES-256-GCM
// under a key derived from installationSecret via Argon2id, per AI.md
// PART 11 "Generate": "Private key is encrypted with a key derived from
// installation_secret". The Argon2id parameters are the project's single
// set, reused from src/security — no second KDF is introduced.
//
// The random salt and nonce are stored in the clear ahead of the
// ciphertext (both are public inputs) and are authenticated as additional
// data together with the magic header.
func SealPrivate(armored, installationSecret string) ([]byte, error) {
	if installationSecret == "" {
		return nil, ErrNoInstallationSecret
	}

	salt := make([]byte, security.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("pgp: generate salt: %w", err)
	}

	gcm, err := newPrivateGCM(installationSecret, salt)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("pgp: generate nonce: %w", err)
	}

	header := make([]byte, 0, len(privMagic)+len(salt)+len(nonce))
	header = append(header, privMagic...)
	header = append(header, salt...)
	header = append(header, nonce...)

	return gcm.Seal(header, nonce, []byte(armored), header), nil
}

// OpenPrivate reverses SealPrivate.
func OpenPrivate(sealed []byte, installationSecret string) (string, error) {
	if installationSecret == "" {
		return "", ErrNoInstallationSecret
	}

	headerLen := len(privMagic) + security.SaltLen
	if len(sealed) < headerLen || string(sealed[:len(privMagic)]) != privMagic {
		return "", ErrPrivateKeyFormat
	}
	salt := sealed[len(privMagic):headerLen]

	gcm, err := newPrivateGCM(installationSecret, salt)
	if err != nil {
		return "", err
	}

	nonceEnd := headerLen + gcm.NonceSize()
	if len(sealed) < nonceEnd+gcm.Overhead() {
		return "", ErrPrivateKeyFormat
	}
	nonce := sealed[headerLen:nonceEnd]

	plaintext, err := gcm.Open(nil, nonce, sealed[nonceEnd:], sealed[:nonceEnd])
	if err != nil {
		return "", ErrPrivateKeyUndecryptable
	}
	return string(plaintext), nil
}

// newPrivateGCM derives the AES-256 key for installationSecret/salt and
// wraps it in GCM.
func newPrivateGCM(installationSecret string, salt []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(security.DeriveKey(installationSecret, salt))
	if err != nil {
		return nil, fmt.Errorf("pgp: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("pgp: new gcm: %w", err)
	}
	return gcm, nil
}
