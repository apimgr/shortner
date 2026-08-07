// Package fqdn implements static (non-request-scoped) FQDN resolution and
// dev-TLD detection, per AI.md PART 15 "FQDN Resolution" / "Dev TLD
// Handling". Request-scoped {proto}/{fqdn}/{port} resolution (reverse-
// proxy headers first) is a separate concern implemented by
// httpserver.ProxyResolver.GetURLVars — this package covers the startup-
// time / background-task resolution path (env vars, hostname, global IP,
// localhost fallback) that TLS certificate provisioning needs before any
// request has arrived.
package fqdn

import (
	"net"
	"os"
	"strings"
)

// devOnlyTLDs are the static reserved/local TLDs that never get a public
// Let's Encrypt certificate, per AI.md PART 15 "Static Dev TLDs".
var devOnlyTLDs = map[string]bool{
	"local": true, "test": true, "example": true, "invalid": true,
	"localhost": true, "lan": true, "internal": true, "home": true,
	"localdomain": true, "home.arpa": true, "intranet": true,
	"corp": true, "private": true,
}

// GetAllDomains returns every domain in the DOMAIN env var (comma-
// separated), trimmed, in the order given. Returns nil if DOMAIN is unset.
func GetAllDomains() []string {
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return nil
	}
	parts := strings.Split(domain, ",")
	domains := make([]string, 0, len(parts))
	for _, p := range parts {
		if d := strings.TrimSpace(p); d != "" {
			domains = append(domains, d)
		}
	}
	return domains
}

// GetFQDN resolves the primary FQDN for projectName, per AI.md PART 15
// "FQDN Resolution" priority order (DOMAIN env var, os.Hostname(),
// $HOSTNAME env var, global IPv6, global IPv4, "localhost"). Reverse-proxy
// header resolution (priority 1 in the full table) is request-scoped and
// handled separately by httpserver.ProxyResolver.
func GetFQDN(projectName string) string {
	if domains := GetAllDomains(); len(domains) > 0 {
		return domains[0]
	}

	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		if !isLoopback(hostname) {
			return hostname
		}
	}

	if hostname := os.Getenv("HOSTNAME"); hostname != "" {
		if !isLoopback(hostname) {
			return hostname
		}
	}

	if ipv6 := getGlobalIPv6(); ipv6 != "" {
		return ipv6
	}
	if ipv4 := getGlobalIPv4(); ipv4 != "" {
		return ipv4
	}

	return "localhost"
}

// isLoopback reports whether host is "localhost" or a loopback IP.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// getGlobalIPv6 returns the first public IPv6 address on any local
// interface, excluding loopback/link-local/unique-local ranges.
func getGlobalIPv6() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			ip := ipnet.IP
			if ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

// getGlobalIPv4 returns the first public IPv4 address on any local
// interface, excluding loopback/private/link-local ranges.
func getGlobalIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			ip := ipnet.IP
			if ip4 := ip.To4(); ip4 != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				return ip4.String()
			}
		}
	}
	return ""
}

// IsDevTLD reports whether host is a development-only TLD that must never
// receive a public Let's Encrypt certificate, per AI.md PART 15 "Dev TLD
// Handling": the project's own name as a TLD (and `{project_name}.local` /
// `{project_name}.test`), plus the static RFC 6761 / locally-reserved TLD
// set.
func IsDevTLD(host, projectName string) bool {
	lower := strings.ToLower(host)

	if projectName != "" {
		pn := strings.ToLower(projectName)
		if lower == pn || strings.HasSuffix(lower, "."+pn) {
			return true
		}
	}

	for tld := range devOnlyTLDs {
		if lower == tld || strings.HasSuffix(lower, "."+tld) {
			return true
		}
	}

	return false
}
