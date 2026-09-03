package certmgr

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateTestCert returns PEM-encoded cert+key for host, valid from now
// until notAfter.
func generateTestCert(t *testing.T, host string, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func TestLookupOrder(t *testing.T) {
	order := LookupOrder("/config", "example.com")
	if len(order) != 4 {
		t.Fatalf("expected 4 tiers, got %d", len(order))
	}
	want := []Tier{TierSystemDomain, TierSystemFQDN, TierAppManaged, TierLocal}
	for i, c := range order {
		if c.Tier != want[i] {
			t.Errorf("tier[%d] = %v, want %v", i, c.Tier, want[i])
		}
	}
	if order[0].CertPath != "/etc/letsencrypt/live/domain/fullchain.pem" {
		t.Errorf("system-domain cert path = %q", order[0].CertPath)
	}
	if order[1].CertPath != "/etc/letsencrypt/live/example.com/fullchain.pem" {
		t.Errorf("system-fqdn cert path = %q", order[1].CertPath)
	}
	if order[2].CertPath != filepath.Join("/config", "ssl", "letsencrypt", "example.com", "fullchain.pem") {
		t.Errorf("app-managed cert path = %q", order[2].CertPath)
	}
	if order[3].CertPath != filepath.Join("/config", "ssl", "local", "example.com", "cert.pem") {
		t.Errorf("local cert path = %q", order[3].CertPath)
	}
}

func TestManagesRenewal(t *testing.T) {
	if !TierAppManaged.ManagesRenewal() {
		t.Error("app-managed tier should manage renewal")
	}
	for _, tier := range []Tier{TierSystemDomain, TierSystemFQDN, TierLocal} {
		if tier.ManagesRenewal() {
			t.Errorf("%v should not manage renewal", tier)
		}
	}
}

func TestNeedsRenewal(t *testing.T) {
	if !NeedsRenewal(time.Now().Add(24 * time.Hour)) {
		t.Error("expiring in 1 day should need renewal")
	}
	if NeedsRenewal(time.Now().Add(30 * 24 * time.Hour)) {
		t.Error("expiring in 30 days should not need renewal")
	}
	if !NeedsRenewal(time.Now().Add(-time.Hour)) {
		t.Error("already-expired cert should need renewal")
	}
}

func TestSaveAppManagedCertificate(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t, "example.com", time.Now().Add(90*24*time.Hour))

	if err := SaveAppManagedCertificate(dir, "example.com", certPEM, keyPEM); err != nil {
		t.Fatalf("SaveAppManagedCertificate: %v", err)
	}

	certPath := filepath.Join(AppManagedDir(dir, "example.com"), "fullchain.pem")
	keyPath := filepath.Join(AppManagedDir(dir, "example.com"), "privkey.pem")

	if got, err := os.ReadFile(certPath); err != nil || string(got) != string(certPEM) {
		t.Fatalf("fullchain.pem mismatch: err=%v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat privkey.pem: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("privkey.pem perm = %v, want 0600", info.Mode().Perm())
	}
}

func TestFindCertificate_AppManagedValid(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t, "example.com", time.Now().Add(90*24*time.Hour))
	if err := SaveAppManagedCertificate(dir, "example.com", certPEM, keyPEM); err != nil {
		t.Fatalf("save: %v", err)
	}

	found, err := FindCertificate(dir, "example.com")
	if err != nil {
		t.Fatalf("FindCertificate: %v", err)
	}
	if found == nil {
		t.Fatal("expected a match, got nil")
	}
	if found.Tier != TierAppManaged {
		t.Errorf("tier = %v, want app-managed", found.Tier)
	}
}

func TestFindCertificate_ExpiredIsSkipped(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t, "example.com", time.Now().Add(-time.Hour))
	if err := SaveAppManagedCertificate(dir, "example.com", certPEM, keyPEM); err != nil {
		t.Fatalf("save: %v", err)
	}

	found, err := FindCertificate(dir, "example.com")
	if err != nil {
		t.Fatalf("FindCertificate: %v", err)
	}
	if found != nil {
		t.Fatalf("expected no match for expired cert, got %+v", found)
	}
}

func TestFindCertificate_HostnameMismatchIsSkipped(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM := generateTestCert(t, "other.example.com", time.Now().Add(90*24*time.Hour))
	if err := SaveAppManagedCertificate(dir, "example.com", certPEM, keyPEM); err != nil {
		t.Fatalf("save: %v", err)
	}

	found, err := FindCertificate(dir, "example.com")
	if err != nil {
		t.Fatalf("FindCertificate: %v", err)
	}
	if found != nil {
		t.Fatalf("expected no match for hostname mismatch, got %+v", found)
	}
}

func TestFindCertificate_NoneFound(t *testing.T) {
	dir := t.TempDir()
	found, err := FindCertificate(dir, "nowhere.example.com")
	if err != nil {
		t.Fatalf("FindCertificate: %v", err)
	}
	if found != nil {
		t.Fatalf("expected nil, got %+v", found)
	}
}

func TestAppManagedDirAndLocalDir(t *testing.T) {
	if got, want := AppManagedDir("/cfg", "x.com"), filepath.Join("/cfg", "ssl", "letsencrypt", "x.com"); got != want {
		t.Errorf("AppManagedDir = %q, want %q", got, want)
	}
	if got, want := LocalDir("/cfg", "x.com"), filepath.Join("/cfg", "ssl", "local", "x.com"); got != want {
		t.Errorf("LocalDir = %q, want %q", got, want)
	}
}
