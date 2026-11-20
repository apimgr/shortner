package urlutil

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"mime"
)

// FetchRemoteImageConfig limits the network and response behavior of a remote
// branding image fetch.
type FetchRemoteImageConfig struct {
	MaxSize        int64
	Timeout        time.Duration
	AllowedTypes   []string
	AllowedSchemes []string
}

// DefaultFetchRemoteImageConfig returns the production image fetch policy.
func DefaultFetchRemoteImageConfig() FetchRemoteImageConfig {
	return FetchRemoteImageConfig{
		MaxSize: 10 << 20, Timeout: 30 * time.Second,
		AllowedTypes:   []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon"},
		AllowedSchemes: []string{"https"},
	}
}

// ValidateRemoteURL rejects non-HTTPS URLs and names that resolve to local or
// non-global addresses. FetchRemoteImage repeats this validation while dialing
// so a DNS change between validation and connection cannot bypass the policy.
func ValidateRemoteURL(rawURL string, cfg FetchRemoteImageConfig) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return fmt.Errorf("invalid URL")
	}
	allowed := false
	for _, scheme := range cfg.AllowedSchemes {
		if strings.EqualFold(u.Scheme, scheme) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("URL scheme is not allowed")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("local and internal hostnames are not allowed")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("hostname cannot be resolved")
	}
	for _, addr := range ips {
		if !publicIP(addr.IP) {
			return fmt.Errorf("hostname resolves to a non-public address")
		}
	}
	return nil
}

// FetchRemoteImage retrieves a bounded image over validated HTTPS. Redirects
// and the final dial target are independently checked against SSRF targets.
func FetchRemoteImage(ctx context.Context, rawURL string, cfg FetchRemoteImageConfig) ([]byte, string, error) {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 10 << 20
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if len(cfg.AllowedTypes) == 0 {
		cfg.AllowedTypes = DefaultFetchRemoteImageConfig().AllowedTypes
	}
	if len(cfg.AllowedSchemes) == 0 {
		cfg.AllowedSchemes = []string{"https"}
	}
	if err := ValidateRemoteURL(rawURL, cfg); err != nil {
		return nil, "", fmt.Errorf("URL validation failed: %w", err)
	}
	dialer := &net.Dialer{Timeout: cfg.Timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, fmt.Errorf("hostname cannot be resolved")
			}
			for _, addr := range ips {
				if !publicIP(addr.IP) {
					return nil, fmt.Errorf("hostname resolves to a non-public address")
				}
			}
			var lastErr error
			for _, addr := range ips {
				ip := addr.IP.String()
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: cfg.Timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return ValidateRemoteURL(req.URL.String(), cfg)
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "shortner/branding-image")
	req.Header.Set("Accept", strings.Join(cfg.AllowedTypes, ", "))
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetching URL: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !mimeAllowed(contentType, cfg.AllowedTypes) {
		return nil, "", fmt.Errorf("content type is not allowed")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading response: %w", err)
	}
	if int64(len(body)) > cfg.MaxSize {
		return nil, "", fmt.Errorf("image exceeds size limit")
	}
	return body, contentType, nil
}

func mimeAllowed(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}

func publicIP(raw net.IP) bool {
	ip, err := netip.ParseAddr(raw.String())
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip.Is4() {
		for _, prefix := range nonPublicIPv4Prefixes {
			if prefix.Contains(ip) {
				return false
			}
		}
		return true
	}
	if !ip.Is6() || !publicIPv6Prefix.Contains(ip) {
		return false
	}
	for _, prefix := range nonPublicIPv6Prefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicIPv4Prefixes = mustPrefixes(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
	"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"240.0.0.0/4",
)

var publicIPv6Prefix = netip.MustParsePrefix("2000::/3")
var nonPublicIPv6Prefixes = mustPrefixes("2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20")

func mustPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}
