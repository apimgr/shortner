package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const EncryptedSuffix = ".enc"
const EncryptionMethod = "AES-256-GCM"

const (
	encMagic       = "BKUP1"
	legacyEncMagic = "SHRTBAK1"
	backupArgonM   = 64 * 1024
	backupArgonT   = 3
	backupArgonP   = 2
	backupKeyBytes = 32
	backupNonceLen = 12
	backupKeyIDLen = 8
	backupBaseMax  = 255
	backupHeaderLen = 55
	maxArgonMemory = 256 * 1024
	maxArgonTime   = 10
	maxArgonThreads = 8
)

var (
	ErrInvalidPassword = errors.New("backup: invalid backup password")
	ErrNotEncrypted = errors.New("backup: not an encrypted backup archive")
	ErrPasswordRequired = errors.New("backup: encrypted backup requires a password")
)

// EncryptionHeader is the public, password-independent metadata at the
// start of an encrypted archive. Its exact bytes are authenticated by GCM.
type EncryptionHeader struct {
	Salt       []byte
	MemoryKiB  uint32
	Iterations uint32
	Parallelism uint32
	Nonce      []byte
	KeyID      string
	BaseName   string
	Legacy     bool
	Length     int
}

// Encrypt writes the PART 21 BKUP1 header followed by AES-256-GCM ciphertext.
// An optional base name is recorded for an incremental archive.
func Encrypt(plaintext []byte, password string, baseNames ...string) ([]byte, error) {
	if password == "" {
		return nil, ErrPasswordRequired
	}
	baseName := ""
	if len(baseNames) > 1 {
		return nil, fmt.Errorf("backup: only one base backup filename is allowed")
	}
	if len(baseNames) > 0 {
		baseName = baseNames[0]
	}
	if !validBaseName(baseName) {
		return nil, fmt.Errorf("backup: invalid base backup filename")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("backup: generate salt: %w", err)
	}
	nonce := make([]byte, backupNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("backup: generate nonce: %w", err)
	}
	key := deriveBackupKey(password, salt, backupArgonM, backupArgonT, backupArgonP)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: new gcm: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("key-id"))
	keyID := hex.EncodeToString(mac.Sum(nil)[:backupKeyIDLen/2])

	header := make([]byte, 0, backupHeaderLen+len(baseName))
	header = append(header, encMagic...)
	header = append(header, salt...)
	for _, value := range []uint32{backupArgonM, backupArgonT, backupArgonP} {
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], value)
		header = append(header, encoded[:]...)
	}
	header = append(header, nonce...)
	header = append(header, keyID...)
	var baseLen [2]byte
	binary.BigEndian.PutUint16(baseLen[:], uint16(len(baseName)))
	header = append(header, baseLen[:]...)
	header = append(header, baseName...)
	return gcm.Seal(header, nonce, plaintext, header), nil
}

// ParseEncryptionHeader validates and returns cleartext archive metadata
// without deriving a key. Legacy SHRTBAK1 archives remain readable.
func ParseEncryptionHeader(sealed []byte) (EncryptionHeader, error) {
	if strings.HasPrefix(string(sealed), legacyEncMagic) {
		length := len(legacyEncMagic) + 16 + backupNonceLen
		if len(sealed) < length {
			return EncryptionHeader{}, ErrNotEncrypted
		}
		start := len(legacyEncMagic)
		return EncryptionHeader{Salt: append([]byte(nil), sealed[start:start+16]...), MemoryKiB: backupArgonM, Iterations: backupArgonT, Parallelism: backupArgonP, Nonce: append([]byte(nil), sealed[start+16:length]...), Legacy: true, Length: length}, nil
	}
	if len(sealed) < backupHeaderLen || string(sealed[:len(encMagic)]) != encMagic {
		return EncryptionHeader{}, ErrNotEncrypted
	}
	position := len(encMagic)
	header := EncryptionHeader{Salt: append([]byte(nil), sealed[position:position+16]...)}
	position += 16
	header.MemoryKiB = binary.BigEndian.Uint32(sealed[position : position+4])
	position += 4
	header.Iterations = binary.BigEndian.Uint32(sealed[position : position+4])
	position += 4
	header.Parallelism = binary.BigEndian.Uint32(sealed[position : position+4])
	position += 4
	if !validArgonParameters(header.MemoryKiB, header.Iterations, header.Parallelism) {
		return EncryptionHeader{}, ErrNotEncrypted
	}
	header.Nonce = append([]byte(nil), sealed[position:position+backupNonceLen]...)
	position += backupNonceLen
	keyID := string(sealed[position : position+backupKeyIDLen])
	if _, err := hex.DecodeString(keyID); err != nil || strings.ToLower(keyID) != keyID {
		return EncryptionHeader{}, ErrNotEncrypted
	}
	header.KeyID = keyID
	position += backupKeyIDLen
	baseLen := int(binary.BigEndian.Uint16(sealed[position : position+2]))
	position += 2
	if baseLen > backupBaseMax || len(sealed) < position+baseLen {
		return EncryptionHeader{}, ErrNotEncrypted
	}
	header.BaseName = string(sealed[position : position+baseLen])
	if !validBaseName(header.BaseName) {
		return EncryptionHeader{}, ErrNotEncrypted
	}
	header.Length = position + baseLen
	return header, nil
}

// ReadEncryptionHeader reads only the public header bytes from an archive.
func ReadEncryptionHeader(filename string) (EncryptionHeader, error) {
	file, err := os.Open(filename)
	if err != nil {
		return EncryptionHeader{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, backupHeaderLen+backupBaseMax))
	if err != nil {
		return EncryptionHeader{}, err
	}
	return ParseEncryptionHeader(data)
}

// KeyIDForPassword derives the identifier for a parsed archive header. It
// supports password-change checks without decrypting the archive.
func KeyIDForPassword(password string, header EncryptionHeader) (string, error) {
	if password == "" {
		return "", ErrPasswordRequired
	}
	if header.Legacy || !validArgonParameters(header.MemoryKiB, header.Iterations, header.Parallelism) {
		return "", ErrNotEncrypted
	}
	key := deriveBackupKey(password, header.Salt, header.MemoryKiB, header.Iterations, header.Parallelism)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("key-id"))
	return hex.EncodeToString(mac.Sum(nil)[:backupKeyIDLen/2]), nil
}

// Decrypt authenticates the complete cleartext header and ciphertext.
func Decrypt(sealed []byte, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrPasswordRequired
	}
	header, err := ParseEncryptionHeader(sealed)
	if err != nil {
		return nil, err
	}
	key := deriveBackupKey(password, header.Salt, header.MemoryKiB, header.Iterations, header.Parallelism)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("backup: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("backup: new gcm: %w", err)
	}
	if len(sealed) < header.Length+gcm.Overhead() {
		return nil, ErrNotEncrypted
	}
	plaintext, err := gcm.Open(nil, header.Nonce, sealed[header.Length:], sealed[:header.Length])
	if err != nil {
		return nil, ErrInvalidPassword
	}
	if !header.Legacy {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte("key-id"))
		want := hex.EncodeToString(mac.Sum(nil)[:backupKeyIDLen/2])
		if !hmac.Equal([]byte(header.KeyID), []byte(want)) {
			return nil, ErrInvalidPassword
		}
	}
	return plaintext, nil
}

func validBaseName(name string) bool {
	if len(name) > backupBaseMax || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	return name == "" || (name != "." && name != ".." && path.Base(name) == name)
}

func validArgonParameters(memory, iterations, parallelism uint32) bool {
	return memory >= 8*parallelism && memory <= maxArgonMemory && iterations > 0 && iterations <= maxArgonTime && parallelism > 0 && parallelism <= maxArgonThreads
}

func deriveBackupKey(password string, salt []byte, memory, iterations, parallelism uint32) []byte {
	return argon2.IDKey([]byte(password), salt, iterations, memory, uint8(parallelism), backupKeyBytes)
}

func IsEncryptedName(name string) bool { return strings.HasSuffix(name, EncryptedSuffix) }

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
