// The rotating {security_id} token used by security.txt's contact-form
// URL, per AI.md PART 11 "Security Reports" -> "`{security_id}` —
// Rotating One-Shot Token".
package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"time"
)

// securityIDWindow is the rotation period: 48 hours, per AI.md PART 11.
const securityIDWindow int64 = 172800

// securityIDLength is the number of hex characters kept from the HMAC.
const securityIDLength = 16

// SecurityID derives the id for the 48-hour window containing at, per
// AI.md PART 11: HMAC-SHA256(installation_secret, floor(unix/172800)),
// hex-encoded, first 16 chars. An empty secret yields an empty id so a
// server that has not generated its installation_secret yet simply omits
// the Contact line rather than publishing a forgeable value.
func SecurityID(installationSecret string, at time.Time) string {
	return securityIDForWindow(installationSecret, at.Unix()/securityIDWindow)
}

// securityIDForWindow computes the id for an explicit window counter.
func securityIDForWindow(installationSecret string, window int64) string {
	if installationSecret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(installationSecret))
	mac.Write([]byte(strconv.FormatInt(window, 10)))
	full := hex.EncodeToString(mac.Sum(nil))
	return full[:securityIDLength]
}

// ValidSecurityID reports whether id matches the current OR the previous
// 48-hour window, per AI.md PART 11's validation-window rule — a
// researcher who loaded security.txt at second 47:59:59 must not be
// rejected. Comparison is constant-time.
func ValidSecurityID(installationSecret, id string, at time.Time) bool {
	if installationSecret == "" || len(id) != securityIDLength {
		return false
	}
	window := at.Unix() / securityIDWindow
	for _, candidate := range []string{
		securityIDForWindow(installationSecret, window),
		securityIDForWindow(installationSecret, window-1),
	} {
		if candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(id)) == 1 {
			return true
		}
	}
	return false
}
