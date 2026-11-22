package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"
)

// reportTokenLength is the number of hex characters kept from the HMAC of
// a security-report tracking id.
const reportTokenLength = 32

// NewReportStatusToken creates a random status token and its SHA-256 digest.
// Only the digest is persisted; the raw value is delivered once by email.
func NewReportStatusToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", errors.New("security: generate report status token")
	}
	token := hex.EncodeToString(raw[:])
	return token, HashReportStatusToken(token), nil
}

// HashReportStatusToken returns the SHA-256 hex digest stored for a report.
func HashReportStatusToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// ValidReportStatusToken compares a candidate status token to its stored
// SHA-256 digest in constant time.
func ValidReportStatusToken(tokenHash, token string) bool {
	if len(tokenHash) != sha256.Size*2 || token == "" {
		return false
	}
	expected, err := hex.DecodeString(tokenHash)
	if err != nil {
		return false
	}
	candidate := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(expected, candidate[:]) == 1
}

// ReportToken derives the one-shot token that authorizes viewing
// `/server/security/report/{tracking_id}`, per AI.md PART 11 "Public
// Pages". It is HMAC-SHA256(installation_secret, "report:"+tracking_id),
// so the server can re-derive it from the tracking id alone and never has
// to store it — the same reasoning AI.md gives for the rotating
// {security_id} being an HMAC rather than a random value.
//
// Unlike {security_id} this token does NOT rotate on a clock: it is bound
// to one report and stays valid for that report's lifetime, because the
// researcher receives it once by email and AI.md scopes its expiry to the
// report being closed for 30 days, not to wall-clock windows.
func ReportToken(installationSecret, trackingID string) string {
	if installationSecret == "" || trackingID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(installationSecret))
	mac.Write([]byte("report:" + trackingID))
	return hex.EncodeToString(mac.Sum(nil))[:reportTokenLength]
}

// ValidReportToken reports whether token authorizes trackingID. The
// comparison is constant-time.
func ValidReportToken(installationSecret, trackingID, token string) bool {
	return validReportTokenBase(installationSecret, trackingID, token)
}

// ValidReportTokenWithPrevious accepts links created before key rotation
// while the previous installation secret remains within its grace period.
func ValidReportTokenWithPrevious(installationSecret, previousSecret, trackingID, token string) bool {
	if validReportTokenBase(installationSecret, trackingID, token) {
		return true
	}
	return previousSecret != "" && validReportTokenBase(previousSecret, trackingID, token)
}

func validReportTokenBase(installationSecret, trackingID, token string) bool {
	expected := ReportToken(installationSecret, trackingID)
	if expected == "" || len(token) != reportTokenLength {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1
}

// ReportTokenGraceDays is how long after a report is closed its status
// token keeps working, per AI.md PART 11: "expires after the report is
// closed for 30 days".
const ReportTokenGraceDays = 30

// ReportTokenExpired reports whether a report closed at closedAt is past
// its 30-day viewing window. A zero closedAt means the report is still
// open, which never expires.
func ReportTokenExpired(closedAt, now time.Time) bool {
	if closedAt.IsZero() {
		return false
	}
	return now.UTC().After(closedAt.UTC().AddDate(0, 0, ReportTokenGraceDays))
}
