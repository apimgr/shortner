// OpenPGP key resolution for the coordinated-disclosure pipeline, per
// AI.md PART 11 "Submission Flow" steps 3-5: the project's own key
// encrypts reports at rest and the maintainer notification, and the
// researcher's submitted key encrypts their acknowledgment.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"

	"github.com/apimgr/shortner/src/pgp"
)

// researcherKeyFetchTimeout and researcherKeyMaxBytes bound the fetch of a
// researcher-supplied key URL. The URL is attacker-controlled, so the
// request is capped in both time and size and only https is followed.
const (
	researcherKeyFetchTimeout = 10 * time.Second
	researcherKeyMaxBytes     = 1 << 18
)

// errResearcherKeyScheme is returned when a submitted key URL is not https.
var errResearcherKeyScheme = errors.New("researcher key URL must use https")

// errResearcherKeyPrivate is returned when a submitted key URL resolves to
// an address that is not routable on the public internet. The URL comes
// from an unauthenticated form, so following it to an internal address
// would turn the disclosure form into an SSRF probe of the operator's
// network — exactly the Tier 1 exposure AI.md PART 11 forbids.
var errResearcherKeyPrivate = errors.New("researcher key URL must resolve to a public address")

// isPublicIP reports whether ip is a globally routable unicast address.
// Loopback, RFC 1918 / RFC 4193 private ranges, link-local (169.254.0.0/16
// and fe80::/10, which covers the cloud metadata endpoint), the
// unspecified address, multicast, and broadcast are all rejected.
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if ip.IsMulticast() || !ip.IsGlobalUnicast() {
		return false
	}
	// 100.64.0.0/10 (RFC 6598 carrier-grade NAT) is global unicast by the
	// stdlib's definition but is not reachable across the public internet.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

// hostResolvesPublic reports whether every address host resolves to is
// public. A literal IP is checked directly; a name is resolved first.
func hostResolvesPublic(host string) error {
	if host == "" {
		return errResearcherKeyScheme
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return errResearcherKeyPrivate
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return errResearcherKeyPrivate
	}
	for _, ip := range addrs {
		if !isPublicIP(ip) {
			return errResearcherKeyPrivate
		}
	}
	return nil
}

// researcherKeyDial re-validates the address the resolver actually handed
// the transport, immediately before connecting. The pre-flight
// hostResolvesPublic check alone is not enough: a DNS-rebinding attacker
// can answer the pre-flight lookup with a public address and the
// connect-time lookup with an internal one.
func researcherKeyDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: researcherKeyFetchTimeout}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, errResearcherKeyPrivate
		}
	}
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errResearcherKeyPrivate
}

// projectPGPKey returns the project's public key as a recipient list, or
// pgp.ErrNoKeypair when no keypair has been generated.
func (fd *frontendDeps) projectPGPKey() (openpgp.EntityList, error) {
	armored, err := pgp.NewStore(fd.configDir).ReadPublicKey()
	if err != nil {
		return nil, err
	}
	return pgp.ParsePublic(armored)
}

// researcherPGPKey resolves the researcher's optional submitted key. AI.md
// PART 11 accepts either a pasted ASCII-armored block or an https URL
// (including a keyserver lookup URL). An unusable value is not an error
// the researcher ever sees — the acknowledgment simply goes out in plain
// text, which carries no vulnerability content anyway.
func (fd *frontendDeps) researcherPGPKey(supplied string) (openpgp.EntityList, error) {
	value := strings.TrimSpace(supplied)
	if value == "" {
		return nil, pgp.ErrNoKeypair
	}
	if strings.HasPrefix(value, "-----BEGIN ") {
		return pgp.ParsePublic(value)
	}
	body, err := fetchResearcherKey(value)
	if err != nil {
		return nil, err
	}
	return pgp.ParsePublic(body)
}

// fetchResearcherKey downloads an armored public key from an https URL.
func fetchResearcherKey(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return "", errResearcherKeyScheme
	}
	if err := hostResolvesPublic(parsed.Hostname()); err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: researcherKeyFetchTimeout,
		Transport: &http.Transport{
			DialContext:           researcherKeyDial,
			TLSHandshakeTimeout:   researcherKeyFetchTimeout,
			ResponseHeaderTimeout: researcherKeyFetchTimeout,
		},
		// Every hop must stay on https and on a public address: a redirect
		// to http would silently downgrade a fetch driven by an
		// attacker-supplied URL, and a redirect to an internal address
		// would make the form an SSRF probe.
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errResearcherKeyScheme
			}
			return hostResolvesPublic(req.URL.Hostname())
		},
	}

	resp, err := client.Get(parsed.String())
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("researcher key URL returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, researcherKeyMaxBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
