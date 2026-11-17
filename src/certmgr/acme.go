package certmgr

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// AutocertCacheDir is where autocert stores its own opaque on-disk cache
// (account keys, ACME state, and issued certificates in autocert's own
// format), separate from the certbot-mirroring layout in AppManagedDir.
func AutocertCacheDir(configDir string) string {
	return filepath.Join(configDir, "ssl", "letsencrypt", "autocert-cache")
}

// NewTLSConfig returns a *tls.Config for fqdn that serves the best
// certificate found by FindCertificate (system certbot, app-managed, or
// user-local — AI.md PART 15 "Certificate Lookup Order") when one exists
// and is not due for renewal, and otherwise falls back to ACME issuance
// (HTTP-01/TLS-ALPN-01) via golang.org/x/crypto/acme/autocert.
//
// autocert keeps its own opaque account/cache data, while every certificate
// it returns is also bridged into the app-managed certbot-mirroring layout
// required by AI.md PART 15 "Certificate Directory Structure". DNS-01
// issuance (needed for wildcard certs and the full provider matrix in
// "DNS-01 Provider Configuration") is not implemented.
func NewTLSConfig(configDir, fqdn, email string, onRenewed func(time.Time)) (*tls.Config, *autocert.Manager) {
	mgr := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(fqdn),
		Cache:      autocert.DirCache(AutocertCacheDir(configDir)),
		Email:      email,
	}

	tlsCfg := mgr.TLSConfig()
	baseGetCert := tlsCfg.GetCertificate
	var renewalMu sync.Mutex
	var lastNotified time.Time
	tlsCfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if found, err := FindCertificate(configDir, fqdn); err == nil && found != nil && !NeedsRenewal(found.NotAfter) {
			if cert, cerr := tls.LoadX509KeyPair(found.CertPath, found.KeyPath); cerr == nil {
				return &cert, nil
			}
		}
		renewalMu.Lock()
		defer renewalMu.Unlock()
		previous, _ := FindCertificate(configDir, fqdn)
		cert, err := baseGetCert(hello)
		if err != nil {
			return nil, err
		}
		if err := SaveAutocertCertificate(configDir, fqdn, cert); err != nil {
			return nil, fmt.Errorf("certmgr: persist ACME certificate: %w", err)
		}
		if notAfter, renewed := renewedCertificateExpiry(previous, cert); renewed && !notAfter.Equal(lastNotified) {
			lastNotified = notAfter
			if onRenewed != nil {
				go onRenewed(notAfter)
			}
		}
		return cert, nil
	}
	return tlsCfg, mgr
}

func renewedCertificateExpiry(previous *Found, cert *tls.Certificate) (time.Time, bool) {
	if previous == nil || cert == nil {
		return time.Time{}, false
	}
	leaf := cert.Leaf
	if leaf == nil && len(cert.Certificate) > 0 {
		parsed, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			return time.Time{}, false
		}
		leaf = parsed
	}
	if leaf == nil || !leaf.NotAfter.After(previous.NotAfter) {
		return time.Time{}, false
	}
	return leaf.NotAfter.UTC(), true
}

// SaveAutocertCertificate serializes an autocert certificate and its private
// key into the app-managed layout. The autocert cache remains in place for
// account and ACME state; the mirrored pair is used by certificate lookup
// and the PART 18 renewal task.
func SaveAutocertCertificate(configDir, fqdn string, cert *tls.Certificate) error {
	if cert == nil || len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		return fmt.Errorf("certmgr: ACME returned an incomplete certificate")
	}
	var certPEM []byte
	for _, der := range cert.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return fmt.Errorf("certmgr: marshal ACME private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return SaveAppManagedCertificate(configDir, fqdn, certPEM, keyPEM)
}

// HTTPHandler wraps fallback with the autocert HTTP-01 challenge handler.
// It must be mounted on plain HTTP port 80 for HTTP-01 issuance to
// succeed; requests that are not part of an ACME challenge are passed
// through to fallback unchanged.
func HTTPHandler(mgr *autocert.Manager, fallback http.Handler) http.Handler {
	return mgr.HTTPHandler(fallback)
}
