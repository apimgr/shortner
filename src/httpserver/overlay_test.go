// Tests for the overlay-network HTTP logic (AI.md PART 31 + PART 12
// overlay HTTP semantics): host classification, request tagging, client
// identity substitution, proxy resolution, security-header suppression,
// Onion-Location advertisement, and the health-endpoint overlay fields.
package httpserver

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

// TestOverlayHostNetwork covers every host-shape branch: bare onion, a
// host:port pair, uppercase (base32 is case-insensitive), a trailing dot,
// a bracketed IPv6 literal, both I2P address forms, and a clearnet host.
func TestOverlayHostNetwork(t *testing.T) {
	tests := []struct {
		host string
		want OverlayNetwork
	}{
		{"abc123def456.onion", OverlayTor},
		{"abc123def456.onion:80", OverlayTor},
		{"ABC123DEF456.ONION", OverlayTor},
		{"abc123def456.onion.", OverlayTor},
		{"[::1]:8080", OverlayNone},
		{"xyz.b32.i2p", OverlayI2P},
		{"site.i2p", OverlayI2P},
		{"example.com", OverlayNone},
		{"", OverlayNone},
	}
	for _, tt := range tests {
		if got := OverlayHostNetwork(tt.host); got != tt.want {
			t.Errorf("OverlayHostNetwork(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}
}

// TestOverlayOfAndIsOverlay covers the two independent tagging signals —
// the connection context and an exact Host match against the published
// address — plus the clearnet default and the untrusted-spoof case: per
// AI.md PART 12 "Tor Request Detection", a Host merely ending in .onion is
// NOT enough; it must equal the currently published address.
func TestOverlayOfAndIsOverlay(t *testing.T) {
	resolver := NewProxyResolver(nil)
	resolver.SetOverlayHost(OverlayTor, "abc123def456.onion")

	ctxReq := httptest.NewRequest("GET", "/", nil)
	ctxReq.Host = "example.com"
	ctxReq = ctxReq.WithContext(withOverlayNetwork(ctxReq.Context(), OverlayTor))
	if got := resolver.OverlayOf(ctxReq); got != OverlayTor {
		t.Errorf("OverlayOf(context-tagged) = %q, want tor", got)
	}
	if !resolver.IsOverlay(ctxReq) {
		t.Error("IsOverlay(context-tagged) = false, want true")
	}

	hostReq := httptest.NewRequest("GET", "/", nil)
	hostReq.Host = "abc123def456.onion"
	if got := resolver.OverlayOf(hostReq); got != OverlayTor {
		t.Errorf("OverlayOf(host matches published address) = %q, want tor", got)
	}
	if !resolver.IsOverlay(hostReq) {
		t.Error("IsOverlay(host matches published address) = false, want true")
	}

	clearReq := httptest.NewRequest("GET", "/", nil)
	clearReq.Host = "example.com"
	if got := resolver.OverlayOf(clearReq); got != OverlayNone {
		t.Errorf("OverlayOf(clearnet) = %q, want none", got)
	}
	if resolver.IsOverlay(clearReq) {
		t.Error("IsOverlay(clearnet) = true, want false")
	}

	// A Host that merely ends in .onion but was never published (an
	// untrusted client naming an address that does not exist) must not be
	// classified as overlay traffic — this is the CVE-class bypass an
	// unpublished/mismatched suffix match would otherwise allow.
	spoofedReq := httptest.NewRequest("GET", "/", nil)
	spoofedReq.Host = "spoofed-not-published.onion"
	if got := resolver.OverlayOf(spoofedReq); got != OverlayNone {
		t.Errorf("OverlayOf(unpublished onion suffix) = %q, want none", got)
	}

	if resolver.OverlayOf(nil) != OverlayNone {
		t.Error("OverlayOf(nil) must not panic and must return OverlayNone")
	}

	var nilResolver *ProxyResolver
	if nilResolver.OverlayOf(hostReq) != OverlayNone {
		t.Error("OverlayOf on a nil resolver must not panic and must return OverlayNone")
	}
}

// TestOverlayClientIdentity covers a Tor request with an exported HAProxy
// circuit id, a Tor request without one (bare loopback peer), an I2P
// request, and a clearnet request.
func TestOverlayClientIdentity(t *testing.T) {
	resolver := NewProxyResolver(nil)
	resolver.SetOverlayHost(OverlayTor, "abc123def456.onion")
	resolver.SetOverlayHost(OverlayI2P, "site.i2p")

	torWithCircuit := httptest.NewRequest("GET", "/", nil)
	torWithCircuit.Host = "abc123def456.onion"
	torWithCircuit.RemoteAddr = "10.1.2.3:9999"
	if got := resolver.OverlayClientIdentity(torWithCircuit); !strings.HasPrefix(got, "tor:") || got == "tor:" {
		t.Errorf("OverlayClientIdentity(tor+circuit) = %q, want tor:{circuit}", got)
	}

	torNoCircuit := httptest.NewRequest("GET", "/", nil)
	torNoCircuit.Host = "abc123def456.onion"
	torNoCircuit.RemoteAddr = "127.0.0.1:9999"
	if got := resolver.OverlayClientIdentity(torNoCircuit); got != "tor" {
		t.Errorf("OverlayClientIdentity(tor, no circuit) = %q, want literal tor", got)
	}

	i2pReq := httptest.NewRequest("GET", "/", nil)
	i2pReq.Host = "site.i2p"
	if got := resolver.OverlayClientIdentity(i2pReq); got != "i2p" {
		t.Errorf("OverlayClientIdentity(i2p) = %q, want literal i2p", got)
	}

	clearReq := httptest.NewRequest("GET", "/", nil)
	clearReq.Host = "example.com"
	if got := resolver.OverlayClientIdentity(clearReq); got != "" {
		t.Errorf("OverlayClientIdentity(clearnet) = %q, want empty", got)
	}

	// A Host naming an unpublished onion address must not be granted the
	// "tor" identity substitution — see TestOverlayOfAndIsOverlay.
	spoofedReq := httptest.NewRequest("GET", "/", nil)
	spoofedReq.Host = "spoofed-not-published.onion"
	spoofedReq.RemoteAddr = "203.0.113.9:1234"
	if got := resolver.OverlayClientIdentity(spoofedReq); got != "" {
		t.Errorf("OverlayClientIdentity(unpublished onion) = %q, want empty (real IP must be used)", got)
	}
}

// TestResolveClientIPNeverLeaksLoopbackForOverlay is the security-critical
// assertion: an overlay request must never resolve to 127.0.0.1, even when
// it carries a forwarded-for header and its peer is an always-trusted
// loopback address that would otherwise make that header authoritative.
func TestResolveClientIPNeverLeaksLoopbackForOverlay(t *testing.T) {
	resolver := NewProxyResolver(nil)
	resolver.SetOverlayHost(OverlayTor, "abc123def456.onion")

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "abc123def456.onion"
	req.RemoteAddr = "127.0.0.1:9999"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	req.Header.Set("X-Real-IP", "203.0.113.5")

	got := resolver.ResolveClientIP(req)
	if got == "127.0.0.1" {
		t.Fatalf("ResolveClientIP() leaked loopback for an overlay request: %q", got)
	}
	if got != "tor" {
		t.Errorf("ResolveClientIP(tor, no circuit) = %q, want literal tor", got)
	}

	// Sanity check: the same untrusted-header shape resolves normally for a
	// clearnet request from a trusted peer, so the overlay branch really is
	// the thing suppressing the leak above and not some other change.
	clearReq := httptest.NewRequest("GET", "/", nil)
	clearReq.Host = "example.com"
	clearReq.RemoteAddr = "127.0.0.1:9999"
	clearReq.Header.Set("X-Real-IP", "203.0.113.5")
	if got := resolver.ResolveClientIP(clearReq); got != "203.0.113.5" {
		t.Errorf("ResolveClientIP(clearnet, trusted peer) = %q, want forwarded header honored", got)
	}
}

// TestGetURLVarsOverlayAlwaysHTTP proves overlay resolution never yields
// https, even when a trusted loopback peer sends X-Forwarded-Proto: https,
// and that it uses the overlay host rather than any forwarded host.
func TestGetURLVarsOverlayAlwaysHTTP(t *testing.T) {
	resolver := NewProxyResolver(nil)
	resolver.SetOverlayHost(OverlayTor, "abc123def456.onion")

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "abc123def456.onion"
	req.RemoteAddr = "127.0.0.1:9999"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "example.com")

	proto, fqdn, port := resolver.GetURLVars(req)
	if proto != "http" {
		t.Errorf("GetURLVars(overlay) proto = %q, want http even with X-Forwarded-Proto: https", proto)
	}
	if fqdn != "abc123def456.onion" {
		t.Errorf("GetURLVars(overlay) fqdn = %q, want the overlay host, not the forwarded host", fqdn)
	}
	if port != "" {
		t.Errorf("GetURLVars(overlay) port = %q, want empty", port)
	}
}

// TestGetURLVarsOverlayListenerFallsBackToPublishedHost covers the
// mismatched-Host case: the connection arrived on the overlay backend
// listener (context-tagged) but the client sent an unrelated Host, so
// GetURLVars must answer under the address SetOverlayHost published rather
// than echo the client-supplied Host back over the overlay.
func TestGetURLVarsOverlayListenerFallsBackToPublishedHost(t *testing.T) {
	resolver := NewProxyResolver(nil)
	resolver.SetOverlayHost(OverlayTor, "published123.onion")

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "not-the-onion.example"
	req = req.WithContext(withOverlayNetwork(req.Context(), OverlayTor))

	proto, fqdn, _ := resolver.GetURLVars(req)
	if proto != "http" {
		t.Errorf("GetURLVars(overlay listener) proto = %q, want http", proto)
	}
	if fqdn != "published123.onion" {
		t.Errorf("GetURLVars(overlay listener) fqdn = %q, want the published overlay host", fqdn)
	}
}

// TestSetOverlayHostRoundTrip covers publish, overwrite, and clear-with-
// empty-string, plus the no-op branches (nil resolver, OverlayNone).
func TestSetOverlayHostRoundTrip(t *testing.T) {
	resolver := NewProxyResolver(nil)

	if got := resolver.overlayHost(OverlayTor); got != "" {
		t.Fatalf("overlayHost() before publish = %q, want empty", got)
	}

	resolver.SetOverlayHost(OverlayTor, "ABC123.onion")
	if got := resolver.overlayHost(OverlayTor); got != "abc123.onion" {
		t.Errorf("overlayHost() after publish = %q, want lowercased address", got)
	}

	resolver.SetOverlayHost(OverlayTor, "def456.onion")
	if got := resolver.overlayHost(OverlayTor); got != "def456.onion" {
		t.Errorf("overlayHost() after overwrite = %q, want the newest address", got)
	}

	resolver.SetOverlayHost(OverlayTor, "")
	if got := resolver.overlayHost(OverlayTor); got != "" {
		t.Errorf("overlayHost() after clear = %q, want empty", got)
	}

	// OverlayNone must never be stored — it is not a real overlay network.
	resolver.SetOverlayHost(OverlayNone, "should-not-be-stored")
	if got := resolver.overlayHost(OverlayNone); got != "" {
		t.Errorf("overlayHost(OverlayNone) = %q, want it never got stored", got)
	}

	// A nil resolver must never panic.
	var nilResolver *ProxyResolver
	nilResolver.SetOverlayHost(OverlayTor, "x.onion")
	if got := nilResolver.overlayHost(OverlayTor); got != "" {
		t.Errorf("overlayHost() on a nil resolver = %q, want empty", got)
	}
}

// TestSecurityHeadersMiddlewareSuppressesOverlayOnly drives the actual
// middleware wrapper (not apply() directly) to prove HSTS and CSP's
// upgrade-insecure-requests never reach an overlay client even on a TLS
// connection, while a clearnet TLS request still gets both.
func TestSecurityHeadersMiddlewareSuppressesOverlayOnly(t *testing.T) {
	cfg := config.Default(":memory:")
	resolver := NewProxyResolver(nil)
	resolver.SetOverlayHost(OverlayTor, "abc123def456.onion")
	hd := newHeaderDeps(cfg, resolver)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := hd.securityHeadersMiddleware(next)

	overlayReq := httptest.NewRequest("GET", "/", nil)
	overlayReq.Host = "abc123def456.onion"
	overlayReq.TLS = &tls.ConnectionState{}
	overlayRec := httptest.NewRecorder()
	handler.ServeHTTP(overlayRec, overlayReq)

	if got := overlayRec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("overlay request via middleware got Strict-Transport-Security = %q, want empty", got)
	}
	if csp := overlayRec.Header().Get("Content-Security-Policy") + overlayRec.Header().Get("Content-Security-Policy-Report-Only"); strings.Contains(csp, "upgrade-insecure-requests") {
		t.Errorf("overlay request via middleware got upgrade-insecure-requests in CSP: %q", csp)
	}

	clearReq := httptest.NewRequest("GET", "/", nil)
	clearReq.Host = "example.com"
	clearReq.TLS = &tls.ConnectionState{}
	clearRec := httptest.NewRecorder()
	handler.ServeHTTP(clearRec, clearReq)

	if got := clearRec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("clearnet TLS request via middleware got no Strict-Transport-Security, want it set")
	}
	if csp := clearRec.Header().Get("Content-Security-Policy") + clearRec.Header().Get("Content-Security-Policy-Report-Only"); !strings.Contains(csp, "upgrade-insecure-requests") {
		t.Errorf("clearnet TLS request via middleware missing upgrade-insecure-requests in CSP: %q", csp)
	}
}

// newTestHeaderDeps builds a headerDeps with a Tor address published, for
// the onionLocationMiddleware tests below.
func newTestHeaderDeps(t *testing.T) *headerDeps {
	t.Helper()
	cfg := config.Default(":memory:")
	resolver := NewProxyResolver(nil)
	resolver.SetOverlayHost(OverlayTor, "abc123def456.onion")
	return newHeaderDeps(cfg, resolver)
}

// navigationRequest builds a top-level HTML navigation request.
func navigationRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	return req
}

// TestOnionLocationMiddlewareSetsHeaderOnClearnetHTMLNavigation proves the
// positive case: a 2xx text/html top-level navigation on clearnet gets
// Onion-Location, with the query string preserved.
func TestOnionLocationMiddlewareSetsHeaderOnClearnetHTMLNavigation(t *testing.T) {
	hd := newTestHeaderDeps(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})
	handler := hd.onionLocationMiddleware(next)

	req := navigationRequest("/list?page=2")
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	want := "http://abc123def456.onion/list?page=2"
	if got := rec.Header().Get("Onion-Location"); got != want {
		t.Errorf("Onion-Location = %q, want %q", got, want)
	}
}

// TestOnionLocationMiddlewareOmittedCases covers every branch that must
// suppress the header: no onion published, the request is itself overlay,
// a JSON response, a redirect status, and a POST request.
func TestOnionLocationMiddlewareOmittedCases(t *testing.T) {
	htmlNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
	})
	jsonNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})
	redirectNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusFound)
	})

	// (a) No onion address is published.
	t.Run("no onion published", func(t *testing.T) {
		cfg := config.Default(":memory:")
		resolver := NewProxyResolver(nil)
		hd := newHeaderDeps(cfg, resolver)
		req := navigationRequest("/")
		req.Host = "example.com"
		rec := httptest.NewRecorder()
		hd.onionLocationMiddleware(htmlNext).ServeHTTP(rec, req)
		if got := rec.Header().Get("Onion-Location"); got != "" {
			t.Errorf("Onion-Location = %q, want empty with no onion published", got)
		}
	})

	// (b) The request is itself an overlay request.
	t.Run("overlay request", func(t *testing.T) {
		hd := newTestHeaderDeps(t)
		req := navigationRequest("/")
		req.Host = "abc123def456.onion"
		rec := httptest.NewRecorder()
		hd.onionLocationMiddleware(htmlNext).ServeHTTP(rec, req)
		if got := rec.Header().Get("Onion-Location"); got != "" {
			t.Errorf("Onion-Location = %q, want empty for an overlay request", got)
		}
	})

	// (c) The response is JSON.
	t.Run("json response", func(t *testing.T) {
		hd := newTestHeaderDeps(t)
		req := navigationRequest("/api/v1/links")
		req.Host = "example.com"
		rec := httptest.NewRecorder()
		hd.onionLocationMiddleware(jsonNext).ServeHTTP(rec, req)
		if got := rec.Header().Get("Onion-Location"); got != "" {
			t.Errorf("Onion-Location = %q, want empty for a JSON response", got)
		}
	})

	// (d) The status is a 3xx redirect.
	t.Run("redirect status", func(t *testing.T) {
		hd := newTestHeaderDeps(t)
		req := navigationRequest("/old")
		req.Host = "example.com"
		rec := httptest.NewRecorder()
		hd.onionLocationMiddleware(redirectNext).ServeHTTP(rec, req)
		if got := rec.Header().Get("Onion-Location"); got != "" {
			t.Errorf("Onion-Location = %q, want empty for a 3xx redirect", got)
		}
	})

	// (e) The method is POST.
	t.Run("post method", func(t *testing.T) {
		hd := newTestHeaderDeps(t)
		req := httptest.NewRequest(http.MethodPost, "/contact", nil)
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Host = "example.com"
		rec := httptest.NewRecorder()
		hd.onionLocationMiddleware(htmlNext).ServeHTTP(rec, req)
		if got := rec.Header().Get("Onion-Location"); got != "" {
			t.Errorf("Onion-Location = %q, want empty for a POST request", got)
		}
	})
}

// fakeTorReporter implements TorReporter with fixed values, for testing
// healthDeps.torInfo() without a live Tor daemon.
type fakeTorReporter struct {
	enabled, running, healthy bool
	address                   string
	err                       string
}

func (f *fakeTorReporter) Enabled() bool        { return f.enabled }
func (f *fakeTorReporter) Running() bool        { return f.running }
func (f *fakeTorReporter) Healthy() bool        { return f.healthy }
func (f *fakeTorReporter) OnionAddress() string { return f.address }
func (f *fakeTorReporter) Err() string          { return f.err }

// fakeI2PReporter implements I2PReporter with fixed values, for testing
// healthDeps.i2pInfo() without a live I2P router.
type fakeI2PReporter struct {
	enabled, running, healthy bool
	address, provider         string
	err                       string
}

func (f *fakeI2PReporter) Enabled() bool          { return f.enabled }
func (f *fakeI2PReporter) Running() bool          { return f.running }
func (f *fakeI2PReporter) Healthy() bool          { return f.healthy }
func (f *fakeI2PReporter) EepsiteAddress() string { return f.address }
func (f *fakeI2PReporter) ProviderName() string   { return f.provider }
func (f *fakeI2PReporter) Err() string            { return f.err }

// TestHealthDepsTorInfoNilReporter proves a nil reporter reads as disabled
// with the checks entry omitted entirely.
func TestHealthDepsTorInfoNilReporter(t *testing.T) {
	h := &healthDeps{}
	info, check := h.torInfo()
	if info.Status != "disabled" {
		t.Errorf("torInfo().Status = %q, want disabled", info.Status)
	}
	if info.Enabled {
		t.Error("torInfo().Enabled = true, want false")
	}
	if check != "" {
		t.Errorf("torInfo() check = %q, want empty (omitted)", check)
	}
}

// TestHealthDepsTorInfoConnectedAndError covers the healthy-connected case
// and the enabled-but-unhealthy error case.
func TestHealthDepsTorInfoConnectedAndError(t *testing.T) {
	h := &healthDeps{tor: &fakeTorReporter{enabled: true, running: true, healthy: true, address: "abc123.onion"}}
	info, check := h.torInfo()
	if info.Status != "healthy" {
		t.Errorf("torInfo().Status = %q, want healthy", info.Status)
	}
	if info.Hostname != "abc123.onion" {
		t.Errorf("torInfo().Hostname = %q, want abc123.onion", info.Hostname)
	}
	if check != "ok" {
		t.Errorf("torInfo() check = %q, want ok", check)
	}

	unhealthy := &healthDeps{tor: &fakeTorReporter{enabled: true, running: true, healthy: false, address: "abc123.onion"}}
	info2, check2 := unhealthy.torInfo()
	if info2.Status != "error:control connection unresponsive" {
		t.Errorf("torInfo().Status = %q, want error:control connection unresponsive", info2.Status)
	}
	if check2 != "error" {
		t.Errorf("torInfo() check = %q, want error when unhealthy", check2)
	}

	withErr := &healthDeps{tor: &fakeTorReporter{enabled: true, running: false, err: "tor binary exited: signal: killed"}}
	info3, check3 := withErr.torInfo()
	if info3.Status != "error:tor binary exited: signal: killed" {
		t.Errorf("torInfo().Status = %q, want the manager's own error message", info3.Status)
	}
	if check3 != "error" {
		t.Errorf("torInfo() check = %q, want error", check3)
	}
}

// TestHealthDepsI2PInfoNilReporter proves a nil reporter reads as disabled
// with provider "none" and the checks entry omitted.
func TestHealthDepsI2PInfoNilReporter(t *testing.T) {
	h := &healthDeps{}
	info, check := h.i2pInfo()
	if info.Status != "disabled" {
		t.Errorf("i2pInfo().Status = %q, want disabled", info.Status)
	}
	if info.Provider != "none" {
		t.Errorf("i2pInfo().Provider = %q, want none", info.Provider)
	}
	if check != "" {
		t.Errorf("i2pInfo() check = %q, want empty (omitted)", check)
	}
}

// TestHealthDepsI2PInfoRunningAndError covers the healthy-running case and
// the enabled-but-unhealthy error case.
func TestHealthDepsI2PInfoRunningAndError(t *testing.T) {
	h := &healthDeps{i2p: &fakeI2PReporter{enabled: true, running: true, healthy: true, address: "xyz.b32.i2p", provider: "i2pd"}}
	info, check := h.i2pInfo()
	if info.Status != "healthy" {
		t.Errorf("i2pInfo().Status = %q, want healthy", info.Status)
	}
	if info.Hostname != "xyz.b32.i2p" || info.Provider != "i2pd" {
		t.Errorf("i2pInfo() = %+v, want hostname/provider populated", info)
	}
	if check != "ok" {
		t.Errorf("i2pInfo() check = %q, want ok", check)
	}

	unhealthy := &healthDeps{i2p: &fakeI2PReporter{enabled: true, running: true, healthy: false, address: "xyz.b32.i2p", provider: "i2pd"}}
	info2, check2 := unhealthy.i2pInfo()
	if info2.Status != "error:provider unresponsive" {
		t.Errorf("i2pInfo().Status = %q, want error:provider unresponsive", info2.Status)
	}
	if check2 != "error" {
		t.Errorf("i2pInfo() check = %q, want error when unhealthy", check2)
	}
}
