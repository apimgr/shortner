// The coordinated-disclosure submission path bolted onto /server/contact,
// per AI.md PART 11 "Security Reports — Coordinated Disclosure Pipeline".
package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/security"
)

// securityReportForm is the security-mode submission, per AI.md PART 11
// "Security-mode form fields". Everything here except Severity and
// Component is sealed before it touches disk.
type securityReportForm struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	ResearcherGPG    string `json:"researcher_gpg,omitempty"`
	Component        string `json:"component"`
	Endpoint         string `json:"endpoint,omitempty"`
	Severity         string `json:"severity"`
	Summary          string `json:"summary"`
	Steps            string `json:"steps"`
	Impact           string `json:"impact"`
	SuggestedFix     string `json:"suggested_fix,omitempty"`
	CVERequested     bool   `json:"cve_requested"`
	DisclosureDays   int    `json:"disclosure_days"`
	CreditPref       string `json:"credit_preference"`
	AppVersion       string `json:"app_version"`
	CommitHash       string `json:"commit_hash"`
	SubmittedAt      string `json:"submitted_at"`
	RequestUA        string `json:"request_user_agent"`
	AgreedDisclosure bool   `json:"agreed_disclosure"`
	// Captcha is the anti-bot answer required of every contact submission
	// (AI.md PART 11 lists the security fields as additions to "Name +
	// Email + Captcha"). It is never persisted.
	Captcha string `json:"-"`
}

// securitySeverities and securityCreditPrefs are the closed vocabularies
// AI.md PART 11 defines for the two required dropdowns.
var (
	securitySeverities  = []string{"Critical", "High", "Medium", "Low", "Informational"}
	securityCreditPrefs = []string{"Yes (real name)", "Yes (handle)", "No", "Anonymous"}
)

// securityComponents is the "Affected component" dropdown. IDEA.md's
// feature set for this project is link shortening plus its API, web
// frontend, and CLI; "Other" keeps the free-text escape hatch AI.md
// requires.
var securityComponents = []string{"Links / redirects", "Click analytics", "API", "Web frontend", "CLI", "Configuration", "Other"}

// defaultDisclosureDays is AI.md PART 11's default coordinated-disclosure
// window when the researcher expresses no preference.
const defaultDisclosureDays = 90

// securityMode reports whether this request carries a valid
// {security_id}. An absent id is plain standard-contact mode; a present
// but invalid id falls back to standard mode AND is logged as
// security.security_id_invalid, per AI.md PART 11's failure-mode row.
func (fd *frontendDeps) securityMode(r *http.Request) bool {
	id := r.FormValue("security_id")
	if id == "" {
		return false
	}
	if security.ValidSecurityID(fd.installSecret, id, time.Now()) {
		return true
	}
	// The supplied id is logged deliberately: it is already invalid, so it
	// grants nothing, and AI.md requires it for spotting scrape attempts.
	_ = fd.audit.Write(applog.Entry{
		Time:     time.Now().UTC(),
		Event:    "security.security_id_invalid",
		Category: "security",
		Severity: applog.SeverityWarn,
		Actor:    applog.Actor{IP: fd.resolver.ResolveClientIP(r)},
		Details:  map[string]any{"supplied_id": id, "user_agent": r.UserAgent()},
		Result:   applog.ResultFailure,
	})
	return false
}

// securityReportPost handles a security-mode contact submission: it
// re-validates the id server-side, seals the report, allocates a tracking
// id, notifies both parties, and renders the canonical success message —
// AI.md PART 11 "Submission Flow" steps 1 through 7.
//
// Steps 4 and 5 are the email CC path AI.md subordinates to this primary
// channel: they are attempted only when SMTP works (PART 17), and a
// delivery failure never fails the submission — the report is already
// sealed in the database at that point.
func (fd *frontendDeps) securityReportPost(w http.ResponseWriter, r *http.Request) {
	base := fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Report a vulnerability", fd.cfg.Server.SEO.Description)
	data := securityContactPageData{
		Base:         base,
		SecurityMode: true,
		SecurityID:   r.FormValue("security_id"),
		Severities:   securitySeverities,
		Components:   securityComponents,
		CreditPrefs:  securityCreditPrefs,
		Form:         fd.readSecurityForm(r),
	}

	if msg := validateSecurityForm(data.Form); msg != "" {
		data.Error = msg
		_ = renderPage(w, http.StatusBadRequest, "contact_security", data)
		return
	}

	trackingID, err := fd.storeSecurityReport(r.Context(), data.Form)
	if err != nil {
		data.Error = "We could not store your report. Please email the security contact directly."
		_ = renderPage(w, http.StatusInternalServerError, "contact_security", data)
		return
	}

	// Steps 4 and 5: maintainer notification and researcher acknowledgment.
	fd.notifySecurityReport(r, trackingID, data.Form)

	// Step 7: metadata only — no researcher PII, no vulnerability content.
	_ = fd.audit.Write(applog.Entry{
		Time:     time.Now().UTC(),
		Event:    "security.report_received",
		Category: "security",
		Severity: applog.SeverityWarn,
		Details: map[string]any{
			"tracking_id": trackingID,
			"severity":    data.Form.Severity,
			"component":   data.Form.Component,
		},
		Result: applog.ResultSuccess,
	})

	data.Submitted = true
	data.TrackingID = trackingID
	_ = renderPage(w, http.StatusOK, "contact_security", data)
}

// readSecurityForm extracts and trims every security-mode field,
// including the hidden auto-filled triage values.
func (fd *frontendDeps) readSecurityForm(r *http.Request) securityReportForm {
	field := func(name string) string { return strings.TrimSpace(r.FormValue(name)) }

	days, err := strconv.Atoi(field("disclosure_days"))
	if err != nil || days <= 0 {
		days = defaultDisclosureDays
	}

	component := field("component")
	if component == "Other" {
		if other := field("component_other"); other != "" {
			component = other
		}
	}

	return securityReportForm{
		Name:             field("name"),
		Email:            field("email"),
		ResearcherGPG:    field("researcher_gpg"),
		Component:        component,
		Endpoint:         field("endpoint"),
		Severity:         field("severity"),
		Summary:          field("summary"),
		Steps:            field("steps"),
		Impact:           field("impact"),
		SuggestedFix:     field("suggested_fix"),
		CVERequested:     config.IsTruthy(field("cve_requested")),
		DisclosureDays:   days,
		CreditPref:       field("credit_preference"),
		AppVersion:       fd.version,
		CommitHash:       fd.commitID,
		SubmittedAt:      time.Now().UTC().Format(time.RFC3339),
		RequestUA:        r.UserAgent(),
		AgreedDisclosure: config.IsTruthy(field("agreed_disclosure")),
		Captcha:          field("captcha"),
	}
}

// validateSecurityForm returns a visitor-facing message for the first
// failed requirement, or "" when the submission is complete. Only the
// fields AI.md marks Required are enforced.
func validateSecurityForm(f securityReportForm) string {
	switch {
	case f.Name == "" || f.Email == "":
		return "Name and email are required so we can acknowledge your report."
	case f.Component == "":
		return "Please pick the affected component."
	case !containsString(securitySeverities, f.Severity):
		return "Please pick a severity."
	case f.Summary == "" || f.Steps == "" || f.Impact == "":
		return "Summary, steps to reproduce, and impact are all required."
	case !containsString(securityCreditPrefs, f.CreditPref):
		return "Please choose how you would like to be credited."
	case !f.AgreedDisclosure:
		return "Please agree to coordinated disclosure before submitting."
	case f.Captcha != contactCaptchaAnswer:
		return "That doesn't look right — try the math question again."
	}
	return ""
}

// containsString reports whether want is one of the allowed values.
func containsString(allowed []string, want string) bool {
	for _, v := range allowed {
		if v == want {
			return true
		}
	}
	return false
}

// storeSecurityReport seals the report under server.security.encryption_key
// and inserts it, returning the allocated tracking id. Plaintext is never
// persisted, per AI.md PART 11 "Submission Flow" step 3. The project has
// no PGP keypair path yet (see TODO.AI.md), so the AES-256-GCM branch the
// same step defines is the one taken.
func (fd *frontendDeps) storeSecurityReport(ctx context.Context, f securityReportForm) (string, error) {
	key, err := config.DecodeEncryptionKey(fd.cfg)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	sealed, err := security.Seal(key, body)
	if err != nil {
		return "", err
	}
	trackingID, err := db.NewTrackingID()
	if err != nil {
		return "", err
	}
	rep := db.SecurityReport{
		TrackingID: trackingID,
		ReceivedAt: time.Now().UTC(),
		Severity:   f.Severity,
		Component:  f.Component,
		Sealed:     sealed,
	}
	if err := db.InsertSecurityReport(ctx, fd.ld.sqlDB, rep); err != nil {
		return "", err
	}
	return trackingID, nil
}
