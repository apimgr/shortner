// The coordinated-disclosure submission path bolted onto /server/contact,
// per AI.md PART 11 "Security Reports — Coordinated Disclosure Pipeline".
package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/pgp"
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
	CreditHandle     string `json:"credit_handle,omitempty"`
	AppVersion       string `json:"app_version"`
	CommitHash       string `json:"commit_hash"`
	SubmittedAt      string `json:"submitted_at"`
	RequestUA        string `json:"request_user_agent"`
	AgreedDisclosure bool   `json:"agreed_disclosure"`
	SecurityID       string `json:"security_id,omitempty"`
}

// securitySeverities and securityCreditPrefs are the closed vocabularies
// AI.md PART 11 defines for the two required dropdowns.
var (
	securitySeverities  = []string{"Critical", "High", "Medium", "Low", "Informational"}
	securityCreditPrefs = []string{"name", "handle", "no", "anon"}
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
	if security.ValidSecurityIDWithPrevious(fd.installSecret, fd.previousInstallSecret, id, time.Now()) {
		return true
	}
	// The supplied id is logged deliberately: it is already invalid, so it
	// grants nothing, and AI.md requires it for spotting scrape attempts.
	if fd.audit != nil {
		digest := sha256.Sum256([]byte(id))
		ip := ""
		if fd.resolver != nil {
			ip = fd.resolver.ResolveClientIP(r)
		}
		_ = fd.audit.Write(applog.Entry{
			Time:     time.Now().UTC(),
			Event:    "security.security_id_invalid",
			Category: "security",
			Severity: applog.SeverityWarn,
			Actor:    applog.Actor{IP: ip},
			Details:  map[string]any{"supplied_id_hash": hex.EncodeToString(digest[:])[:8], "user_agent": r.UserAgent()},
			Result:   applog.ResultFailure,
		})
	}
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
	r.Body = http.MaxBytesReader(w, r.Body, contactFormBodyLimit)
	base := fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "security_report.title"), fd.cfg.Server.Branding.Description)
	data := securityContactPageData{
		Base:         base,
		SecurityMode: true,
		SecurityID:   r.FormValue("security_id"),
		Severities:   securitySeverities,
		Components:   securityComponents,
		CreditPrefs:  securityCreditPrefs,
	}

	if err := r.ParseForm(); err != nil {
		fd.contactSpamBlock(r, "shape")
		fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.form_read_failed"))
		return
	}
	if !fd.contactOriginValid(r) {
		fd.contactSpamBlock(r, "origin")
		fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, "Please submit the form from this site")
		return
	}
	data.Form = fd.readSecurityForm(r)
	step := r.FormValue("step")
	if step == "" {
		step = "submit"
	}
	if reason, valid := contactShapeValid(r.PostForm, true, false); !valid {
		fd.contactSpamBlock(r, reason)
		fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.form_read_failed"))
		return
	}
	if step == "edit" {
		data.FormToken = fd.contactToken(time.Now())
		captcha, err := fd.issueCaptcha(r)
		if err != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		data.Captcha = captcha
		_ = renderPage(w, http.StatusOK, "contact_security", data)
		return
	}
	now := time.Now()
	decoy := fd.contactAntispam().Honeypot && r.FormValue("website") != ""
	if decoy {
		fd.contactSpamBlock(r, "honeypot")
	}
	if step == "submit" {
		if reason := fd.contactTokenValidationReason(r.FormValue("form_token"), now); reason != "" {
			fd.contactSpamBlock(r, reason)
			fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, t(r, "contact.fill_wait"))
			return
		}
		if !fd.verifyCaptcha(r, r.PostForm) {
			fd.contactSpamBlock(r, "captcha")
			fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.captcha_failed"))
			return
		}
	} else if step != "confirm" {
		fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.form_read_failed"))
		return
	}
	if msg := validateSecurityForm(r, data.Form); msg != "" {
		fd.contactSpamBlock(r, "shape")
		fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, msg)
		return
	}
	if step == "submit" {
		if reason, blocked := fd.contactRateReason(r, data.Form.Email, now); blocked {
			fd.contactSpamBlock(r, reason)
			fd.setContactRetryAfter(w, r, reason)
			fd.renderSecurityContactFailure(w, r, http.StatusTooManyRequests, &data, t(r, "errors.captcha_failed"))
			return
		}
		flags := ""
		if decoy {
			flags = "d"
		}
		data.FormToken = fd.contactToken(now)
		data.Review, data.ReviewToken, data.ConfirmAfter, data.ReviewValues = true, issueContactReviewToken(fd.installSecret, r.PostForm, now, flags), fd.contactAntispam().ReviewMinSeconds, contactReviewValues(r.PostForm)
		_ = renderPage(w, http.StatusOK, "contact_security", data)
		return
	}
	settings := fd.contactAntispam()
	nonce, valid := verifyContactReviewToken(fd.installSecret, r.FormValue("review_token"), r.PostForm, now, settings.ReviewMinSeconds, settings.ReviewMaxSeconds)
	if !valid || r.FormValue("confirmed") != "true" {
		fd.contactSpamBlock(r, "review")
		data.Error = "Please read the details and confirm"
		data.Review, data.ReviewToken, data.ConfirmAfter, data.ReviewValues = true, issueContactReviewToken(fd.installSecret, r.PostForm, now, contactReviewFlags(r.FormValue("review_token"))), settings.ReviewMinSeconds, contactReviewValues(r.PostForm)
		_ = renderPage(w, http.StatusBadRequest, "contact_security", data)
		return
	}
	if !fd.consumeContactNonce(nonce, now) {
		fd.contactSpamBlock(r, "replay")
		data.Error = "Please read the details and confirm"
		data.Review, data.ReviewToken, data.ConfirmAfter, data.ReviewValues = true, issueContactReviewToken(fd.installSecret, r.PostForm, now, contactReviewFlags(r.FormValue("review_token"))), settings.ReviewMinSeconds, contactReviewValues(r.PostForm)
		_ = renderPage(w, http.StatusBadRequest, "contact_security", data)
		return
	}
	if strings.Contains(r.FormValue("review_token"), ".d.") {
		trackingID, err := db.NewTrackingID()
		if err != nil {
			http.Error(w, "unable to complete submission", http.StatusInternalServerError)
			return
		}
		data.Submitted, data.TrackingID = true, trackingID
		_ = renderPage(w, http.StatusOK, "contact_security", data)
		return
	}
	if reason, blocked := fd.contactRateReason(r, data.Form.Email, now); blocked {
		fd.contactSpamBlock(r, reason)
		fd.setContactRetryAfter(w, r, reason)
		fd.renderSecurityContactFailure(w, r, http.StatusTooManyRequests, &data, t(r, "errors.captcha_failed"))
		return
	}
	if reason, blocked := fd.contactRecordAccepted(r, data.Form.Email, "", data.Form.Summary+"\n"+data.Form.Steps+"\n"+data.Form.Impact, now, false); blocked {
		fd.contactSpamBlock(r, reason)
		setContactRetryAfter(w, fd.contactAntispam().Window)
		fd.renderSecurityContactFailure(w, r, http.StatusTooManyRequests, &data, t(r, "errors.captcha_failed"))
		return
	}
	if data.Form.ResearcherGPG != "" {
		key, err := resolveResearcherPublicKey(r.Context(), data.Form.ResearcherGPG)
		if err != nil {
			fd.renderSecurityContactFailure(w, r, http.StatusBadRequest, &data, t(r, "security_report.validate_gpg"))
			return
		}
		data.Form.ResearcherGPG = key
	}

	trackingID, statusToken, err := fd.storeSecurityReport(r.Context(), data.Form)
	if err != nil {
		fd.renderSecurityContactFailure(w, r, http.StatusInternalServerError, &data, t(r, "security_report.store_failed"))
		return
	}

	// Steps 4 and 5: maintainer notification and researcher acknowledgment.
	acknowledged, notified := fd.notifySecurityReport(r, trackingID, statusToken, data.Form)
	data.AcknowledgmentSent = acknowledged

	// Step 7: metadata only — no researcher PII, no vulnerability content.
	if fd.audit != nil {
		_ = fd.audit.Write(applog.Entry{
			Time:     time.Now().UTC(),
			Event:    "security.report_received",
			Category: "security",
			Severity: applog.SeverityWarn,
			Details: map[string]any{
				"tracking_id": trackingID,
				"notified":    notified,
				"severity":    data.Form.Severity,
				"component":   data.Form.Component,
			},
			Result: applog.ResultSuccess,
		})
	}

	data.Submitted = true
	data.TrackingID = trackingID
	_ = renderPage(w, http.StatusOK, "contact_security", data)
}

func (fd *frontendDeps) renderSecurityContactFailure(w http.ResponseWriter, r *http.Request, status int, data *securityContactPageData, message string) {
	data.Error = message
	data.FormToken = fd.contactToken(time.Now())
	challenge, err := fd.issueCaptcha(r)
	if err != nil {
		http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	data.Captcha = challenge
	_ = renderPage(w, status, "contact_security", *data)
}

func (fd *frontendDeps) securityReportAPIPost(w http.ResponseWriter, r *http.Request) {
	writeError := func(status int, message string, refresh bool) {
		payload := map[string]any{"ok": false, "error": message}
		if refresh {
			payload["form_token"] = fd.contactToken(time.Now())
			descriptor, err := fd.issueCaptcha(r)
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "captcha unavailable"})
				return
			}
			payload["captcha"] = descriptor
		}
		writeJSON(w, status, payload)
	}
	step := r.FormValue("step")
	if step == "" {
		step = "submit"
	}
	if reason, valid := contactShapeValid(r.PostForm, true, true); !valid {
		fd.contactSpamBlock(r, reason)
		writeError(http.StatusBadRequest, "invalid form", true)
		return
	}
	if step == "edit" {
		challenge, err := fd.issueCaptcha(r)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "captcha unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"fields": contactReviewValues(r.PostForm), "form_token": fd.contactToken(time.Now()), "captcha": challenge}})
		return
	}
	decoy := fd.contactAntispam().Honeypot && r.FormValue("website") != ""
	if decoy {
		fd.contactSpamBlock(r, "honeypot")
	}
	if step == "submit" {
		if reason := fd.contactTokenValidationReason(r.FormValue("form_token"), time.Now()); reason != "" {
			fd.contactSpamBlock(r, reason)
			writeError(http.StatusBadRequest, t(r, "contact.fill_wait"), true)
			return
		}
		if !fd.verifyCaptcha(r, r.PostForm) {
			fd.contactSpamBlock(r, "captcha")
			writeError(http.StatusBadRequest, "validation failed", true)
			return
		}
	} else if step != "confirm" {
		fd.contactSpamBlock(r, "shape")
		writeError(http.StatusBadRequest, "invalid form", true)
		return
	}
	form := fd.readSecurityForm(r)
	if message := validateSecurityForm(r, form); message != "" {
		writeError(http.StatusBadRequest, message, true)
		return
	}
	now := time.Now()
	if step == "submit" {
		if reason, blocked := fd.contactRateReason(r, form.Email, now); blocked {
			fd.contactSpamBlock(r, reason)
			fd.setContactRetryAfter(w, r, reason)
			writeError(http.StatusTooManyRequests, "rate limited", true)
			return
		}
		flags := ""
		if decoy {
			flags = "d"
		}
		token := issueContactReviewToken(fd.installSecret, r.PostForm, now, flags)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"review": map[string]string{"type": "security", "email": form.Email, "subject": form.Summary}, "review_token": token, "confirm_after_seconds": fd.contactAntispam().ReviewMinSeconds}})
		return
	}
	settings := fd.contactAntispam()
	nonce, valid := verifyContactReviewToken(fd.installSecret, r.FormValue("review_token"), r.PostForm, now, settings.ReviewMinSeconds, settings.ReviewMaxSeconds)
	if !valid || r.FormValue("confirmed") != "true" {
		fd.contactSpamBlock(r, "review")
		writeError(http.StatusBadRequest, "Please read the details and confirm", true)
		return
	}
	if !fd.consumeContactNonce(nonce, now) {
		fd.contactSpamBlock(r, "replay")
		writeError(http.StatusBadRequest, "Please read the details and confirm", true)
		return
	}
	if strings.Contains(r.FormValue("review_token"), ".d.") {
		trackingID, err := db.NewTrackingID()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "unable to complete submission"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"tracking_id": trackingID}})
		return
	}
	if reason, blocked := fd.contactRateReason(r, form.Email, now); blocked {
		fd.contactSpamBlock(r, reason)
		fd.setContactRetryAfter(w, r, reason)
		writeError(http.StatusTooManyRequests, "rate limited", true)
		return
	}
	if reason, blocked := fd.contactRecordAccepted(r, form.Email, "", form.Summary+"\n"+form.Steps+"\n"+form.Impact, now, false); blocked {
		fd.contactSpamBlock(r, reason)
		setContactRetryAfter(w, fd.contactAntispam().Window)
		writeError(http.StatusTooManyRequests, "rate limited", true)
		return
	}
	if form.ResearcherGPG != "" {
		key, err := resolveResearcherPublicKey(r.Context(), form.ResearcherGPG)
		if err != nil {
			writeError(http.StatusBadRequest, t(r, "security_report.validate_gpg"), true)
			return
		}
		form.ResearcherGPG = key
	}
	trackingID, statusToken, err := fd.storeSecurityReport(r.Context(), form)
	if err != nil {
		writeError(http.StatusInternalServerError, "report could not be stored", true)
		return
	}
	_, notified := fd.notifySecurityReport(r, trackingID, statusToken, form)
	if fd.audit != nil {
		_ = fd.audit.Write(applog.Entry{
			Time: time.Now().UTC(), Event: "security.report_received", Category: "security",
			Severity: applog.SeverityWarn,
			Details:  map[string]any{"tracking_id": trackingID, "notified": notified, "severity": form.Severity, "component": form.Component},
			Result:   applog.ResultSuccess,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"tracking_id": trackingID}})
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
		CreditHandle:     field("credit_handle"),
		AppVersion:       fd.version,
		CommitHash:       fd.commitID,
		SubmittedAt:      time.Now().UTC().Format(time.RFC3339),
		RequestUA:        r.UserAgent(),
		AgreedDisclosure: config.IsTruthy(field("agreed_disclosure")),
		SecurityID:       field("security_id"),
	}
}

// validateSecurityForm returns a visitor-facing message for the first
// failed requirement, or "" when the submission is complete. Only the
// fields AI.md marks Required are enforced.
func validateSecurityForm(r *http.Request, f securityReportForm) string {
	switch {
	case f.Name == "" || f.Email == "":
		return t(r, "security_report.validate_name_email")
	case f.Component == "":
		return t(r, "security_report.validate_component")
	case !containsString(securitySeverities, f.Severity):
		return t(r, "security_report.validate_severity")
	case f.Summary == "" || f.Steps == "" || f.Impact == "":
		return t(r, "security_report.validate_summary")
	case !containsString(securityCreditPrefs, f.CreditPref):
		return t(r, "security_report.validate_credit")
	case f.CreditPref == "handle" && f.CreditHandle == "":
		return t(r, "security_report.validate_credit_handle")
	case len([]rune(f.ResearcherGPG)) > 16384:
		return t(r, "security_report.validate_gpg")
	case strings.HasPrefix(f.ResearcherGPG, "https://") && len([]rune(f.ResearcherGPG)) > 2048:
		return t(r, "security_report.validate_gpg")
	case !f.AgreedDisclosure:
		return t(r, "security_report.validate_disclosure")
	}
	return ""
}

func resolveResearcherPublicKey(ctx context.Context, submitted string) (string, error) {
	if strings.HasPrefix(submitted, "https://") {
		return pgp.FetchPublicKey(ctx, submitted)
	}
	if _, err := pgp.ParsePublic(submitted); err != nil {
		return "", err
	}
	return submitted, nil
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

// storeSecurityReport encrypts the report with the project PGP key when
// configured, or seals it under server.security.encryption_key otherwise,
// per AI.md PART 11 "Submission Flow" step 3.
func (fd *frontendDeps) storeSecurityReport(ctx context.Context, f securityReportForm) (string, string, error) {
	unlock, err := pgp.NewStore(fd.configDir).LockShared()
	if err != nil {
		return "", "", err
	}
	defer unlock()

	body, err := json.Marshal(f)
	if err != nil {
		return "", "", err
	}
	var sealed string
	bodyAlg := "pgp"
	if armored, readErr := pgp.NewStore(fd.configDir).ReadPublicKey(); readErr == nil {
		recipients, parseErr := pgp.ParsePublic(armored)
		if parseErr != nil {
			return "", "", parseErr
		}
		sealed, err = pgp.EncryptArmored(recipients, body)
	} else if errors.Is(readErr, pgp.ErrNoKeypair) {
		bodyAlg = "aes-256-gcm"
		key, keyErr := config.DecodeEncryptionKey(fd.cfg)
		if keyErr != nil {
			return "", "", keyErr
		}
		sealed, err = security.Seal(key, body)
	} else {
		return "", "", readErr
	}
	if err != nil {
		return "", "", err
	}
	trackingID, err := db.NewTrackingID()
	if err != nil {
		return "", "", err
	}
	statusToken, tokenHash, err := security.NewReportStatusToken()
	if err != nil {
		return "", "", err
	}
	key, err := config.DecodeEncryptionKey(fd.cfg)
	if err != nil {
		return "", "", err
	}
	contact, err := json.Marshal(struct {
		Email string `json:"email"`
		GPG   string `json:"gpg,omitempty"`
	}{Email: f.Email, GPG: f.ResearcherGPG})
	if err != nil {
		return "", "", err
	}
	contactEnc, err := security.Seal(key, contact)
	if err != nil {
		return "", "", err
	}
	creditName := ""
	switch f.CreditPref {
	case "name":
		creditName = f.Name
	case "handle":
		creditName = f.CreditHandle
	}
	disclosureDate := time.Time{}
	if f.DisclosureDays > 0 {
		disclosureDate = time.Now().UTC().AddDate(0, 0, f.DisclosureDays)
	}
	rep := db.SecurityReport{
		TrackingID:   trackingID,
		SecurityID:   f.SecurityID,
		CreatedAt:    time.Now().UTC(),
		Severity:     strings.ToLower(strings.TrimSpace(f.Severity)),
		Component:    f.Component,
		Sealed:       sealed,
		BodyAlg:      bodyAlg,
		ContactEnc:   []byte(contactEnc),
		CreditPref:   f.CreditPref,
		CreditName:   creditName,
		CVERequested: f.CVERequested,
		TokenHash:    tokenHash,
		DisclosureAt: disclosureDate,
	}
	if err := db.InsertSecurityReport(ctx, fd.ld.sqlDB, rep); err != nil {
		return "", "", err
	}
	return trackingID, statusToken, nil
}
