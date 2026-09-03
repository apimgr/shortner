// Overlay-network request handling: AI.md PART 31 "Tor Parity Principle"
// and PART 12's rule that `.onion` and `.b32.i2p` are ALWAYS plain http://.
//
// Two independent signals mark a request as overlay traffic, and either one
// is sufficient. The connection is tagged when it arrives on a dedicated
// overlay backend listener, and the Host header is checked for the overlay
// suffixes. Both exist because a reverse-proxied deployment can forward
// onion traffic over a clearnet socket, and a direct backend connection can
// carry a Host the client chose — trusting only one of them would let an
// overlay request be treated as clearnet and leak a redirect to HTTPS.
package httpserver

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/shortner/src/common/netutil"
)

// OverlayNetwork identifies which overlay network a request arrived over.
type OverlayNetwork string

const (
	// OverlayNone is ordinary clearnet traffic.
	OverlayNone OverlayNetwork = ""
	// OverlayTor is a Tor hidden service request.
	OverlayTor OverlayNetwork = "tor"
	// OverlayI2P is an I2P eepsite request.
	OverlayI2P OverlayNetwork = "i2p"
)

// Overlay address suffixes. They are reserved TLDs that can never resolve
// on the public DNS, so matching on them cannot collide with a real host.
const (
	onionSuffix = ".onion"
	// i2pSuffix is the whole reserved I2P TLD, not just `.b32.i2p`: an
	// eepsite is equally reachable under a hosts.txt name like `site.i2p`,
	// and both forms are equally never-HTTPS.
	i2pSuffix = ".i2p"
)

// overlayCtxKey keys the per-connection overlay tag in a request context.
type overlayCtxKey struct{}

// withOverlayNetwork tags a connection's context with its overlay network.
func withOverlayNetwork(ctx context.Context, network OverlayNetwork) context.Context {
	return context.WithValue(ctx, overlayCtxKey{}, network)
}

// overlayFromContext returns the connection's overlay tag, if any.
func overlayFromContext(ctx context.Context) OverlayNetwork {
	if network, ok := ctx.Value(overlayCtxKey{}).(OverlayNetwork); ok {
		return network
	}
	return OverlayNone
}

// OverlayHostNetwork classifies a Host header by its overlay suffix. The
// port is ignored and matching is case-insensitive, because onion and
// eepsite addresses are case-insensitive base32.
func OverlayHostNetwork(host string) OverlayNetwork {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	host = strings.TrimSuffix(host, ".")
	switch {
	case strings.HasSuffix(host, i2pSuffix):
		return OverlayI2P
	case strings.HasSuffix(host, onionSuffix):
		return OverlayTor
	default:
		return OverlayNone
	}
}

// OverlayOf returns the overlay network a request belongs to.
//
// The listener tag is always authoritative — it is set by ServeOverlay on
// the dedicated backend listener, a connection property the client cannot
// forge. Absent that tag (a reverse-proxied deployment forwarding onion/
// eepsite traffic over the clearnet socket), AI.md PART 12 "Tor Request
// Detection" makes the Host header authoritative too, but only when it
// matches the published address EXACTLY — not merely by suffix. A bare
// `.onion`/`.i2p` suffix match would let any client name an address that
// was never actually provisioned; requiring the exact currently-published
// address is what "matches tor.onion_address" means, and per PART 12 that
// match is "always trusted, no IP check against trusted_proxies required".
func (r *ProxyResolver) OverlayOf(req *http.Request) OverlayNetwork {
	if req == nil {
		return OverlayNone
	}
	if network := overlayFromContext(req.Context()); network != OverlayNone {
		return network
	}
	if r == nil {
		return OverlayNone
	}
	network := OverlayHostNetwork(req.Host)
	if network == OverlayNone {
		return OverlayNone
	}
	published := r.overlayHost(network)
	if published == "" {
		return OverlayNone
	}
	host, _ := splitHostPort(strings.ToLower(strings.TrimSpace(req.Host)))
	if host == "" || host != published {
		return OverlayNone
	}
	return network
}

// IsOverlay reports whether a request arrived over Tor or I2P. Every
// HTTPS-implying behavior — the redirect, HSTS, and CSP's
// upgrade-insecure-requests — is suppressed when this is true, because an
// overlay address has no certificate and is already end-to-end encrypted by
// the network itself.
func (r *ProxyResolver) IsOverlay(req *http.Request) bool {
	return r.OverlayOf(req) != OverlayNone
}

// OverlayClientIdentity returns the identity string to use in place of a
// client IP for an overlay request, or an empty string for clearnet.
//
// A Tor request's transport peer is always 127.0.0.1, which AI.md PART 31.1
// forbids ever being logged or displayed as the client IP. When Tor is
// configured with `HiddenServiceExportCircuitID haproxy` the PROXY header
// carries a synthetic per-circuit address, which becomes `tor:{circuit_id}`;
// without it the identity is the bare literal `tor`. I2P provides no client
// information whatsoever, so it is always the literal `i2p`.
func (r *ProxyResolver) OverlayClientIdentity(req *http.Request) string {
	switch r.OverlayOf(req) {
	case OverlayTor:
		if circuit := torCircuitID(req); circuit != "" {
			return "tor:" + circuit
		}
		return "tor"
	case OverlayI2P:
		return "i2p"
	default:
		return ""
	}
}

// torCircuitID derives the opaque per-circuit token from the synthetic
// source address Tor exported through the PROXY-protocol header. Loopback
// means circuit-ID export is off (or the request did not come through the
// PROXY listener), in which case there is no circuit to key on.
func torCircuitID(req *http.Request) string {
	host := stripPort(req.RemoteAddr)
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return netutil.CircuitToken(overlayAddr(host))
}

// overlayAddr adapts a host string to the net.Addr that CircuitToken takes.
type overlayAddr string

// Network satisfies net.Addr; the circuit token only ever hashes String().
func (a overlayAddr) Network() string { return "tor" }

// String satisfies net.Addr.
func (a overlayAddr) String() string { return string(a) }

// SetOverlayHost records the live address of an overlay network so absolute
// URLs built for an overlay request name the overlay, and so clearnet HTML
// responses can advertise the hidden service via Onion-Location. An empty
// host clears the entry, which is how a stopped provider stops being
// advertised.
func (r *ProxyResolver) SetOverlayHost(network OverlayNetwork, host string) {
	if r == nil || network == OverlayNone {
		return
	}
	r.overlayMu.Lock()
	defer r.overlayMu.Unlock()
	if host == "" {
		delete(r.overlayHosts, network)
		return
	}
	if r.overlayHosts == nil {
		r.overlayHosts = make(map[OverlayNetwork]string)
	}
	r.overlayHosts[network] = strings.ToLower(host)
}

// overlayHost returns the recorded address for an overlay network, or an
// empty string when that network is not published.
func (r *ProxyResolver) overlayHost(network OverlayNetwork) string {
	if r == nil {
		return ""
	}
	r.overlayMu.RLock()
	defer r.overlayMu.RUnlock()
	return r.overlayHosts[network]
}

// SetOverlayHost publishes an overlay network's live address to the server's
// resolver. The Tor and I2P managers call it on every successful start and
// with an empty host when the provider stops.
func (s *Server) SetOverlayHost(network OverlayNetwork, host string) {
	if s == nil {
		return
	}
	s.resolver.SetOverlayHost(network, host)
}

// onionLocationWriter defers the Onion-Location decision to WriteHeader,
// which is the first moment the status code and Content-Type are both
// known. AI.md PART 31 "Onion-Location Advertisement" allows the header on
// 2xx HTML documents only, so it can never be decided up front.
type onionLocationWriter struct {
	http.ResponseWriter
	value string
	done  bool
}

// WriteHeader adds Onion-Location when the response turned out to be an
// HTML document with a 2xx status, and never otherwise.
func (w *onionLocationWriter) WriteHeader(status int) {
	if !w.done {
		w.done = true
		if status >= 200 && status < 300 && isHTMLContentType(w.Header().Get("Content-Type")) {
			w.ResponseWriter.Header().Set("Onion-Location", w.value)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write covers handlers that never call WriteHeader explicitly, where Go
// implies a 200.
func (w *onionLocationWriter) Write(b []byte) (int, error) {
	if !w.done {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// isHTMLContentType reports whether a Content-Type names an HTML document.
func isHTMLContentType(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "text/html")
}

// isTopLevelNavigation reports whether a request is a browser navigating to
// a page, rather than fetching a subresource or calling the API. Sec-Fetch
// metadata is authoritative when the browser sent it; otherwise an Accept
// header preferring HTML on a safe method is the fallback.
func isTopLevelNavigation(req *http.Request) bool {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false
	}
	if dest := req.Header.Get("Sec-Fetch-Dest"); dest != "" {
		return strings.EqualFold(dest, "document")
	}
	return strings.Contains(strings.ToLower(req.Header.Get("Accept")), "text/html")
}

// onionLocationMiddleware advertises the hidden service on clearnet HTML
// document responses, per AI.md PART 31 "Onion-Location Advertisement".
//
// It is skipped entirely for overlay requests (the onion never advertises
// itself), for non-navigations (API, JSON, static assets), and whenever no
// onion address is published — and the writer wrapper drops it again for
// any redirect or non-HTML body.
func (hd *headerDeps) onionLocationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		onion := hd.resolver.overlayHost(OverlayTor)
		if onion == "" || hd.resolver.IsOverlay(req) || !isTopLevelNavigation(req) {
			next.ServeHTTP(w, req)
			return
		}
		target := &url.URL{Scheme: "http", Host: onion, Path: req.URL.Path, RawQuery: req.URL.RawQuery}
		next.ServeHTTP(&onionLocationWriter{ResponseWriter: w, value: target.String()}, req)
	})
}

// ServeOverlay serves the same handler on an overlay backend listener,
// tagging every connection with its network. It blocks until the listener
// closes, exactly like Start, and is meant to be run in its own goroutine
// alongside the clearnet listener.
//
// The overlay server is always plain HTTP with no TLS config, no matter
// what the clearnet listener does — a `.onion` or `.b32.i2p` address can
// never present a certificate.
func (s *Server) ServeOverlay(ln net.Listener, network OverlayNetwork) error {
	if ln == nil {
		return nil
	}
	srv := &http.Server{
		Handler:      s.httpServer.Handler,
		ReadTimeout:  s.httpServer.ReadTimeout,
		WriteTimeout: s.httpServer.WriteTimeout,
		IdleTimeout:  s.httpServer.IdleTimeout,
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return withOverlayNetwork(ctx, network)
		},
	}

	s.overlayMu.Lock()
	s.overlayServers = append(s.overlayServers, srv)
	s.overlayMu.Unlock()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// shutdownOverlays stops every overlay listener that ServeOverlay started.
func (s *Server) shutdownOverlays(ctx context.Context) {
	s.overlayMu.Lock()
	servers := append([]*http.Server(nil), s.overlayServers...)
	s.overlayServers = nil
	s.overlayMu.Unlock()
	for _, srv := range servers {
		_ = srv.Shutdown(ctx)
	}
}
