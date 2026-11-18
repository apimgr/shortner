package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDNSCredentialsSealAndRotate(t *testing.T) {
	cfg := Default(filepath.Join(t.TempDir(), "server.db"))
	if _, err := EnsureEncryptionKey(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Server.TLS.DNSCredentials = map[string]string{"CF_DNS_API_TOKEN": "secret-value"}
	changed, err := SealDNSCredentials(cfg)
	if err != nil || !changed {
		t.Fatalf("SealDNSCredentials() = %v, %v; want changed", changed, err)
	}
	sealed := cfg.Server.TLS.DNSCredentials["CF_DNS_API_TOKEN"]
	if !strings.HasPrefix(sealed, dnsCredentialPrefix) || strings.Contains(sealed, "secret-value") {
		t.Fatalf("credential was not encrypted: %q", sealed)
	}
	values, err := DNSCredentialValues(cfg)
	if err != nil || values["CF_DNS_API_TOKEN"] != "secret-value" {
		t.Fatalf("DNSCredentialValues() = %#v, %v", values, err)
	}
	oldKey, err := DecodeEncryptionKey(cfg)
	if err != nil {
		t.Fatal(err)
	}
	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = byte(i + 1)
	}
	if err := ReencryptDNSCredentials(cfg, oldKey, newKey); err != nil {
		t.Fatal(err)
	}
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(newKey)
	values, err = DNSCredentialValues(cfg)
	if err != nil || values["CF_DNS_API_TOKEN"] != "secret-value" {
		t.Fatalf("rotated DNSCredentialValues() = %#v, %v", values, err)
	}
	cfg.Server.TLS.DNSCredentials["CF_DNS_API_TOKEN"] += "tampered"
	if _, err := DNSCredentialValues(cfg); err == nil {
		t.Fatal("tampered ciphertext decrypted without error")
	}
}

func TestLoadAndSavePreserveSecretFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	cfg := Default(filepath.Join(t.TempDir(), "server.db"))
	if _, err := EnsureEncryptionKey(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Server.TLS.DNSCredentials = map[string]string{"DO_AUTH_TOKEN": "secret-value"}
	if _, err := SealDNSCredentials(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-value") {
		t.Fatal("saved config contains plaintext DNS credential")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}
