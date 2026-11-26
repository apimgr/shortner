package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const dnsCredentialPrefix = "enc:v1:"

// SealDNSCredentials encrypts plaintext DNS values in-place and validates
// already encrypted entries. The caller persists cfg atomically when changed.
func SealDNSCredentials(cfg *Config) (bool, error) {
	credentials := cfg.Server.TLS.LetsEncrypt.DNSCredentials
	if len(credentials) == 0 {
		return false, nil
	}
	key, err := DecodeEncryptionKey(cfg)
	if err != nil {
		return false, fmt.Errorf("config: DNS credentials require a valid server.security.encryption_key")
	}
	changed := false
	for name, value := range credentials {
		if strings.HasPrefix(value, dnsCredentialPrefix) {
			if _, err := decryptDNSCredential(key, value); err != nil {
				return false, fmt.Errorf("config: invalid encrypted DNS credential %s", name)
			}
			continue
		}
		sealed, err := encryptDNSCredential(key, value)
		if err != nil {
			return false, fmt.Errorf("config: encrypt DNS credential %s: %w", name, err)
		}
		credentials[name] = sealed
		changed = true
	}
	return changed, nil
}

// DNSCredentialValues decrypts the flat lego environment map for the
// immediate provider-construction call. Values are never logged or serialized.
func DNSCredentialValues(cfg *Config) (map[string]string, error) {
	credentials := cfg.Server.TLS.LetsEncrypt.DNSCredentials
	if len(credentials) == 0 {
		return map[string]string{}, nil
	}
	key, err := DecodeEncryptionKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("config: DNS credentials require a valid server.security.encryption_key")
	}
	values := make(map[string]string, len(credentials))
	for name, value := range credentials {
		if strings.HasPrefix(value, dnsCredentialPrefix) {
			value, err = decryptDNSCredential(key, value)
			if err != nil {
				return nil, fmt.Errorf("config: decrypt DNS credential %s", name)
			}
		}
		values[name] = value
	}
	return values, nil
}

// ReencryptDNSCredentials migrates stored DNS credentials during key
// rotation without exposing values beyond this in-memory operation.
func ReencryptDNSCredentials(cfg *Config, oldKey, newKey []byte) error {
	for name, sealed := range cfg.Server.TLS.LetsEncrypt.DNSCredentials {
		if !strings.HasPrefix(sealed, dnsCredentialPrefix) {
			return fmt.Errorf("config: plaintext DNS credential %s cannot be rotated safely", name)
		}
		plain, err := decryptDNSCredential(oldKey, sealed)
		if err != nil {
			return fmt.Errorf("config: decrypt DNS credential %s for key rotation", name)
		}
		rotated, err := encryptDNSCredential(newKey, plain)
		if err != nil {
			return fmt.Errorf("config: re-encrypt DNS credential %s", name)
		}
		cfg.Server.TLS.LetsEncrypt.DNSCredentials[name] = rotated
	}
	return nil
}

func encryptDNSCredential(key []byte, value string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	payload := aead.Seal(nonce, nonce, []byte(value), nil)
	return dnsCredentialPrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func decryptDNSCredential(key []byte, value string) (string, error) {
	encoded := strings.TrimPrefix(value, dnsCredentialPrefix)
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < aead.NonceSize() {
		return "", fmt.Errorf("encrypted value is too short")
	}
	nonce, ciphertext := payload[:aead.NonceSize()], payload[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
