package security

import "net"

// AnonymizeIP anonymizes rawIP per IDEA.md "Business rules": "IP addresses
// are anonymized (IPv4: zero last octet; IPv6: zero last 80 bits) before
// any write to the Click table — the raw IP is never persisted." Returns
// "" if rawIP does not parse as a valid IP (caller should then omit the
// field rather than store garbage).
func AnonymizeIP(rawIP string) string {
	ip := net.ParseIP(rawIP)
	if ip == nil {
		return ""
	}

	if v4 := ip.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}

	v6 := ip.To16()
	if v6 == nil {
		return ""
	}
	// 80 bits = 10 bytes; zero the last 10 of the 16 address bytes.
	for i := 6; i < 16; i++ {
		v6[i] = 0
	}
	return v6.String()
}
