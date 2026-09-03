package notify

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"strings"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/fqdn"
)

// detectPorts is AI.md PART 17 "SMTP Auto-Detection": every candidate host
// is tried on 25, 465, and 587.
var detectPorts = []int{25, 465, 587}

// Candidate is one host/port pair the detector will try, carrying the
// priority label from the spec's table so the console line can explain
// where a detected server came from.
type Candidate struct {
	Host string
	Port int
	// Source is the spec's own description ("Loopback (same machine)",
	// "Docker bridge gateway", ...).
	Source string
}

// candidateHost pairs a host with its spec description.
type candidateHost struct {
	host, source string
}

// DetectCandidates builds the AI.md PART 17 "SMTP Auto-Detection" table in
// priority order for the given FQDN. Hosts that cannot be resolved for
// this machine (no default gateway, no global IPv4, no FQDN) are simply
// absent — they are not tried against an empty string.
func DetectCandidates(host string) []Candidate {
	hosts := []candidateHost{
		{"127.0.0.1", "Loopback (same machine)"},
		{"172.17.0.1", "Docker bridge gateway"},
		{defaultGateway(), "Default gateway IP"},
		{host, "Detected FQDN"},
		{fqdn.GlobalIPv4(), "Global IPv4"},
	}
	if host != "" {
		hosts = append(hosts,
			candidateHost{"mail." + host, "Common mail subdomain"},
			candidateHost{"smtp." + host, "Common SMTP subdomain"},
		)
	}

	var out []Candidate
	seen := map[string]bool{}
	for _, h := range hosts {
		if h.host == "" || seen[h.host] {
			continue
		}
		seen[h.host] = true
		for _, port := range detectPorts {
			out = append(out, Candidate{Host: h.host, Port: port, Source: h.source})
		}
	}
	return out
}

// probe tests one host/port. It backs both auto-detection and the
// configured-host startup check, and is a package variable so both can be
// exercised in tests without opening real sockets.
var probe = func(cfg config.SMTP) error { return TestConnection(cfg) }

// Detect walks the candidate list in priority order and returns the first
// host/port that completes an SMTP handshake, per AI.md PART 17
// "Auto-Detection Process". The second return value is false when every
// candidate fails — which the spec is explicit is "not an error, just no
// SMTP available".
//
// Credentials from the existing config are carried into each probe: a
// local relay that requires AUTH must still authenticate for the detected
// entry to be usable.
func Detect(host string, base config.SMTP) (Candidate, bool) {
	for _, c := range DetectCandidates(host) {
		try := base
		try.Host = c.Host
		try.Port = c.Port
		if try.TLS == "" {
			try.TLS = config.SMTPTLSAuto
		}
		if err := probe(try); err == nil {
			return c, true
		}
	}
	return Candidate{}, false
}

// defaultGateway returns the machine's IPv4 default gateway, or "" when it
// cannot be determined. Only Linux exposes this without cgo or shelling
// out; on every other platform the candidate is skipped rather than
// guessed at.
func defaultGateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Skip the column-header line.
	if !scanner.Scan() {
		return ""
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		raw, err := hex.DecodeString(fields[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		// /proc/net/route stores the address little-endian.
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, binary.BigEndian.Uint32(raw))
		if ip.IsUnspecified() {
			continue
		}
		return ip.String()
	}
	return ""
}
