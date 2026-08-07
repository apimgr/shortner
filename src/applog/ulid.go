// Package applog implements the multi-file logging system and audit log
// described in AI.md PART 11 "Logging" / "Audit Log": per-log-type output
// formats (apache/nginx/json/logfmt/syslog/fail2ban/cef/text), raw-text-only
// log files, and an append-only, tamper-evident JSON-Lines audit log.
//
// Time-based log rotation/retention scheduling (AI.md PART 11 "Log
// Rotation" / "Audit Log Retention") depends on the scheduler that AI.md
// PART 18 defines and is not built yet; this package appends to open files
// and exposes rotation as a manual, callable operation (see Logger.Rotate)
// rather than a background scheduled job. Tracked in TODO.AI.md.
package applog

import (
	"crypto/rand"
	"strings"
	"time"
)

// crockfordAlphabet is the Crockford Base32 alphabet used by ULID, per
// AI.md PART 11 "Audit Log Format": "Unique audit entry ID (ULID format)".
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GenerateULID returns a 26-character Crockford-Base32 ULID: a 48-bit
// millisecond timestamp followed by 80 bits of cryptographic randomness.
// ULIDs generated in the same millisecond are not guaranteed monotonic —
// audit ordering relies on the "time" field, not lexical ID order.
func GenerateULID() (string, error) {
	var ts [6]byte
	ms := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		ts[i] = byte(ms & 0xFF)
		ms >>= 8
	}

	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}

	var raw [16]byte
	copy(raw[:6], ts[:])
	copy(raw[6:], entropy[:])

	return encodeCrockford(raw), nil
}

// encodeCrockford encodes 16 raw bytes (128 bits) as the 26-character
// Crockford Base32 ULID string.
func encodeCrockford(raw [16]byte) string {
	var b strings.Builder
	b.Grow(26)

	var bits uint64
	var bitCount uint
	idx := 0

	for b.Len() < 26 {
		for bitCount < 5 && idx < len(raw) {
			bits = (bits << 8) | uint64(raw[idx])
			bitCount += 8
			idx++
		}
		if bitCount < 5 {
			bits <<= 5 - bitCount
			bitCount = 5
		}
		shift := bitCount - 5
		symbol := (bits >> shift) & 0x1F
		b.WriteByte(crockfordAlphabet[symbol])
		bitCount -= 5
		bits &= (1 << bitCount) - 1
	}

	return b.String()
}
