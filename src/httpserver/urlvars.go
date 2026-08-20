// URL & FQDN detection and the trusted-proxies trust gate, per AI.md
// PART 12 "Trusted Proxies" and PART 8 "URL Detection". The periodic
// (5-minute) DNS refresh for hostname entries in
// `trusted_proxies.additional` is deferred — see TODO.AI.md.
package httpserver

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// alwaysTrustedCIDRs are the private/loopback/link-local ranges trusted
// regardless of configuration, per AI.md PART 12 "Trusted Proxies".
var alwaysTrustedCIDRs = []string{
	"127.0.0.0/8",
	"::1/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"fc00::/7",
	"169.254.0.0/16",
	"fe80::/10",
}

// ProxyResolver evaluates the trusted_proxies gate and resolves
// {proto}/{fqdn}/{port} and the client IP from reverse-proxy headers, per
// AI.md PART 12 "Trusted Proxies" and PART 8 "URL Detection". Resolved once
// at construction from server.yml — the spec's "refreshed every 5 minutes"
// DNS re-resolution for hostname entries in `additional` is deferred (see
// TODO.AI.md).
type ProxyResolver struct {
	nets []*net.IPNet
	ips  map[string]bool
	// overlayMu guards overlayHosts, which holds the live `.onion` and
	// `.b32.i2p` addresses (AI.md PART 31). They are only known once the
	// provider has published them, and change on every regeneration, so
	// they are set after construction rather than read from server.yml.
	overlayMu    sync.RWMutex
	overlayHosts map[OverlayNetwork]string
}

// NewProxyResolver builds a resolver trusting the always-trusted private
// ranges plus every IP/CIDR/hostname in additional. Hostnames are resolved
// once at startup; a lookup failure simply omits that entry rather than
// failing startup, per the Config Validation Rule.
func NewProxyResolver(additional []string) *ProxyResolver {
	r := &ProxyResolver{ips: make(map[string]bool)}
	for _, cidr := range alwaysTrustedCIDRs {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			r.nets = append(r.nets, n)
		}
	}
	for _, entry := range additional {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(entry); err == nil {
			r.nets = append(r.nets, n)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			r.ips[ip.String()] = true
			continue
		}
		if addrs, err := net.LookupHost(entry); err == nil {
			for _, a := range addrs {
				r.ips[a] = true
			}
		}
	}
	return r
}

// IsTrustedPeer reports whether remoteAddr (an "IP:port" or bare IP, as
// found in http.Request.RemoteAddr) is a trusted reverse proxy.
func (r *ProxyResolver) IsTrustedPeer(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if r.ips[ip.String()] {
		return true
	}
	for _, n := range r.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// forwardedForHeaders are checked in priority order for the client IP, per
// AI.md PART 8 "Client IP Detection". Only honored from a trusted peer.
var forwardedForHeaders = []string{
	"CF-Connecting-IP",
	"True-Client-IP",
	"X-Real-IP",
}

// ResolveClientIP returns the request's client IP, honoring reverse-proxy
// headers only when the immediate TCP peer is trusted, per AI.md PART 8
// "Client IP Detection" and PART 12 "Middleware ordering — preserve the
// original TCP peer".
func (r *ProxyResolver) ResolveClientIP(req *http.Request) string {
	// AI.md PART 31 "Tor Request Logging & Identity": the peer of an overlay
	// request is the local daemon on loopback, so it is never a client
	// identifier. Substituting here covers every consumer at once — access
	// logs, audit trails, rate-limit keys, blocklists — so 127.0.0.1 can
	// never be recorded as a Tor client IP.
	if identity := r.OverlayClientIdentity(req); identity != "" {
		return identity
	}
	if !r.IsTrustedPeer(req.RemoteAddr) {
		return stripPort(req.RemoteAddr)
	}
	for _, h := range forwardedForHeaders {
		if v := strings.TrimSpace(req.Header.Get(h)); v != "" {
			return v
		}
	}
	if v := req.Header.Get("X-Forwarded-For"); v != "" {
		parts := strings.Split(v, ",")
		if leftmost := strings.TrimSpace(parts[0]); leftmost != "" {
			return leftmost
		}
	}
	if v := strings.TrimSpace(req.Header.Get("X-Client-IP")); v != "" {
		return v
	}
	return stripPort(req.RemoteAddr)
}

// stripPort removes a trailing ":port" from addr, if present.
func stripPort(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// GetURLVars resolves {proto}, {fqdn}, {port} for req, per AI.md PART 8
// "URL Detection". Proxy headers are only honored from a trusted peer; the
// domain-learning/live-reload layer is deferred (see TODO.AI.md).
//
// AI.md PART 12 "Tor Request Detection" makes overlay detection priority 0:
// it is evaluated before any reverse-proxy header, is always trusted with no
// trusted_proxies check, forces `http` (an overlay address can never be
// https), and strips the port so an absolute URL is always
// `http://{onion_address}{path}`.
func (r *ProxyResolver) GetURLVars(req *http.Request) (proto, fqdn, port string) {
	if network := r.OverlayOf(req); network != OverlayNone {
		host := req.Host
		if OverlayHostNetwork(host) != network {
			// The connection arrived on the overlay backend listener but the
			// client sent some other Host. The overlay address is the only
			// name this request may be answered under, so fall back to the
			// running service's own address rather than echoing the clearnet
			// host back over the onion.
			if overlayHost := r.overlayHost(network); overlayHost != "" {
				host = overlayHost
			}
		}
		overlayFQDN, _ := splitHostPort(host)
		return "http", overlayFQDN, ""
	}

	trusted := r.IsTrustedPeer(req.RemoteAddr)

	proto = "http"
	if req.TLS != nil {
		proto = "https"
	}
	// Priority per AI.md PART 8 "{proto} Resolution": X-Forwarded-Proto,
	// X-Forwarded-Ssl, X-Url-Scheme, Front-End-Https (Microsoft), then the
	// TLS state of the connection, then "http".
	if trusted {
		switch {
		case req.Header.Get("X-Forwarded-Proto") != "":
			proto = strings.ToLower(strings.TrimSpace(strings.Split(req.Header.Get("X-Forwarded-Proto"), ",")[0]))
		case strings.EqualFold(req.Header.Get("X-Forwarded-Ssl"), "on"):
			proto = "https"
		case strings.EqualFold(req.Header.Get("X-Forwarded-Ssl"), "off"):
			proto = "http"
		case strings.EqualFold(req.Header.Get("X-Url-Scheme"), "https"):
			proto = "https"
		case strings.EqualFold(req.Header.Get("X-Url-Scheme"), "http"):
			proto = "http"
		case strings.EqualFold(req.Header.Get("Front-End-Https"), "on"):
			proto = "https"
		}
	}

	host := req.Host
	if trusted {
		for _, h := range []string{"X-Forwarded-Host", "X-Real-Host", "X-Original-Host"} {
			if v := strings.TrimSpace(req.Header.Get(h)); v != "" {
				host = v
				break
			}
		}
	}
	fqdn, hostPort := splitHostPort(host)
	port = hostPort
	// X-Real-Port is the nginx alternative to X-Forwarded-Port (AI.md
	// PART 8 "Port Detection").
	if trusted {
		for _, h := range []string{"X-Forwarded-Port", "X-Real-Port"} {
			if v := strings.TrimSpace(req.Header.Get(h)); v != "" {
				port = v
				break
			}
		}
	}
	return proto, fqdn, port
}

// splitHostPort splits a "host" or "host:port" value into its parts. If
// host has no port, port is "".
func splitHostPort(host string) (fqdn, port string) {
	if h, p, err := net.SplitHostPort(host); err == nil {
		return h, p
	}
	return host, ""
}

// BuildURL builds an absolute URL for reqPath using req's resolved
// {proto}/{fqdn}/{port}, per AI.md PART 8 "URL Builder". Default ports
// (80/443) are stripped.
func (r *ProxyResolver) BuildURL(req *http.Request, reqPath string) string {
	proto, fqdn, port := r.GetURLVars(req)
	var b strings.Builder
	b.WriteString(proto)
	b.WriteString("://")
	b.WriteString(fqdn)
	if port != "" && !isDefaultPort(proto, port) {
		b.WriteByte(':')
		b.WriteString(port)
	}
	if !strings.HasPrefix(reqPath, "/") {
		b.WriteByte('/')
	}
	b.WriteString(reqPath)
	return b.String()
}

// isDefaultPort reports whether port is the implicit default for proto.
func isDefaultPort(proto, port string) bool {
	return (proto == "http" && port == "80") || (proto == "https" && port == "443")
}
