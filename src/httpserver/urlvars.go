// URL & FQDN detection and the trusted-proxies trust gate, per AI.md
// PART 12 "Trusted Proxies" and PART 8 "URL Detection". The Tor priority-0
// resolution branch and the periodic (5-minute) DNS refresh for hostname
// entries in `trusted_proxies.additional` are deferred — see TODO.AI.md.
package httpserver

import (
	"net"
	"net/http"
	"strings"
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
// "URL Detection". Proxy headers are only honored from a trusted peer; Tor
// priority-0 resolution and the domain-learning/live-reload layer are
// deferred (see TODO.AI.md).
func (r *ProxyResolver) GetURLVars(req *http.Request) (proto, fqdn, port string) {
	trusted := r.IsTrustedPeer(req.RemoteAddr)

	proto = "http"
	if req.TLS != nil {
		proto = "https"
	}
	if trusted {
		switch {
		case req.Header.Get("X-Forwarded-Ssl") == "on":
			proto = "https"
		case strings.EqualFold(req.Header.Get("X-Url-Scheme"), "https"):
			proto = "https"
		case req.Header.Get("X-Forwarded-Proto") != "":
			proto = strings.ToLower(strings.TrimSpace(strings.Split(req.Header.Get("X-Forwarded-Proto"), ",")[0]))
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
	if trusted {
		if v := strings.TrimSpace(req.Header.Get("X-Forwarded-Port")); v != "" {
			port = v
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
