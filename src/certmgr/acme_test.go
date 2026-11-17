package certmgr

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenewedCertificateExpiry(t *testing.T) {
	oldExpiry := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		previous *Found
		cert     *tls.Certificate
		want     bool
	}{
		{name: "initial certificate", cert: &tls.Certificate{Leaf: &x509.Certificate{NotAfter: oldExpiry}}, want: false},
		{name: "new expiry", previous: &Found{NotAfter: oldExpiry}, cert: &tls.Certificate{Leaf: &x509.Certificate{NotAfter: oldExpiry.Add(90 * 24 * time.Hour)}}, want: true},
		{name: "unchanged expiry", previous: &Found{NotAfter: oldExpiry}, cert: &tls.Certificate{Leaf: &x509.Certificate{NotAfter: oldExpiry}}, want: false},
		{name: "shorter expiry", previous: &Found{NotAfter: oldExpiry}, cert: &tls.Certificate{Leaf: &x509.Certificate{NotAfter: oldExpiry.Add(-24 * time.Hour)}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotExpiry, got := renewedCertificateExpiry(test.previous, test.cert)
			if got != test.want {
				t.Fatalf("renewedCertificateExpiry() = (%v, %v), want renewed=%v", gotExpiry, got, test.want)
			}
			if got && !gotExpiry.Equal(test.cert.Leaf.NotAfter) {
				t.Errorf("expiry = %s, want %s", gotExpiry, test.cert.Leaf.NotAfter)
			}
		})
	}
}

func TestSaveAutocertCertificateMirrorsAppManagedPair(t *testing.T) {
	configDir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t, "example.com", time.Now().Add(90*24*time.Hour))
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("load generated certificate: %v", err)
	}

	if err := SaveAutocertCertificate(configDir, "example.com", &pair); err != nil {
		t.Fatalf("SaveAutocertCertificate: %v", err)
	}

	certPath := filepath.Join(AppManagedDir(configDir, "example.com"), "fullchain.pem")
	keyPath := filepath.Join(AppManagedDir(configDir, "example.com"), "privkey.pem")
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("mirrored certificate pair is unusable: %v", err)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat mirrored private key: %v", err)
	}
	if got := keyInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("private key mode = %04o, want 0600", got)
	}
	found, err := FindCertificate(configDir, "example.com")
	if err != nil || found == nil || found.Tier != TierAppManaged {
		t.Fatalf("FindCertificate after mirror = (%+v, %v), want app-managed certificate", found, err)
	}
}

func TestSaveAutocertCertificateRejectsIncompleteCertificate(t *testing.T) {
	for name, cert := range map[string]*tls.Certificate{
		"nil":      nil,
		"no chain": {PrivateKey: struct{}{}},
		"no key":   {Certificate: [][]byte{{1}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := SaveAutocertCertificate(t.TempDir(), "example.com", cert); err == nil {
				t.Fatal("expected incomplete certificate error")
			}
		})
	}
}
