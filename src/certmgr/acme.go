package certmgr

import (
	"crypto/tls"
	"net/http"
	"path/filepath"

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
// Deviation from a literal reading of AI.md PART 15: autocert manages its
// own on-disk cache format (an opaque DirCache), not the
// {fullchain,privkey}.pem layout PART 15's "Certificate Directory
// Structure" specifies. A freshly-issued certificate is therefore cached
// under AutocertCacheDir, not written into
// {config_dir}/ssl/letsencrypt/{fqdn}/, until copied there by
// SaveAppManagedCertificate — this bridging is not yet wired up
// automatically (tracked in TODO.AI.md). DNS-01 issuance (needed for
// wildcard certs and the full provider matrix in "DNS-01 Provider
// Configuration") is not implemented.
func NewTLSConfig(configDir, fqdn, email string) (*tls.Config, *autocert.Manager) {
	mgr := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(fqdn),
		Cache:      autocert.DirCache(AutocertCacheDir(configDir)),
		Email:      email,
	}

	tlsCfg := mgr.TLSConfig()
	baseGetCert := tlsCfg.GetCertificate
	tlsCfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if found, err := FindCertificate(configDir, fqdn); err == nil && found != nil && !NeedsRenewal(found.NotAfter) {
			if cert, cerr := tls.LoadX509KeyPair(found.CertPath, found.KeyPath); cerr == nil {
				return &cert, nil
			}
		}
		return baseGetCert(hello)
	}
	return tlsCfg, mgr
}

// HTTPHandler wraps fallback with the autocert HTTP-01 challenge handler.
// It must be mounted on plain HTTP port 80 for HTTP-01 issuance to
// succeed; requests that are not part of an ACME challenge are passed
// through to fallback unchanged.
func HTTPHandler(mgr *autocert.Manager, fallback http.Handler) http.Handler {
	return mgr.HTTPHandler(fallback)
}
