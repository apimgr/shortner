package certmgr

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/providers/dns"
	"github.com/go-acme/lego/v5/registration"
)

var dnsEnvironmentMu sync.Mutex
var dnsCredentialName = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{1,127}\b`)
var dnsEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DNSConfigFingerprint returns a keyed digest used only to detect changes to
// the configured provider or credentials without storing the values in DB.
func DNSConfigFingerprint(provider string, credentials map[string]string, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = fmt.Fprintf(mac, "%d:%s", len(provider), provider)
	names := make([]string, 0, len(credentials))
	for name := range credentials {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = fmt.Fprintf(mac, "%d:%s%d:%s", len(name), name, len(credentials[name]), credentials[name])
	}
	return fmt.Sprintf("%x", mac.Sum(nil))
}

// NewDNSChallengeProvider constructs a lego DNS provider using only the
// supplied environment map and restores the process environment immediately.
func NewDNSChallengeProvider(name string, credentials map[string]string) (challenge.Provider, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("certmgr: DNS provider name is required")
	}
	for key := range credentials {
		if !dnsEnvironmentName.MatchString(key) {
			return nil, fmt.Errorf("certmgr: invalid DNS credential variable name %q", key)
		}
	}

	dnsEnvironmentMu.Lock()
	defer dnsEnvironmentMu.Unlock()

	original := os.Environ()
	os.Clearenv()
	defer func() {
		os.Clearenv()
		for _, entry := range original {
			key, value, found := strings.Cut(entry, "=")
			if found {
				_ = os.Setenv(key, value)
			}
		}
	}()
	for key, value := range credentials {
		if err := os.Setenv(key, value); err != nil {
			return nil, fmt.Errorf("certmgr: unable to set DNS credential variable %s", key)
		}
	}
	provider, err := dns.NewDNSChallengeProviderByName(name)
	if err != nil {
		if match := dnsCredentialName.FindString(err.Error()); match != "" {
			return nil, fmt.Errorf("certmgr: DNS provider %s rejected credential variable %s", name, match)
		}
		return nil, fmt.Errorf("certmgr: unable to construct DNS provider %s", name)
	}
	return provider, nil
}

// NewDNSTLSConfig returns a TLS config that issues and renews certificates
// using lego's DNS-01 solver. Provider validation occurs at construction and
// again immediately before each certificate request.
func NewDNSTLSConfig(configDir, fqdn, providerName string, credentials map[string]string, onIssued func(time.Time, bool)) (*tls.Config, error) {
	if _, err := NewDNSChallengeProvider(providerName, credentials); err != nil {
		return nil, err
	}
	var issueMu sync.Mutex
	var lastNotified time.Time
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if found, err := FindCertificate(configDir, fqdn); err == nil && found != nil && !NeedsRenewal(found.NotAfter) {
				cert, loadErr := tls.LoadX509KeyPair(found.CertPath, found.KeyPath)
				if loadErr == nil {
					return &cert, nil
				}
			}
			issueMu.Lock()
			defer issueMu.Unlock()
			previous, _ := FindCertificate(configDir, fqdn)
			resource, err := obtainDNSCertificate(configDir, fqdn, providerName, credentials)
			if err != nil {
				return nil, err
			}
			if err := SaveAppManagedCertificate(configDir, fqdn, resource.Certificate, resource.PrivateKey); err != nil {
				return nil, fmt.Errorf("certmgr: persist DNS ACME certificate")
			}
			cert, err := tls.X509KeyPair(resource.Certificate, resource.PrivateKey)
			if err != nil {
				return nil, fmt.Errorf("certmgr: parse DNS ACME certificate")
			}
			leaf, err := x509.ParseCertificate(cert.Certificate[0])
			if err == nil {
				cert.Leaf = leaf
				if renewed, ok := renewedCertificateExpiry(previous, &cert); ok && !renewed.Equal(lastNotified) {
					lastNotified = renewed
					if onIssued != nil {
						go onIssued(renewed, true)
					}
				} else if previous == nil && onIssued != nil {
					go onIssued(leaf.NotAfter.UTC(), false)
				}
			}
			return &cert, nil
		},
	}, nil
}

type dnsACMEUser struct {
	email        string
	privateKey   crypto.Signer
	registration *acme.ExtendedAccount
}

func (u *dnsACMEUser) GetEmail() string { return u.email }

func (u *dnsACMEUser) GetRegistration() *acme.ExtendedAccount { return u.registration }

func (u *dnsACMEUser) GetPrivateKey() crypto.Signer { return u.privateKey }

type dnsACMEAccount struct {
	Registration *acme.ExtendedAccount `json:"registration"`
	PrivateKey   []byte                `json:"private_key"`
}

func obtainDNSCertificate(configDir, fqdn, providerName string, credentials map[string]string) (*certificate.Resource, error) {
	provider, err := NewDNSChallengeProvider(providerName, credentials)
	if err != nil {
		return nil, err
	}
	accountPath := filepath.Join(AutocertCacheDir(configDir), "dns-account.json")
	if err := os.MkdirAll(filepath.Dir(accountPath), 0o700); err != nil {
		return nil, fmt.Errorf("certmgr: create private ACME account directory")
	}
	_ = os.Chmod(filepath.Dir(accountPath), 0o700)
	user, err := loadDNSACMEUser(accountPath)
	if err != nil {
		return nil, err
	}
	client, err := lego.NewClient(lego.NewConfig(user))
	if err != nil {
		return nil, fmt.Errorf("certmgr: initialize DNS ACME client")
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return nil, fmt.Errorf("certmgr: configure DNS ACME solver")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if user.registration == nil {
		account, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, sanitizedDNSError(providerName, err)
		}
		user.registration = account
		if err := saveDNSACMEUser(accountPath, user); err != nil {
			return nil, err
		}
	}
	resource, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{
		Domains: []string{fqdn},
		Bundle:  true,
		KeyType: certcrypto.EC256,
	})
	if err != nil {
		return nil, sanitizedDNSError(providerName, err)
	}
	return resource, nil
}

func loadDNSACMEUser(path string) (*dnsACMEUser, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if keyErr != nil {
			return nil, fmt.Errorf("certmgr: generate DNS ACME account key")
		}
		return &dnsACMEUser{privateKey: key}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("certmgr: read DNS ACME account")
	}
	var account dnsACMEAccount
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, fmt.Errorf("certmgr: parse DNS ACME account")
	}
	block, _ := pem.Decode(account.PrivateKey)
	if block == nil {
		return nil, fmt.Errorf("certmgr: decode DNS ACME account key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certmgr: parse DNS ACME account key")
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("certmgr: unsupported DNS ACME account key")
	}
	return &dnsACMEUser{privateKey: signer, registration: account.Registration}, nil
}

func saveDNSACMEUser(path string, user *dnsACMEUser) error {
	keyDER, err := x509.MarshalPKCS8PrivateKey(user.privateKey)
	if err != nil {
		return fmt.Errorf("certmgr: encode DNS ACME account key")
	}
	data, err := json.Marshal(dnsACMEAccount{
		Registration: user.registration,
		PrivateKey:   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	})
	if err != nil {
		return fmt.Errorf("certmgr: encode DNS ACME account")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "dns-account-*.tmp")
	if err != nil {
		return fmt.Errorf("certmgr: create DNS ACME account file")
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("certmgr: secure DNS ACME account file")
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("certmgr: write DNS ACME account file")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("certmgr: sync DNS ACME account file")
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("certmgr: close DNS ACME account file")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("certmgr: replace DNS ACME account file")
	}
	return nil
}

func sanitizedDNSError(provider string, err error) error {
	if match := dnsCredentialName.FindString(err.Error()); match != "" {
		return fmt.Errorf("certmgr: DNS provider %s failed for credential variable %s", provider, match)
	}
	return fmt.Errorf("certmgr: DNS provider %s certificate request failed", provider)
}
