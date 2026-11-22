package pgp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxRemotePublicKeyBytes = 16 * 1024

// FetchPublicKey downloads an HTTPS public key without following redirects,
// using proxy settings, or connecting to a private or local address.
func FetchPublicKey(ctx context.Context, rawURL string) (string, error) {
	u, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("pgp: invalid public key URL")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", errors.New("pgp: invalid public key URL port")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, u.Hostname()) {
				return nil, errors.New("pgp: unexpected public key host")
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("pgp: public key host lookup failed")
			}
			var lastErr error
			for _, candidate := range ips {
				ip := candidate.IP
				if v4 := ip.To4(); v4 != nil {
					ip = v4
				}
				if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
					return nil, errors.New("pgp: public key host resolves to a non-public address")
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, fmt.Errorf("pgp: connect to public key host: %w", lastErr)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", errors.New("pgp: invalid public key URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("pgp: public key download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pgp: public key server returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemotePublicKeyBytes+1))
	if err != nil || len(body) > maxRemotePublicKeyBytes {
		return "", errors.New("pgp: public key response exceeds size limit")
	}
	if _, err := ParsePublic(string(body)); err != nil {
		return "", err
	}
	return string(body), nil
}
