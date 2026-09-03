package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyResolverIsTrustedPeer(t *testing.T) {
	r := NewProxyResolver(nil)

	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"loopback trusted", "127.0.0.1:1234", true},
		{"private 10/8 trusted", "10.1.2.3:1234", true},
		{"private 192.168/16 trusted", "192.168.1.1:80", true},
		{"public IP not trusted", "8.8.8.8:1234", false},
		{"bare IP no port", "127.0.0.1", true},
		{"garbage rejected", "not-an-ip", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.IsTrustedPeer(tt.addr); got != tt.want {
				t.Errorf("IsTrustedPeer(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestProxyResolverAdditionalTrust(t *testing.T) {
	r := NewProxyResolver([]string{"203.0.113.5", "198.51.100.0/24"})

	if !r.IsTrustedPeer("203.0.113.5:443") {
		t.Error("expected explicit additional IP to be trusted")
	}
	if !r.IsTrustedPeer("198.51.100.42:443") {
		t.Error("expected additional CIDR member to be trusted")
	}
	if r.IsTrustedPeer("203.0.113.6:443") {
		t.Error("expected unrelated public IP to remain untrusted")
	}
}

func TestResolveClientIPUntrustedPeerIgnoresHeaders(t *testing.T) {
	r := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := r.ResolveClientIP(req); got != "8.8.8.8" {
		t.Errorf("ResolveClientIP() = %q, want 8.8.8.8 (untrusted peer headers ignored)", got)
	}
}

func TestResolveClientIPTrustedPeerHonorsHeaders(t *testing.T) {
	r := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

	if got := r.ResolveClientIP(req); got != "1.2.3.4" {
		t.Errorf("ResolveClientIP() = %q, want leftmost X-Forwarded-For entry 1.2.3.4", got)
	}
}

func TestResolveClientIPPriorityHeaders(t *testing.T) {
	r := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	req.Header.Set("CF-Connecting-IP", "1.1.1.1")

	if got := r.ResolveClientIP(req); got != "1.1.1.1" {
		t.Errorf("ResolveClientIP() = %q, want CF-Connecting-IP to take priority", got)
	}
}

func TestGetURLVarsUntrustedPeerUsesRequestDefaults(t *testing.T) {
	r := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.Host = "example.com:8080"
	req.Header.Set("X-Forwarded-Proto", "https")

	proto, fqdn, port := r.GetURLVars(req)
	if proto != "http" {
		t.Errorf("proto = %q, want http (untrusted peer's header ignored)", proto)
	}
	if fqdn != "example.com" {
		t.Errorf("fqdn = %q, want example.com", fqdn)
	}
	if port != "8080" {
		t.Errorf("port = %q, want 8080", port)
	}
}

func TestGetURLVarsTrustedPeerHonorsForwardedHeaders(t *testing.T) {
	r := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "internal:8080"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "public.example.com")
	req.Header.Set("X-Forwarded-Port", "443")

	proto, fqdn, port := r.GetURLVars(req)
	if proto != "https" {
		t.Errorf("proto = %q, want https", proto)
	}
	if fqdn != "public.example.com" {
		t.Errorf("fqdn = %q, want public.example.com", fqdn)
	}
	if port != "443" {
		t.Errorf("port = %q, want 443", port)
	}
}

func TestBuildURLStripsDefaultPort(t *testing.T) {
	r := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Port", "443")

	got := r.BuildURL(req, "/abc")
	want := "https://example.com/abc"
	if got != want {
		t.Errorf("BuildURL() = %q, want %q", got, want)
	}
}

func TestBuildURLKeepsNonDefaultPort(t *testing.T) {
	r := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "example.com:9090"

	got := r.BuildURL(req, "abc")
	want := "http://example.com:9090/abc"
	if got != want {
		t.Errorf("BuildURL() = %q, want %q", got, want)
	}
}
