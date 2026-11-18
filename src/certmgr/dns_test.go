package certmgr

import (
	"os"
	"strings"
	"testing"
)

func TestDNSConfigFingerprintTracksProviderAndCredentials(t *testing.T) {
	credentials := map[string]string{"CF_DNS_API_TOKEN": "value"}
	first := DNSConfigFingerprint("cloudflare", credentials, "stable-secret")
	if first == DNSConfigFingerprint("route53", credentials, "stable-secret") {
		t.Fatal("fingerprint did not change when provider changed")
	}
	credentials["CF_DNS_API_TOKEN"] = "changed"
	if first == DNSConfigFingerprint("cloudflare", credentials, "stable-secret") {
		t.Fatal("fingerprint did not change when credentials changed")
	}
	if first == DNSConfigFingerprint("cloudflare", map[string]string{"CF_DNS_API_TOKEN": "value"}, "different-secret") {
		t.Fatal("fingerprint was not keyed")
	}
}

func TestNewDNSChallengeProviderRestoresEnvironment(t *testing.T) {
	const name = "APIMGR_DNS_TEST_SECRET"
	t.Setenv(name, "outside-value")
	provider, err := NewDNSChallengeProvider("cloudflare", map[string]string{"CF_DNS_API_TOKEN": "credential-value"})
	if err != nil {
		t.Fatalf("NewDNSChallengeProvider() error = %v", err)
	}
	if provider == nil {
		t.Fatal("NewDNSChallengeProvider() returned nil provider")
	}
	if got := os.Getenv(name); got != "outside-value" {
		t.Fatalf("environment value after provider construction = %q", got)
	}
	if got := os.Getenv("CF_DNS_API_TOKEN"); got != "" {
		t.Fatalf("DNS credential remained in process environment: %q", got)
	}
}

func TestNewDNSChallengeProviderDoesNotExposeInvalidCredentialValues(t *testing.T) {
	_, err := NewDNSChallengeProvider("not-a-real-provider", map[string]string{"CF_DNS_API_TOKEN": "never-log-this-value"})
	if err == nil {
		t.Fatal("unknown provider succeeded")
	}
	if strings.Contains(err.Error(), "never-log-this-value") {
		t.Fatalf("provider error exposed a credential value: %v", err)
	}
}
