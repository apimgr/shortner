// Package ssl builds the server's TLS configuration and ACME challenge handlers.
package ssl

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/certmgr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/fqdn"
)

// Options contains the inputs required to configure TLS for a server.
type Options struct {
	Config       *config.Config
	ConfigDir    string
	InternalName string
	Database     db.Handle
	OnRenewed    func(host string, notAfter time.Time)
}

// BuildTLSConfig returns the TLS config and HTTP-01 challenge wrapper for the
// configured listener. A manual certificate does not enable TLS by itself;
// TLS starts only when server.ssl.enabled is true or port 443 is configured.
func BuildTLSConfig(opts Options) (*tls.Config, func(http.Handler) http.Handler, error) {
	cfg := opts.Config
	if cfg == nil {
		return nil, nil, fmt.Errorf("tls: configuration is required")
	}
	if !cfg.Server.TLS.Enabled && !portIncludes(cfg.Server.Port, 443) {
		return nil, nil, nil
	}
	if (cfg.Server.TLS.Cert == "") != (cfg.Server.TLS.Key == "") {
		return nil, nil, fmt.Errorf("tls: server.ssl.cert and server.ssl.key must be configured together")
	}
	minVersion := uint16(tls.VersionTLS12)
	switch strings.TrimSpace(cfg.Server.TLS.MinVersion) {
	case "", "TLS1.2":
	case "TLS1.3":
		minVersion = tls.VersionTLS13
	default:
		return nil, nil, fmt.Errorf("tls: unsupported server.ssl.min_version %q", cfg.Server.TLS.MinVersion)
	}
	if cfg.Server.TLS.Cert != "" {
		certPath, err := resolveConfigPath(opts.ConfigDir, cfg.Server.TLS.Cert)
		if err != nil {
			return nil, nil, fmt.Errorf("tls: resolve server.ssl.cert: %w", err)
		}
		keyPath, err := resolveConfigPath(opts.ConfigDir, cfg.Server.TLS.Key)
		if err != nil {
			return nil, nil, fmt.Errorf("tls: resolve server.ssl.key: %w", err)
		}
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("tls: load configured certificate: %w", err)
		}
		return &tls.Config{MinVersion: minVersion, Certificates: []tls.Certificate{certificate}}, nil, nil
	}

	host := fqdn.GetFQDN(opts.InternalName)
	if fqdn.IsDevTLD(host, opts.InternalName) {
		return nil, nil, fmt.Errorf("tls: %q is a dev-only TLD, skipping certificate issuance", host)
	}
	if cfg.Server.TLS.LetsEncrypt.DNSProvider != "" {
		credentials, err := config.DNSCredentialValues(cfg)
		if err != nil {
			return dnsTLSFailureConfig(err), nil, err
		}
		tlsConfig, err := certmgr.NewDNSTLSConfig(opts.ConfigDir, host, cfg.Server.TLS.LetsEncrypt.DNSProvider, credentials, func(notAfter time.Time, renewed bool) {
			stamp := time.Now().UTC().Format(time.RFC3339)
			if opts.Database != nil {
				if err := db.SetSecret(context.Background(), opts.Database, db.SecretDNSValidatedAt, stamp); err != nil {
					fmt.Fprintln(os.Stderr, "shortner: ERROR: update DNS validation timestamp")
				}
			}
			if renewed && opts.OnRenewed != nil {
				opts.OnRenewed(host, notAfter)
			}
		})
		if err != nil {
			return dnsTLSFailureConfig(err), nil, err
		}
		tlsConfig.MinVersion = minVersion
		return tlsConfig, nil, nil
	}

	tlsConfig, manager := certmgr.NewTLSConfig(opts.ConfigDir, host, "", func(notAfter time.Time) {
		if opts.OnRenewed != nil {
			opts.OnRenewed(host, notAfter)
		}
	})
	challengeHandler := func(fallback http.Handler) http.Handler {
		return certmgr.HTTPHandler(manager, fallback)
	}
	tlsConfig.MinVersion = minVersion
	return tlsConfig, challengeHandler, nil
}

func resolveConfigPath(configDir, value string) (string, error) {
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	return filepath.Abs(filepath.Join(configDir, value))
}

func dnsTLSFailureConfig(cause error) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, cause
		},
	}
}

func portIncludes(setting string, wanted int) bool {
	for _, part := range strings.Split(setting, ",") {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && port == wanted {
			return true
		}
	}
	return false
}
