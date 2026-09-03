// AI.md PART 11 "Submission Flow" steps 4 and 5 — the two emails that
// accompany a stored security report.
package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/security"
)

// notifySecurityReport sends AI.md PART 11 "Submission Flow" step 4 (the
// maintainer notification) and step 5 (the researcher acknowledgment).
//
// Both are best-effort: the report is already sealed in the database
// before this runs, so a dead SMTP server must never turn a successful
// submission into a visitor-facing error. When no SMTP works at all,
// notify.Notifier refuses the send outright and nothing is queued — AI.md
// PART 17 "SMTP Requirement".
func (fd *frontendDeps) notifySecurityReport(r *http.Request, trackingID string, f securityReportForm) {
	if !fd.notifier.Enabled() {
		return
	}
	fd.notifyMaintainerSecurityReport(trackingID, f)
	fd.notifyResearcherSecurityReport(r, trackingID, f)
}

// notifyMaintainerSecurityReport implements step 4. AI.md requires a
// PGP-encrypted email to the `security` contact role, "falls back to
// AES-encrypted attachment + warning if no PGP key is configured" — and
// this project has no PGP keypair path yet, so the fallback is what is
// sent: the same AES-256-GCM sealed blob stored in the database, armored
// inline, plus the mandated warning. The vulnerability text itself is
// never placed in the plaintext body.
func (fd *frontendDeps) notifyMaintainerSecurityReport(trackingID string, f securityReportForm) {
	to := strings.TrimSpace(fd.cfg.Server.Contact.Security.Email)
	if to == "" {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "A new security report was received.\n\n")
	fmt.Fprintf(&b, "Tracking ID: %s\n", trackingID)
	fmt.Fprintf(&b, "Severity: %s\n", f.Severity)
	fmt.Fprintf(&b, "Component: %s\n", f.Component)
	fmt.Fprintf(&b, "Received: %s\n", f.SubmittedAt)
	fmt.Fprintf(&b, "Disclosure window: %d days\n\n", f.DisclosureDays)
	b.WriteString("WARNING: no PGP keypair is configured for this server, so the\n")
	b.WriteString("report body below is AES-256-GCM encrypted under\n")
	b.WriteString("server.security.encryption_key instead of being PGP-encrypted to\n")
	b.WriteString("your key. Anyone holding that config key can read it.\n\n")
	b.WriteString("Decrypt it with the same key to read the full report; the\n")
	b.WriteString("plaintext is never written to disk or to any log.\n\n")
	b.WriteString("----- BEGIN AES-256-GCM SEALED REPORT -----\n")
	b.WriteString(fd.sealedReportArmor(f))
	b.WriteString("\n----- END AES-256-GCM SEALED REPORT -----\n")

	_ = fd.notifier.SendRaw([]string{to},
		fmt.Sprintf("[SECURITY] %s report %s", f.Severity, trackingID), b.String())
}

// sealedReportArmor re-seals the report for transport and wraps the
// base64 at 64 columns so mail clients do not mangle it. A sealing
// failure yields an explicit marker rather than leaking plaintext.
func (fd *frontendDeps) sealedReportArmor(f securityReportForm) string {
	key, err := config.DecodeEncryptionKey(fd.cfg)
	if err != nil {
		return "(unavailable: encryption key could not be loaded)"
	}
	body, err := json.Marshal(f)
	if err != nil {
		return "(unavailable: report could not be serialized)"
	}
	sealed, err := security.Seal(key, body)
	if err != nil {
		return "(unavailable: report could not be sealed)"
	}
	return wrapColumns(base64.StdEncoding.EncodeToString([]byte(sealed)), 64)
}

// wrapColumns breaks s into lines of at most width characters.
func wrapColumns(s string, width int) string {
	var b strings.Builder
	for len(s) > width {
		b.WriteString(s[:width])
		b.WriteByte('\n')
		s = s[width:]
	}
	b.WriteString(s)
	return b.String()
}

// notifyResearcherSecurityReport implements step 5: the acknowledgment
// carrying the tracking id and the researcher status-page URL. AI.md
// specifies a PGP-encrypted body using the researcher's submitted pubkey;
// with no PGP path in this project the acknowledgment is sent in plain
// text and deliberately contains no vulnerability content — only the
// tracking id the researcher already knows from the success page.
func (fd *frontendDeps) notifyResearcherSecurityReport(r *http.Request, trackingID string, f securityReportForm) {
	to := strings.TrimSpace(f.Email)
	if to == "" {
		return
	}
	statusURL := fd.resolver.BuildURL(r, "/server/security/report/"+trackingID)

	var b strings.Builder
	fmt.Fprintf(&b, "Thank you for reporting a security issue to %s.\n\n", projectInfo.Name)
	fmt.Fprintf(&b, "Your report has been received and encrypted at rest.\n\n")
	fmt.Fprintf(&b, "Tracking ID: %s\n", trackingID)
	fmt.Fprintf(&b, "Status page: %s\n", statusURL)
	fmt.Fprintf(&b, "Coordinated disclosure window: %d days\n\n", f.DisclosureDays)
	b.WriteString("Keep the tracking ID — it is the only reference to your report.\n")
	b.WriteString("Please do not disclose this issue publicly before the coordinated\n")
	b.WriteString("date you agreed to when submitting.\n\n")
	if f.ResearcherGPG == "" {
		b.WriteString("You did not supply a PGP public key, so this acknowledgment is\n")
		b.WriteString("unencrypted. It deliberately contains no details of your report.\n")
	} else {
		b.WriteString("This server has no PGP keypair configured, so it could not encrypt\n")
		b.WriteString("this acknowledgment to the key you supplied. It deliberately\n")
		b.WriteString("contains no details of your report.\n")
	}

	_ = fd.notifier.SendRaw([]string{to},
		fmt.Sprintf("Security report received - %s", trackingID), b.String())
}
