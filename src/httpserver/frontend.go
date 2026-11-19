// The AI.md PART 16 "Web Frontend" page handlers: the home page (HTML
// link-creation form, reusing the same business logic as the JSON
// POST /api/{api_version}/links in links.go rather than duplicating it),
// the standard /server/{about,privacy,contact,help,terms} pages, the
// cookie-consent banner endpoint, the CCPA opt-out page, the public
// paginated /list page, and the HTML variants of /server/healthz and
// /{slug}/stats (both of which fall back to their existing JSON/text
// handlers for non-browser clients via detectClientType).
//
// Nav-item decision: IDEA.md "Frontend design reference" describes a nav
// with Home/List/Domains/About, "adapted to this project's actual routes
// and reserved names". Since every link is public (no accounts, no
// per-owner scoping), "List" is a site-wide listing of all created links —
// see listHTMLHandler and partial/public/nav.tmpl. "Domains" stays out of
// the nav: there is no per-tenant custom-domain feature, and none is
// planned (out of IDEA.md's business-logic scope), though
// src/security/slug.go's reservedSlugs still reserves the name.
package httpserver

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/common/sanitize"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/security"
)

// registerFrontendRoutes mounts every PART 16 page route on r. It must be
// called after the /api and /server/healthz routes (so those keep their
// existing JSON/text-first handlers) but the exact position doesn't matter
// for the /server/* and /{slug} routes below — chi's radix tree always
// prefers a more specific static route over the /{slug} wildcard,
// regardless of registration order.
func (fd *frontendDeps) registerFrontendRoutes(r chi.Router, hd *healthDeps, ld *linkDeps) {
	r.Get("/", fd.homeHandler)
	r.Post("/", fd.homeHandler)

	r.Get("/server", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/server/about", http.StatusMovedPermanently)
	})
	r.Get("/server/about", fd.aboutHandler)
	r.Get("/server/privacy", fd.privacyHandler)
	r.Get("/server/help", fd.helpHandler)
	r.Get("/server/docs/swagger", fd.swaggerPage)
	r.Get("/server/docs/graphql", fd.graphqlPage)
	r.Get("/server/terms", fd.termsHandler)
	r.Get("/server/contact", fd.contactHandler)
	r.Post("/server/contact", fd.contactHandler)
	r.Post("/server/consent", fd.consentHandler)
	r.Get("/server/ccpa", fd.ccpaHandler)
	r.Get("/server/security", fd.securityHandler)
	r.Get("/server/security/policy", fd.securityPolicyHandler)
	r.Get("/server/security/thanks", fd.securityThanksHandler)
	r.Get("/server/security/report/{tracking_id}", fd.securityReportStatusHandler)
	r.Get("/server/dpo", fd.dpoHandler)
	r.Post("/server/theme", fd.themeHandler)
	r.Post("/announcements/dismiss", fd.dismissAnnouncementHandler)

	r.Get("/server/healthz", fd.healthzHTMLHandler(hd))
	r.Get("/list", fd.listHTMLHandler(ld))
	r.Get("/{slug}", ld.resolveHandler)
	r.Get("/{slug}/stats", fd.statsHTMLHandler(ld))
}

func (fd *frontendDeps) dismissAnnouncementHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.FormValue("id")
	valid := false
	for _, item := range fd.cfg.Web.Announcements.Messages {
		if validAnnouncementID(id) && item.ID == id && item.Dismissible {
			valid = true
			break
		}
	}
	if !fd.cfg.Web.Announcements.Enabled || !valid {
		http.Error(w, "announcement not found", http.StatusNotFound)
		return
	}
	ids := []string{}
	if cookie, err := r.Cookie("dismissed_announcements"); err == nil {
		value, decodeErr := url.PathUnescape(cookie.Value)
		if decodeErr != nil {
			value = cookie.Value
		}
		ids = strings.Split(value, ",")
	}
	for _, existing := range ids {
		if existing == id {
			fd.redirectAfterAnnouncementDismiss(w, r)
			return
		}
	}
	ids = append(ids, id)
	http.SetCookie(w, &http.Cookie{
		Name: "dismissed_announcements", Value: strings.Join(ids, ","), Path: "/",
		MaxAge: 365 * 24 * 60 * 60, Secure: requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	fd.redirectAfterAnnouncementDismiss(w, r)
}

func parseContactAPIForm(r *http.Request) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return r.ParseForm()
	}
	var fields map[string]json.RawMessage
	body, err := io.ReadAll(io.LimitReader(r.Body, contactFormBodyLimit+1))
	if err != nil {
		return err
	}
	if len(body) > contactFormBodyLimit {
		return fmt.Errorf("contact form exceeds body limit")
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	values := make(url.Values, len(fields))
	for key, raw := range fields {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("contact field %q must be a string", key)
		}
		values.Set(key, value)
	}
	r.PostForm = values
	r.Form = values
	return nil
}

func (fd *frontendDeps) redirectAfterAnnouncementDismiss(w http.ResponseWriter, r *http.Request) {
	returnTo := r.FormValue("return_to")
	parsed, err := url.ParseRequestURI(returnTo)
	if err != nil || !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") || parsed.IsAbs() || parsed.Host != "" {
		returnTo = "/"
	}
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

// homeLinkResult is the just-created link's data shown on the home page's
// success state, per home.tmpl.
type homeLinkResult struct {
	ShortURL   string
	OwnerToken string
}

// homePageData is the data bound to page/home.tmpl.
type homePageData struct {
	Base        PageData
	Link        *homeLinkResult
	Error       string
	Destination string
	CustomSlug  string
}

func (fd *frontendDeps) homeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		fd.homePost(w, r)
		return
	}
	data := homePageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "home.title"), fd.cfg.Server.SEO.Description)}
	_ = renderPage(w, http.StatusOK, "home", data)
}

// homePost handles the server-rendered POST / form submission, reusing the
// same validation and owner-scoped db.CreateLink* calls as
// createLinkHandler in links.go (PART 14) instead of duplicating that
// business logic.
func (fd *frontendDeps) homePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fd.homeError(w, r, t(r, "errors.form_read_failed"), "", "", http.StatusBadRequest)
		return
	}

	destInput := r.FormValue("url")
	slugInput := strings.TrimSpace(r.FormValue("slug"))

	destination, ok := validateDestinationURL(destInput)
	if !ok {
		fd.homeError(w, r, t(r, "errors.invalid_url"), destInput, slugInput, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	ownerID, ownerToken, err := fd.ld.ownerForCreate(r, true)
	if err != nil {
		fd.homeError(w, r, t(r, "errors.link_create_failed"), destInput, slugInput, http.StatusInternalServerError)
		return
	}
	var link *db.Link

	if slugInput != "" {
		if !security.ValidateSlugFormat(slugInput) {
			fd.homeError(w, r, t(r, "errors.slug_format"), destInput, slugInput, http.StatusBadRequest)
			return
		}
		if security.IsReservedSlug(slugInput) {
			fd.homeError(w, r, t(r, "errors.slug_reserved"), destInput, slugInput, http.StatusConflict)
			return
		}
		link, err = db.CreateLinkCustomSlugOwned(ctx, fd.ld.sqlDB, ownerID, slugInput, destination, nil)
		if errors.Is(err, db.ErrSlugTaken) {
			fd.homeError(w, r, t(r, "errors.slug_in_use"), destInput, slugInput, http.StatusConflict)
			return
		}
	} else {
		link, err = db.CreateLinkAutoCodeOwned(ctx, fd.ld.sqlDB, ownerID, destination, nil)
	}
	if err != nil {
		fd.homeError(w, r, t(r, "errors.link_create_failed"), destInput, slugInput, http.StatusInternalServerError)
		return
	}

	if ownerToken != "" {
		setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, ownerToken)
	} else if cookie, err := r.Cookie(ownerTokenCookieName); err == nil {
		setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, cookie.Value)
	}

	shortURL := fd.ld.resolver.BuildURL(r, "/"+link.ShortCode)
	data := homePageData{
		Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "home.title"), fd.cfg.Server.SEO.Description),
		Link: &homeLinkResult{ShortURL: shortURL, OwnerToken: ownerToken},
	}
	_ = renderPage(w, http.StatusCreated, "home", data)
}

func (fd *frontendDeps) homeError(w http.ResponseWriter, r *http.Request, msg, dest, slug string, status int) {
	data := homePageData{
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "home.title"), fd.cfg.Server.SEO.Description),
		Error:       msg,
		Destination: dest,
		CustomSlug:  slug,
	}
	_ = renderPage(w, status, "home", data)
}

// aboutPageData is the data bound to page/about.tmpl. AboutContent is an
// optional operator-configured extra block (pages.about.content),
// sanitized via the same formatting-only allow-list as the footer, since
// it comes from the same trust level (operator config file).
type aboutPageData struct {
	Base         PageData
	AboutContent template.HTML
}

func (fd *frontendDeps) aboutHandler(w http.ResponseWriter, r *http.Request) {
	data := aboutPageData{
		Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "about.title"), fd.cfg.Server.SEO.Description),
		AboutContent: sanitize.RenderMarkdown(fd.cfg.Pages.About.Content, r.Host),
	}
	_ = renderPage(w, http.StatusOK, "about", data)
}

// helpPageData is the data bound to page/help.tmpl.
type helpPageData struct {
	Base        PageData
	HelpContent template.HTML
}

func (fd *frontendDeps) helpHandler(w http.ResponseWriter, r *http.Request) {
	data := helpPageData{
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "help.title"), fd.cfg.Server.SEO.Description),
		HelpContent: sanitize.RenderMarkdown(fd.cfg.Pages.Help.Content, r.Host),
	}
	_ = renderPage(w, http.StatusOK, "help", data)
}

// termsPageData is the data bound to page/terms.tmpl.
type termsPageData struct {
	Base    PageData
	Content template.HTML
}

func (fd *frontendDeps) termsHandler(w http.ResponseWriter, r *http.Request) {
	custom := fd.cfg.Pages.Terms.Content
	var content template.HTML
	if custom != "" {
		content = sanitize.RenderMarkdown(custom, r.Host)
	}
	data := termsPageData{
		Base:    fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "terms.title"), fd.cfg.Server.SEO.Description),
		Content: content,
	}
	_ = renderPage(w, http.StatusOK, "terms", data)
}

// privacyPageData is the data bound to page/privacy.tmpl, sourced from
// server.privacy in server.yml (auto-generated privacy page, per AI.md
// PART 16 "/server/privacy").
type privacyPageData struct {
	Base    PageData
	Content template.HTML

	DataCollection template.HTML
	DataUsage      template.HTML
	DataSecurity   template.HTML

	CookieEssentialEnabled       bool
	CookieEssentialDescription   string
	CookiePreferencesEnabled     bool
	CookiePreferencesDescription string
	CookieAnalyticsEnabled       bool
	CookieAnalyticsDescription   string

	Sold               bool
	ThirdPartyServices []config.PrivacyThirdPartyService
	RetentionPeriod    string
}

func (fd *frontendDeps) privacyHandler(w http.ResponseWriter, r *http.Request) {
	priv := fd.cfg.Server.Privacy
	custom := fd.cfg.Pages.Privacy.Content
	var content template.HTML
	if custom != "" {
		content = sanitize.RenderMarkdown(custom, r.Host)
	}

	data := privacyPageData{
		Base:    fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "privacy.title"), fd.cfg.Server.SEO.Description),
		Content: content,

		DataCollection: sanitize.RenderMarkdown(priv.Content.DataCollection, r.Host),
		DataUsage:      sanitize.RenderMarkdown(priv.GetDataUsageContent(), r.Host),
		DataSecurity:   sanitize.RenderMarkdown(priv.Content.DataSecurity, r.Host),

		CookieEssentialEnabled:       priv.Cookies.Essential.Enabled,
		CookieEssentialDescription:   priv.Cookies.Essential.Description,
		CookiePreferencesEnabled:     priv.Cookies.Preferences.Enabled,
		CookiePreferencesDescription: priv.Cookies.Preferences.Description,
		CookieAnalyticsEnabled:       priv.Cookies.Analytics.Enabled,
		CookieAnalyticsDescription:   priv.Cookies.Analytics.Description,

		Sold:               priv.Data.Sold,
		ThirdPartyServices: priv.ThirdParty.Services,
		RetentionPeriod:    priv.Retention.Period,
	}
	_ = renderPage(w, http.StatusOK, "privacy", data)
}

// contactPageData is the data bound to page/contact.tmpl.
type contactPageData struct {
	Base           PageData
	Enabled        bool
	Submitted      bool
	SuccessMessage string
	Error          string
	Name           string
	Email          string
	Subject        string
	Message        string
	AbuseEmail     string
	FormToken      string
	Captcha        captchaDescriptor
}

// securityContactPageData is the data bound to page/contact_security.tmpl
// — the security-report mode of /server/contact, per AI.md PART 11
// "`/server/contact?security_id={id}` — Mode Switch".
type securityContactPageData struct {
	Base         PageData
	SecurityMode bool
	SecurityID   string
	FormToken    string
	Captcha      captchaDescriptor
	Severities   []string
	Components   []string
	CreditPrefs  []string
	Form         securityReportForm
	Error        string
	Submitted    bool
	TrackingID   string
}

func (fd *frontendDeps) contactHandler(w http.ResponseWriter, r *http.Request) {
	// AI.md PART 11: a valid {security_id} switches the form into security
	// mode; an absent or invalid one falls through to standard contact.
	if fd.cfg.Pages.Contact.Enabled && fd.securityMode(r) {
		if r.Method == http.MethodPost {
			fd.securityReportPost(w, r)
			return
		}
		captcha, captchaErr := fd.issueCaptcha(r)
		data := securityContactPageData{
			Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "security_report.title"), fd.cfg.Server.SEO.Description),
			SecurityMode: true,
			SecurityID:   r.FormValue("security_id"),
			FormToken:    fd.contactToken(time.Now()),
			Severities:   securitySeverities,
			Components:   securityComponents,
			CreditPrefs:  securityCreditPrefs,
			Form:         securityReportForm{DisclosureDays: defaultDisclosureDays},
			Captcha:      captcha,
		}
		if captchaErr != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = renderPage(w, http.StatusOK, "contact_security", data)
		return
	}
	if r.Method == http.MethodPost {
		fd.contactPost(w, r)
		return
	}
	captcha, captchaErr := fd.issueCaptcha(r)
	if captchaErr != nil {
		http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	data := contactPageData{
		Base:       fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "contact.title"), fd.cfg.Server.SEO.Description),
		Enabled:    fd.cfg.Pages.Contact.Enabled,
		AbuseEmail: fd.cfg.Server.Contact.Abuse.Email,
		FormToken:  fd.contactToken(time.Now()),
		Captcha:    captcha,
	}
	_ = renderPage(w, http.StatusOK, "contact", data)
}

// contactPost validates and submits the contact form. A validated message
// is relayed to the operator's contact address through the AI.md PART 17
// notifier; per that PART's SMTP Requirement the relay is best-effort, so
// the visitor is shown success either way and nothing is ever queued.
func (fd *frontendDeps) contactPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, contactFormBodyLimit)
	base := fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "contact.title"), fd.cfg.Server.SEO.Description)
	abuseEmail := fd.cfg.Server.Contact.Abuse.Email

	if !fd.cfg.Pages.Contact.Enabled {
		data := contactPageData{Base: base, Enabled: false, AbuseEmail: abuseEmail}
		_ = renderPage(w, http.StatusOK, "contact", data)
		return
	}

	if err := r.ParseForm(); err != nil {
		captcha, issueErr := fd.issueCaptcha(r)
		if issueErr != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		data := contactPageData{Base: base, Enabled: true, AbuseEmail: abuseEmail, Error: t(r, "errors.form_read_failed"), FormToken: fd.contactToken(time.Now()), Captcha: captcha}
		_ = renderPage(w, http.StatusBadRequest, "contact", data)
		return
	}
	if reason, valid := contactShapeValid(r.PostForm, false, false); !valid {
		fd.contactSpamBlock(r, reason)
		captcha, issueErr := fd.issueCaptcha(r)
		if issueErr != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = renderPage(w, http.StatusBadRequest, "contact", contactPageData{Base: base, Enabled: true, AbuseEmail: abuseEmail, Error: t(r, "errors.form_read_failed"), FormToken: fd.contactToken(time.Now()), Captcha: captcha})
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	message := strings.TrimSpace(r.FormValue("message"))
	data := contactPageData{
		Base: base, Enabled: true, AbuseEmail: abuseEmail,
		Name: name, Email: email, Subject: subject, Message: message,
		FormToken: fd.contactToken(time.Now()),
	}
	now := time.Now()
	if fd.contactAntispam().Honeypot && r.FormValue("website") != "" {
		fd.contactSpamBlock(r, "honeypot")
		data.Submitted = true
		data.SuccessMessage = fd.contactSuccessMessage(r)
		_ = renderPage(w, http.StatusOK, "contact", data)
		return
	}

	switch {
	case name == "" || email == "" || subject == "" || message == "":
		fd.renderContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.all_fields_required"))
		return
	case !fd.contactTokenValid(r.FormValue("form_token"), now):
		fd.contactSpamBlock(r, "fill_time")
		fd.renderContactFailure(w, r, http.StatusBadRequest, &data, t(r, "contact.fill_wait"))
		return
	case !fd.verifyCaptcha(r, r.PostForm):
		fd.contactSpamBlock(r, "captcha")
		fd.renderContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.captcha_failed"))
		return
	}
	if reason, blocked := fd.contactRateReason(r, email, now); blocked {
		fd.contactSpamBlock(r, reason)
		fd.setContactRetryAfter(w, r, reason)
		fd.renderContactFailure(w, r, http.StatusTooManyRequests, &data, t(r, "errors.captcha_failed"))
		return
	}
	if reason := fd.contactContentReason(name, email, subject, message); reason != "" {
		fd.contactSpamBlock(r, reason)
		fd.renderContactFailure(w, r, http.StatusBadRequest, &data, "Your message could not be sent")
		return
	}
	if reason, blocked := fd.contactRecordAccepted(r, email, subject, message, now, true); blocked {
		fd.contactSpamBlock(r, reason)
		if reason == "duplicate" {
			data.Submitted = true
			data.SuccessMessage = fd.contactSuccessMessage(r)
			_ = renderPage(w, http.StatusOK, "contact", data)
			return
		}
		fd.setContactRetryAfter(w, r, reason)
		fd.renderContactFailure(w, r, http.StatusTooManyRequests, &data, t(r, "errors.captcha_failed"))
		return
	}

	fd.relayContactMessage(name, email, subject, message)

	data.Submitted = true
	data.SuccessMessage = fd.contactSuccessMessage(r)
	_ = renderPage(w, http.StatusOK, "contact", data)
}

func (fd *frontendDeps) contactSuccessMessage(r *http.Request) string {
	if message := fd.cfg.Pages.Contact.SuccessMessage; message != "" {
		return message
	}
	return t(r, "contact.success")
}

func (fd *frontendDeps) contactAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodGet {
		abuseEmail := strings.TrimSpace(fd.cfg.Server.Contact.Abuse.Email)
		if abuseEmail == "" {
			abuseEmail = strings.TrimSpace(fd.cfg.Server.Contact.General.Email)
		}
		captcha, err := fd.issueCaptcha(r)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "captcha unavailable"})
			return
		}
		fields := []map[string]any{
			{"name": "name", "type": "text", "required": true, "max_length": 100},
			{"name": "email", "type": "email", "required": true, "max_length": 254},
			{"name": "subject", "type": "text", "required": true, "max_length": 200},
			{"name": "message", "type": "textarea", "required": true, "max_length": 5000},
			{"name": "captcha", "type": "captcha", "required": true, "max_length": nil},
		}
		if fd.securityMode(r) {
			fields = append(fields,
				map[string]any{"name": "security_id", "type": "hidden", "required": true, "max_length": 100},
				map[string]any{"name": "researcher_gpg", "type": "text", "required": false, "max_length": 16384, "max_length_when": []map[string]any{{"prefix": "https://", "max_length": 2048}}},
				map[string]any{"name": "component", "type": "enum", "required": true, "max_length": 255, "enum": securityComponents},
				map[string]any{"name": "endpoint", "type": "text", "required": false, "max_length": 2048},
				map[string]any{"name": "severity", "type": "enum", "required": true, "max_length": nil, "enum": securitySeverities},
				map[string]any{"name": "summary", "type": "text", "required": true, "max_length": 200},
				map[string]any{"name": "steps", "type": "textarea", "required": true, "max_length": 10000},
				map[string]any{"name": "impact", "type": "textarea", "required": true, "max_length": 5000},
				map[string]any{"name": "suggested_fix", "type": "textarea", "required": false, "max_length": 5000},
				map[string]any{"name": "cve_requested", "type": "checkbox", "required": false, "max_length": nil},
				map[string]any{"name": "disclosure_days", "type": "number", "required": false, "max_length": nil},
				map[string]any{"name": "credit_preference", "type": "enum", "required": true, "max_length": nil, "enum": securityCreditPrefs},
				map[string]any{"name": "credit_handle", "type": "text", "required": false, "max_length": 100, "required_when": map[string]string{"field": "credit_preference", "equals": "handle"}},
				map[string]any{"name": "agreed_disclosure", "type": "checkbox", "required": true, "max_length": nil},
				map[string]any{"name": "app_version", "type": "hidden", "required": false, "max_length": nil},
				map[string]any{"name": "commit_hash", "type": "hidden", "required": false, "max_length": nil},
				map[string]any{"name": "timestamp", "type": "hidden", "required": false, "max_length": nil},
				map[string]any{"name": "request_user_agent", "type": "hidden", "required": false, "max_length": nil},
			)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"fields":       fields,
			"captcha":      captcha,
			"form_token":   fd.contactToken(time.Now()),
			"security_url": fd.resolver.BuildURL(r, "/server/security"),
			"abuse_email":  abuseEmail,
		})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, contactFormBodyLimit)
	if err := parseContactAPIForm(r); err != nil {
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "invalid form", true)
		return
	}
	if fd.securityMode(r) {
		fd.securityReportAPIPost(w, r)
		return
	}
	if reason, valid := contactShapeValid(r.PostForm, false, true); !valid {
		fd.contactSpamBlock(r, reason)
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "invalid form", true)
		return
	}
	if fd.contactAntispam().Honeypot && r.FormValue("website") != "" {
		fd.contactSpamBlock(r, "honeypot")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"message": fd.contactSuccessMessage(r)}})
		return
	}
	if !fd.contactTokenValid(r.FormValue("form_token"), time.Now()) {
		fd.contactSpamBlock(r, "fill_time")
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, t(r, "contact.fill_wait"), true)
		return
	}
	if !fd.verifyCaptcha(r, r.PostForm) {
		fd.contactSpamBlock(r, "captcha")
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "validation failed", true)
		return
	}
	name, email := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("email"))
	subject, message := strings.TrimSpace(r.FormValue("subject")), strings.TrimSpace(r.FormValue("message"))
	now := time.Now()
	if reason, blocked := fd.contactRateReason(r, email, now); blocked {
		fd.contactSpamBlock(r, reason)
		fd.setContactRetryAfter(w, r, reason)
		fd.writeAPIContactFailure(w, r, http.StatusTooManyRequests, "rate limited", true)
		return
	}
	if reason := fd.contactContentReason(name, email, subject, message); reason != "" {
		fd.contactSpamBlock(r, reason)
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "Your message could not be sent", true)
		return
	}
	if reason, blocked := fd.contactRecordAccepted(r, email, subject, message, now, true); blocked {
		fd.contactSpamBlock(r, reason)
		if reason == "duplicate" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"message": fd.contactSuccessMessage(r)}})
			return
		}
		fd.setContactRetryAfter(w, r, reason)
		fd.writeAPIContactFailure(w, r, http.StatusTooManyRequests, "rate limited", true)
		return
	}
	fd.relayContactMessage(name, email, subject, message)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"message": fd.contactSuccessMessage(r)}})
}

func (fd *frontendDeps) writeAPIContactFailure(w http.ResponseWriter, r *http.Request, status int, message string, refresh bool) {
	payload := map[string]any{"ok": false, "error": message}
	if refresh {
		payload["form_token"] = fd.contactToken(time.Now())
		challenge, err := fd.issueCaptcha(r)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "captcha unavailable"})
			return
		}
		payload["captcha"] = challenge
	}
	writeJSON(w, status, payload)
}

func (fd *frontendDeps) renderContactFailure(w http.ResponseWriter, r *http.Request, status int, data *contactPageData, message string) {
	data.Error = message
	data.FormToken = fd.contactToken(time.Now())
	challenge, err := fd.issueCaptcha(r)
	if err != nil {
		http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	data.Captcha = challenge
	_ = renderPage(w, status, "contact", *data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func setContactRetryAfter(w http.ResponseWriter, window string) {
	duration, err := time.ParseDuration(window)
	if err != nil || duration <= 0 {
		duration = time.Hour
	}
	seconds := int(duration.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}

// relayContactMessage forwards a validated contact submission to the
// operator's general contact address. It is best-effort by design: the
// visitor has already been told their message was accepted, and per AI.md
// PART 17 "SMTP Requirement" there is no queue — with no working SMTP the
// message is simply not relayed, and nothing is logged as "would have
// sent".
func (fd *frontendDeps) relayContactMessage(name, email, subject, message string) {
	if !fd.notifier.Enabled() {
		return
	}
	to := strings.TrimSpace(fd.cfg.Server.Contact.General.Email)
	if to == "" {
		to = strings.TrimSpace(fd.cfg.Server.Contact.Admin.Email)
	}
	if to == "" {
		return
	}
	body := fmt.Sprintf("A message was submitted through the contact form.\n\nFrom: %s <%s>\nSubject: %s\n\n%s\n",
		name, email, subject, message)
	appName := strings.TrimSpace(fd.cfg.Server.Branding.SiteName)
	if appName == "" {
		appName = "Shortner"
	}
	_ = fd.notifier.SendRawReplyTo([]string{to}, email, "["+appName+" contact] "+subject, body)
}

// consentCookieName is the granular cookie-consent cookie, per AI.md
// PART 16 "Cookie Consent Banner" -> "Implementation".
const consentCookieName = "cookie_consent"

// consentChoice is the JSON value stored in consentCookieName.
type consentChoice struct {
	Essential   bool `json:"essential"`
	Preferences bool `json:"preferences"`
	Analytics   bool `json:"analytics"`
}

// consentHandler handles the cookie-consent banner's, the preferences
// dialog's, and the CCPA page's form POSTs — all three post here with a
// "decision" field ("accept", "decline", or "custom").
func (fd *frontendDeps) consentHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	choice := consentChoice{Essential: true}
	switch r.FormValue("decision") {
	case "accept":
		choice.Preferences = true
		choice.Analytics = fd.cfg.Server.Privacy.Cookies.Analytics.Enabled
	case "custom":
		// Every boolean source goes through config.IsTruthy, per AI.md
		// PART 5 "Boolean Parsing" — an unchecked box submits nothing,
		// which parses as false.
		choice.Preferences = config.IsTruthy(r.FormValue("preferences"))
		choice.Analytics = config.IsTruthy(r.FormValue("analytics"))
	default:
		// "decline" (or anything else): essential only.
	}

	raw, err := json.Marshal(choice)
	if err == nil {
		// JSON's quote/brace/colon bytes are not valid raw cookie-value
		// octets per RFC 6265 — net/http silently strips them on Set,
		// corrupting the value. base64 keeps it a single opaque token.
		encoded := base64.RawURLEncoding.EncodeToString(raw)
		secure := fd.cfg.Server.CSRF.Secure != "false"
		http.SetCookie(w, &http.Cookie{
			Name:     consentCookieName,
			Value:    encoded,
			Path:     "/",
			MaxAge:   int((365 * 24 * time.Hour).Seconds()),
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
		})
	}

	dest := r.FormValue("return_to")
	if dest == "" {
		dest = r.Referer()
	}
	if !isSafeLocalRedirect(dest) {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// isSafeLocalRedirect reports whether dest is a same-site, path-only
// redirect target — rejecting scheme-bearing, protocol-relative
// ("//evil.com"), and backslash-disguised ("/\evil.com") targets that
// browsers or proxies could interpret as pointing off-site.
func isSafeLocalRedirect(dest string) bool {
	if dest == "" || !strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "//") || strings.HasPrefix(dest, "/\\") {
		return false
	}
	parsed, err := url.Parse(dest)
	if err != nil {
		return false
	}
	return parsed.Scheme == "" && parsed.Host == "" && strings.HasPrefix(parsed.Path, "/")
}

func (fd *frontendDeps) ccpaHandler(w http.ResponseWriter, r *http.Request) {
	if !fd.cfg.Server.Privacy.Data.Sold {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	data := struct {
		Base PageData
	}{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "ccpa.do_not_sell"), fd.cfg.Server.SEO.Description)}
	_ = renderPage(w, http.StatusOK, "ccpa", data)
}

// healthzPageData is the data bound to page/healthz.tmpl.
type healthzPageData struct {
	Base   PageData
	Health HealthResponse
}

// healthzHTMLHandler renders the HTML health page for browsers, per AI.md
// PART 16 "/server/ Frontend Routes" -> "/server/healthz | Health page
// (HTML)"; every other client type falls through to hd.healthHandler's
// existing JSON/text negotiation unchanged.
func (fd *frontendDeps) healthzHTMLHandler(hd *healthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if detectClientType(r) != clientHTML {
			hd.healthHandler()(w, r)
			return
		}
		resp := hd.buildHealthResponse(r.Context())
		status := http.StatusOK
		if resp.Status != "healthy" {
			status = http.StatusServiceUnavailable
		}
		data := healthzPageData{
			Base:   fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "health.title"), fd.cfg.Server.SEO.Description),
			Health: resp,
		}
		_ = renderPage(w, status, "healthz", data)
	}
}

// statsPageData is the data bound to page/stats.tmpl.
type statsPageData struct {
	Base  PageData
	Stats StatsResponse
}

// statsHTMLHandler renders the HTML click-analytics page for browsers at
// GET /{slug}/stats, per AI.md PART 16 frontend-rules.md "Nested
// sub-resource pattern"; every other client type falls through to
// ld.statsHandler's existing JSON/text negotiation unchanged, reusing the
// exact same lookupLink/db.ClicksForLink/buildStatsResponse machinery
// rather than duplicating it.
func (fd *frontendDeps) statsHTMLHandler(ld *linkDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if detectClientType(r) != clientHTML {
			ld.statsHandler(w, r)
			return
		}

		slug := chi.URLParam(r, "slug")
		link, err := lookupLink(r.Context(), ld.sqlDB, slug)
		if err != nil {
			sendError(w, r, mapLookupErr(err))
			return
		}
		clicks, err := db.ClicksForLink(r.Context(), ld.sqlDB, link.ID, statsMaxRows)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		resp := buildStatsResponse(link, clicks)
		data := statsPageData{
			Base:  fd.newPageData(r, requestCSRFToken(r, fd.cfg), tf(r, "stats.title_short", map[string]string{"code": resp.ShortCode}), fd.cfg.Server.SEO.Description),
			Stats: resp,
		}
		_ = renderPage(w, http.StatusOK, "stats", data)
	}
}

// listPageData is the data bound to page/list.tmpl.
type listPageData struct {
	Base       PageData
	Links      []LinkResponse
	Pagination paginationResponse
	PrevPage   int
	NextPage   int
	HasPrev    bool
	HasNext    bool
}

// listHTMLHandler renders the public, paginated "all created links" page at
// GET /list, per IDEA.md "Endpoints": "List all created links (public,
// paginated)" — every client type gets the same page, since this is a
// browse page, not a resource with its own JSON/text representations
// distinct from the JSON API's GET /api/{api_version}/links (which
// non-browser clients should use directly).
func (fd *frontendDeps) listHTMLHandler(ld *linkDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if detectClientType(r) != clientHTML {
			ld.listLinksHandler(w, r)
			return
		}

		page, limit := parsePagination(r)
		resp, err := ld.buildListLinksResponse(r.Context(), r, page, limit)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}

		data := listPageData{
			Base:       fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "list.title"), fd.cfg.Server.SEO.Description),
			Links:      resp.Data,
			Pagination: resp.Pagination,
			PrevPage:   page - 1,
			NextPage:   page + 1,
			HasPrev:    page > 1,
			HasNext:    page < resp.Pagination.Pages,
		}
		_ = renderPage(w, http.StatusOK, "list", data)
	}
}
