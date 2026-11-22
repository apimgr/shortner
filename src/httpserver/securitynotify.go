// AI.md PART 11 "Submission Flow" steps 4 and 5 — the two emails that
// accompany a stored security report.
package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/pgp"
	"github.com/apimgr/shortner/src/security"
)

type securityReportSender interface {
	Enabled() bool
	SendRaw(to []string, subject, body string) error
	SendRawWithAttachments(to []string, subject, body string, attachments []notify.Attachment) error
}

// notifySecurityReport sends AI.md PART 11 "Submission Flow" step 4 (the
// maintainer notification) and step 5 (the researcher acknowledgment).
//
// Both are best-effort: the report is already sealed in the database
// before this runs, so a dead SMTP server must never turn a successful
// submission into a visitor-facing error. When no SMTP works at all,
// notify.Notifier refuses the send outright and nothing is queued — AI.md
// PART 17 "SMTP Requirement".
func (fd *frontendDeps) notifySecurityReport(r *http.Request, trackingID, statusToken string, f securityReportForm) bool {
	return fd.notifySecurityReportWith(fd.notifier, r, trackingID, statusToken, f)
}

func (fd *frontendDeps) notifySecurityReportWith(sender securityReportSender, r *http.Request, trackingID, statusToken string, f securityReportForm) bool {
	if sender == nil || !sender.Enabled() {
		return false
	}
	fd.notifyMaintainerSecurityReport(sender, trackingID, f)
	return fd.notifyResearcherSecurityReport(sender, r, trackingID, statusToken, f)
}

// notifyMaintainerSecurityReport implements step 4. AI.md requires a
// PGP-encrypted email to the `security` contact role, "falls back to
// AES-encrypted attachment + warning if no PGP key is configured. The
// vulnerability text is never included in the plaintext body.
func (fd *frontendDeps) notifyMaintainerSecurityReport(sender securityReportSender, trackingID string, f securityReportForm) {
	to := strings.TrimSpace(fd.cfg.Server.Contact.Security.Email)
	if to == "" {
		return
	}
	if armored, err := pgp.NewStore(fd.configDir).ReadPublicKey(); err == nil {
		recipients, err := pgp.ParsePublic(armored)
		if err != nil {
			return
		}
		plaintext, err := json.Marshal(f)
		if err != nil {
			return
		}
		message, err := pgp.EncryptArmored(recipients, plaintext)
		if err != nil {
			return
		}
		_ = sender.SendRaw([]string{to}, fmt.Sprintf("[SECURITY] %s report %s", f.Severity, trackingID), message)
		return
	}

	sealed, err := fd.sealedReportBytes(f)
	if err != nil {
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
	b.WriteString("report attachment is AES-256-GCM encrypted under\n")
	b.WriteString("server.security.encryption_key instead of being PGP-encrypted to\n")
	b.WriteString("your key. Anyone holding that config key can read it.\n\n")
	b.WriteString("Decrypt the attachment with the same key to read the full report; the\n")
	b.WriteString("plaintext is never written to disk or to any log.\n\n")
	_ = sender.SendRawWithAttachments([]string{to},
		fmt.Sprintf("[SECURITY] %s report %s", f.Severity, trackingID), b.String(), []notify.Attachment{{
			Filename: "security-report-" + trackingID + ".enc", ContentType: "application/octet-stream", Data: sealed,
		}})
}

func (fd *frontendDeps) sealedReportBytes(f securityReportForm) ([]byte, error) {
	key, err := config.DecodeEncryptionKey(fd.cfg)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	sealed, err := security.Seal(key, body)
	if err != nil {
		return nil, err
	}
	return []byte(sealed), nil
}

// notifyResearcherSecurityReport implements step 5: the acknowledgment
// carrying the tracking id and the researcher status-page URL. AI.md
// specifies a PGP-encrypted body when the researcher supplied a public key.
func (fd *frontendDeps) notifyResearcherSecurityReport(sender securityReportSender, r *http.Request, trackingID, statusToken string, f securityReportForm) bool {
	to := strings.TrimSpace(f.Email)
	if to == "" {
		return false
	}
	statusURL := fd.resolver.BuildURL(r, "/server/security/report/"+trackingID) + "?token=" + url.QueryEscape(statusToken)

	body := fmt.Sprintf("Tracking ID: %s\nStatus URL: %s\n", trackingID, statusURL)
	if f.ResearcherGPG != "" {
		recipients, err := pgp.ParsePublic(f.ResearcherGPG)
		if err != nil {
			return false
		}
		message, err := pgp.EncryptArmored(recipients, []byte(body))
		if err != nil {
			return false
		}
		body = message
	}

	return sender.SendRaw([]string{to},
		fmt.Sprintf("Security report received - %s", trackingID), body) == nil
}
