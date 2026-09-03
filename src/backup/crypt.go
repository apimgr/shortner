package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/apimgr/shortner/src/security"
)

// EncryptedSuffix is appended to a backup filename when the archive is
// encrypted, per AI.md PART 21 "Backup Format".
const EncryptedSuffix = ".enc"

// EncryptionMethod is the manifest's `encryption_method` value, per AI.md
// PART 21 "Backup Encryption" -> "Algorithm: AES-256-GCM".
const EncryptionMethod = "AES-256-GCM"

// encMagic prefixes every encrypted archive so a wrong-format file is
// rejected with a clear error instead of a meaningless GCM auth failure.
const encMagic = "SHRTBAK1"

// ErrInvalidPassword is returned when an encrypted archive cannot be
// authenticated with the supplied password, per AI.md PART 21 "Restore
// Verification" -> "Decrypt test ... Error: invalid password".
var ErrInvalidPassword = errors.New("backup: invalid backup password")

// ErrNotEncrypted is returned when Decrypt is handed data that is not an
// archive produced by Encrypt.
var ErrNotEncrypted = errors.New("backup: not an encrypted backup archive")

// ErrPasswordRequired is returned when an operation needs the backup
// password and none was supplied.
var ErrPasswordRequired = errors.New("backup: encrypted backup requires a password")

// Encrypt seals plaintext with AES-256-GCM under a key derived from
// password via Argon2id, per AI.md PART 21 "How Encryption Works" steps
// 2-4. The random salt and nonce are stored in the clear ahead of the
// ciphertext — both are public inputs, and the salt must survive so the
// key can be re-derived at restore time.
func Encrypt(plaintext []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrPasswordRequired
	}

	salt := make([]byte, security.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("backup: generate salt: %w", err)
	}

	gcm, err := newGCM(password, salt)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("backup: generate nonce: %w", err)
	}

	out := make([]byte, 0, len(encMagic)+len(salt)+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, encMagic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	// The magic+salt+nonce header is authenticated as additional data so a
	// tampered header fails the same way tampered ciphertext does.
	out = gcm.Seal(out, nonce, plaintext, out[:len(encMagic)+len(salt)+len(nonce)])
	return out, nil
}

// Decrypt reverses Encrypt. A password that does not authenticate returns
// ErrInvalidPassword — GCM cannot distinguish a wrong key from tampered
// data, and both mean the same thing to the operator: this backup cannot
// be restored with what was supplied.
func Decrypt(sealed []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrPasswordRequired
	}

	headerLen := len(encMagic) + security.SaltLen
	if len(sealed) < headerLen || string(sealed[:len(encMagic)]) != encMagic {
		return nil, ErrNotEncrypted
	}
	salt := sealed[len(encMagic):headerLen]

	gcm, err := newGCM(password, salt)
	if err != nil {
		return nil, err
	}

	nonceEnd := headerLen + gcm.NonceSize()
	if len(sealed) < nonceEnd+gcm.Overhead() {
		return nil, ErrNotEncrypted
	}
	nonce := sealed[headerLen:nonceEnd]

	plaintext, err := gcm.Open(nil, nonce, sealed[nonceEnd:], sealed[:nonceEnd])
	if err != nil {
		return nil, ErrInvalidPassword
	}
	return plaintext, nil
}

// newGCM derives the AES-256 key for password/salt and wraps it in GCM.
func newGCM(password string, salt []byte) (cipher.AEAD, error) {
	key := security.DeriveKey(password, salt)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: new gcm: %w", err)
	}
	return gcm, nil
}

// IsEncryptedName reports whether a backup filename denotes an encrypted
// archive, per AI.md PART 21 "File Extension: .tar.gz (unencrypted) or
// .tar.gz.enc (encrypted)".
func IsEncryptedName(name string) bool {
	return strings.HasSuffix(name, EncryptedSuffix)
}

// readAllLimited reads r fully, refusing anything past max bytes so a
// corrupt or hostile archive cannot exhaust memory during verification.
func readAllLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("backup: archive exceeds %d byte limit", max)
	}
	return data, nil
}
