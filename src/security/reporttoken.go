package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"
)

// reportTokenLength is the number of hex characters kept from the HMAC of
// a security-report tracking id.
const reportTokenLength = 32

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
