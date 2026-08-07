// Package certmgr implements the AI.md PART 15 "Certificate Lookup Order",
// "Certificate Directory Structure", "Certificate Management Ownership",
// and "Renewal Rules" sections: given a config directory and FQDN, find the
// best existing certificate (system certbot, app-managed, or user-local),
// validate it, and decide whether an app-managed certificate is due for
// renewal.
//
// ACME issuance itself (obtaining a NEW certificate when none is found) is
// in acme.go, built on golang.org/x/crypto/acme/autocert. The DNS-01
// provider matrix (AI.md PART 15 "DNS-01 Provider Configuration") is not
// implemented — see TODO.AI.md.
package certmgr

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RenewalWindow is how long before expiry an app-managed certificate is
// considered due for renewal, per AI.md PART 15 "Renewal Rules":
// "{config_dir}/ssl/letsencrypt/{fqdn}/ | Daily (03:00) | 7 days before
// expiry".
const RenewalWindow = 7 * 24 * time.Hour

// Tier identifies which of the four AI.md PART 15 "Certificate Lookup
// Order" locations a certificate was found in.
type Tier string

// The four lookup-order tiers, in priority order (see LookupOrder).
const (
	TierSystemDomain Tier = "system-domain" // /etc/letsencrypt/live/domain/
	TierSystemFQDN   Tier = "system-fqdn"   // /etc/letsencrypt/live/{fqdn}/
	TierAppManaged   Tier = "app-managed"   // {config_dir}/ssl/letsencrypt/{fqdn}/
	TierLocal        Tier = "local"         // {config_dir}/ssl/local/{fqdn}/
)

// ManagesRenewal reports whether the app auto-renews a certificate found at
// this tier, per AI.md PART 15 "Certificate Management Ownership": only
// TierAppManaged is the app's responsibility.
func (t Tier) ManagesRenewal() bool {
	return t == TierAppManaged
}

// candidate is one entry in the certificate lookup order.
type candidate struct {
	Tier     Tier
	CertPath string
	KeyPath  string
}

// LookupOrder returns the four candidate certificate locations for fqdn,
// in the exact priority order from AI.md PART 15 "Certificate Lookup
// Order".
func LookupOrder(configDir, fqdn string) []candidate {
	return []candidate{
		{TierSystemDomain, "/etc/letsencrypt/live/domain/fullchain.pem", "/etc/letsencrypt/live/domain/privkey.pem"},
		{TierSystemFQDN,
			filepath.Join("/etc/letsencrypt/live", fqdn, "fullchain.pem"),
			filepath.Join("/etc/letsencrypt/live", fqdn, "privkey.pem")},
		{TierAppManaged, filepath.Join(AppManagedDir(configDir, fqdn), "fullchain.pem"), filepath.Join(AppManagedDir(configDir, fqdn), "privkey.pem")},
		{TierLocal, filepath.Join(LocalDir(configDir, fqdn), "cert.pem"), filepath.Join(LocalDir(configDir, fqdn), "key.pem")},
	}
}

// AppManagedDir is {config_dir}/ssl/letsencrypt/{fqdn}, per AI.md PART 15
// "Certificate Directory Structure".
func AppManagedDir(configDir, fqdn string) string {
	return filepath.Join(configDir, "ssl", "letsencrypt", fqdn)
}

// LocalDir is {config_dir}/ssl/local/{fqdn}, per AI.md PART 15
// "Certificate Directory Structure".
func LocalDir(configDir, fqdn string) string {
	return filepath.Join(configDir, "ssl", "local", fqdn)
}

// Found describes a matched, validated certificate.
type Found struct {
	Tier     Tier
	CertPath string
	KeyPath  string
	NotAfter time.Time
}

// FindCertificate walks LookupOrder(configDir, fqdn) and returns the first
// candidate that exists, is readable, is not expired, and whose CN/SAN
// matches fqdn, per AI.md PART 15 "Certificate Validation". Returns
// (nil, nil) — not an error — if nothing matches; the caller decides
// whether to request a new certificate.
func FindCertificate(configDir, fqdn string) (*Found, error) {
	for _, c := range LookupOrder(configDir, fqdn) {
		notAfter, ok := validateCertFiles(c.CertPath, c.KeyPath, fqdn)
		if ok {
			return &Found{Tier: c.Tier, CertPath: c.CertPath, KeyPath: c.KeyPath, NotAfter: notAfter}, nil
		}
	}
	return nil, nil
}

// validateCertFiles reports whether the cert/key pair at these paths is
// readable, unexpired, and matches fqdn, per AI.md PART 15 "Certificate
// Validation": "CN or SAN must match ... must not be expired ... both cert
// and key files must be readable."
func validateCertFiles(certPath, keyPath, fqdn string) (time.Time, bool) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return time.Time{}, false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return time.Time{}, false
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, false
	}
	if time.Now().After(cert.NotAfter) {
		return time.Time{}, false
	}
	if err := cert.VerifyHostname(fqdn); err != nil {
		return time.Time{}, false
	}
	return cert.NotAfter, true
}

// NeedsRenewal reports whether a certificate expiring at notAfter is
// within the RenewalWindow, per AI.md PART 15 "Renewal Rules".
func NeedsRenewal(notAfter time.Time) bool {
	return time.Now().Add(RenewalWindow).After(notAfter)
}

// SaveAppManagedCertificate writes certPEM/keyPEM to
// {config_dir}/ssl/letsencrypt/{fqdn}/{fullchain,privkey}.pem, per AI.md
// PART 15 "Certificate Directory Structure". The key file is written 0600
// (owner-read-only); the certificate is not secret and is written 0644.
func SaveAppManagedCertificate(configDir, fqdn string, certPEM, keyPEM []byte) error {
	dir := AppManagedDir(configDir, fqdn)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("certmgr: create %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fullchain.pem"), certPEM, 0o644); err != nil {
		return fmt.Errorf("certmgr: write fullchain.pem: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "privkey.pem"), keyPEM, 0o600); err != nil {
		return fmt.Errorf("certmgr: write privkey.pem: %w", err)
	}
	return nil
}
