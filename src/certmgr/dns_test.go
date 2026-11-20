package certmgr

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func startDNSUpdateTestServer(t *testing.T, secret string) (string, <-chan *dns.Msg, <-chan error) {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan *dns.Msg, 8)
	badSignatures := make(chan error, 2)
	var mu sync.Mutex
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		if verifyErr := w.TsigStatus(); verifyErr != nil {
			badSignatures <- verifyErr
			response := new(dns.Msg)
			response.SetReply(req)
			response.Rcode = dns.RcodeNotAuth
			if tsig := req.IsTsig(); tsig != nil {
				response.SetTsig(tsig.Hdr.Name, tsig.Algorithm, 300, time.Now().Unix())
				response.IsTsig().Error = dns.RcodeBadSig
			}
			_ = w.WriteMsg(response)
			return
		}
		if req.Opcode == dns.OpcodeUpdate {
			copyMsg := req.Copy()
			mu.Lock()
			updates <- copyMsg
			mu.Unlock()
		}
		response := new(dns.Msg)
		response.SetReply(req)
		_ = w.WriteMsg(response)
	})
	server := &dns.Server{
		PacketConn: packet,
		Handler:    handler,
		MsgAcceptFunc: func(dns.Header) dns.MsgAcceptAction {
			return dns.MsgAccept
		},
	}
	if secret != "" {
		server.TsigSecret = map[string]string{"test-key.": secret}
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = server.Shutdown()
	})
	return packet.LocalAddr().String(), updates, badSignatures
}

func awaitDNSUpdate(t *testing.T, updates <-chan *dns.Msg) *dns.Msg {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for DNS UPDATE")
		return nil
	}
}

func TestDNSUpdateProviderAddsAndRemovesOneSignedTXTRecord(t *testing.T) {
	const secret = "dGVzdC1zaGFyZWQta2V5"
	for _, spelling := range []string{"dnsupdate", "rfc2136"} {
		t.Run(spelling, func(t *testing.T) {
			address, updates, _ := startDNSUpdateTestServer(t, secret)
			provider, err := NewDNSChallengeProvider(spelling, map[string]string{
				"DNSUPDATE_NAMESERVER":     address,
				"DNSUPDATE_ZONES":          "example.test",
				"DNSUPDATE_TSIG_KEY":       "test-key",
				"DNSUPDATE_TSIG_SECRET":    secret,
				"DNSUPDATE_TSIG_ALGORITHM": "hmac-sha256.",
				"DNSUPDATE_DNS_TIMEOUT":    "2",
			})
			if err != nil {
				t.Fatalf("construct provider: %v", err)
			}
			challenge, ok := provider.(interface {
				Present(context.Context, string, string, string) error
				CleanUp(context.Context, string, string, string) error
			})
			if !ok {
				t.Fatalf("provider %T does not support Present/CleanUp", provider)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := challenge.Present(ctx, "example.test", "token", "key-authorization"); err != nil {
				t.Fatalf("Present() error = %v", err)
			}
			insert := awaitDNSUpdate(t, updates)
			if insert.IsTsig() == nil || len(insert.Ns) != 2 {
				t.Fatalf("signed INSERT update = %#v", insert)
			}
			if rr, ok := insert.Ns[1].(*dns.TXT); !ok || rr.Hdr.Name != "_acme-challenge.example.test." || len(rr.Txt) != 1 {
				t.Fatalf("INSERT did not add exactly the challenge TXT record: %#v", insert.Ns)
			}
			if err := challenge.CleanUp(ctx, "example.test", "token", "key-authorization"); err != nil {
				t.Fatalf("CleanUp() error = %v", err)
			}
			remove := awaitDNSUpdate(t, updates)
			if remove.IsTsig() == nil || len(remove.Ns) != 1 {
				t.Fatalf("signed REMOVE update = %#v", remove)
			}
			if rr, ok := remove.Ns[0].(*dns.TXT); !ok || rr.Hdr.Name != "_acme-challenge.example.test." || rr.Hdr.Class != dns.ClassNONE || len(rr.Txt) != 1 {
				t.Fatalf("REMOVE did not delete exactly the challenge TXT record: %#v", remove.Ns)
			}
		})
	}
}

func TestDNSUpdateProviderRejectsWrongTSIGKey(t *testing.T) {
	address, _, badSignatures := startDNSUpdateTestServer(t, "dHJ1ZS1rZXk")
	provider, err := NewDNSChallengeProvider("dnsupdate", map[string]string{
		"DNSUPDATE_NAMESERVER":     address,
		"DNSUPDATE_ZONES":          "example.test",
		"DNSUPDATE_TSIG_KEY":       "test-key",
		"DNSUPDATE_TSIG_SECRET":    "d3Jvbmcta2V5",
		"DNSUPDATE_TSIG_ALGORITHM": "hmac-sha256.",
		"DNSUPDATE_DNS_TIMEOUT":    "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	challenge := provider.(interface {
		Present(context.Context, string, string, string) error
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = challenge.Present(ctx, "example.test", "token", "key-authorization")
	if err == nil || strings.Contains(err.Error(), "d3Jvbmcta2V5") {
		t.Fatalf("Present() error = %v; want sanitized TSIG failure", err)
	}
	select {
	case signatureErr := <-badSignatures:
		if signatureErr == nil {
			t.Fatal("wrong TSIG secret was accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DNS server did not detect the bad TSIG signature")
	}
}

func TestDNSUpdateProviderAcceptsUnsignedUpdate(t *testing.T) {
	address, updates, _ := startDNSUpdateTestServer(t, "")
	provider, err := NewDNSChallengeProvider("dnsupdate", map[string]string{
		"DNSUPDATE_NAMESERVER":  address,
		"DNSUPDATE_ZONES":       "example.test",
		"DNSUPDATE_DNS_TIMEOUT": "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	challenge := provider.(interface {
		Present(context.Context, string, string, string) error
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := challenge.Present(ctx, "example.test", "token", "key-authorization"); err != nil {
		t.Fatalf("unsigned Present() error = %v", err)
	}
	if got := awaitDNSUpdate(t, updates).IsTsig(); got != nil {
		t.Fatalf("unsigned update contained TSIG: %#v", got)
	}
}

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

func TestRFC2136ProviderAliasesAndCredentialNames(t *testing.T) {
	for _, providerName := range []string{"rfc2136", "dnsupdate"} {
		credentials := dnsProviderCredentials(providerName, map[string]string{
			"RFC2136_NAMESERVER":  "127.0.0.1",
			"RFC2136_TSIG_SECRET": "private-value",
		})
		if credentials["DNSUPDATE_NAMESERVER"] != "127.0.0.1" || credentials["DNSUPDATE_TSIG_SECRET"] != "private-value" {
			t.Fatalf("%s credentials did not receive lego v5 aliases: %#v", providerName, credentials)
		}
		if credentials["RFC2136_NAMESERVER"] != "127.0.0.1" || credentials["RFC2136_TSIG_SECRET"] != "private-value" {
			t.Fatalf("%s credentials lost their stored spelling: %#v", providerName, credentials)
		}
	}
	credentials := dnsProviderCredentials("dnsupdate", map[string]string{"DNSUPDATE_NAMESERVER": "127.0.0.1"})
	if credentials["RFC2136_NAMESERVER"] != "127.0.0.1" {
		t.Fatalf("DNSUPDATE credentials did not receive legacy aliases: %#v", credentials)
	}
	for _, providerName := range []string{"rfc2136", "dnsupdate"} {
		provider, err := NewDNSChallengeProvider(providerName, map[string]string{"RFC2136_NAMESERVER": "127.0.0.1"})
		if err != nil || provider == nil {
			t.Fatalf("NewDNSChallengeProvider(%q) = (%v, %v)", providerName, provider, err)
		}
	}
}
