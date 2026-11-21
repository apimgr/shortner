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
	r.Get("/server/help/mcp", fd.mcpHelpPage)
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
	data := homePageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "home.title"), fd.cfg.Server.Branding.Description)}
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
		Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "home.title"), fd.cfg.Server.Branding.Description),
		Link: &homeLinkResult{ShortURL: shortURL, OwnerToken: ownerToken},
	}
	_ = renderPage(w, http.StatusCreated, "home", data)
}

func (fd *frontendDeps) homeError(w http.ResponseWriter, r *http.Request, msg, dest, slug string, status int) {
	data := homePageData{
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "home.title"), fd.cfg.Server.Branding.Description),
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
		Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "about.title"), fd.cfg.Server.Branding.Description),
		AboutContent: fd.operatorHTML(r, "about", fd.operatorVariables(r)),
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
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "help.title"), fd.cfg.Server.Branding.Description),
		HelpContent: sanitize.RenderMarkdown(fd.cfg.Server.Pages.Help.Content, r.Host),
	}
	_ = renderPage(w, http.StatusOK, "help", data)
}

// termsPageData is the data bound to page/terms.tmpl.
type termsPageData struct {
	Base             PageData
	Content          template.HTML
	HasCustomContent bool
}

func (fd *frontendDeps) termsHandler(w http.ResponseWriter, r *http.Request) {
	entry, hasContent := configuredOperatorContent(fd.branding.load(fd.cfg).Branding.Content, "terms")
	if hasContent {
		_, hasContent = entry.Body.Resolve(langFromContext(r))
	}
	data := termsPageData{
		Base:             fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "terms.title"), fd.cfg.Server.Branding.Description),
		Content:          fd.operatorHTML(r, "terms", fd.operatorVariables(r)),
		HasCustomContent: hasContent,
	}
	_ = renderPage(w, http.StatusOK, "terms", data)
}

// privacyPageData is the data bound to page/privacy.tmpl, sourced from
// server.privacy in server.yml (auto-generated privacy page, per AI.md
// PART 16 "/server/privacy").
type privacyPageData struct {
	Base         PageData
	Extension    template.HTML
	ContactEmail string

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

	data := privacyPageData{
		Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "privacy.title"), fd.cfg.Server.Branding.Description),
		Extension:    fd.operatorHTML(r, "privacy", fd.operatorVariables(r)),
		ContactEmail: strings.TrimSpace(fd.cfg.Server.Contact.DPO.Email),

		DataCollection: "",
		DataUsage:      "This information is used to create and redirect short links, measure aggregate usage, and prevent abuse.",
		DataSecurity:   "",

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
	Base            PageData
	Enabled         bool
	Submitted       bool
	SuccessMessage  string
	Error           string
	Name            string
	Email           string
	Subject         string
	Message         string
	AbuseEmail      string
	Type            string
	Types           []string
	ReportedURL     string
	Reason          string
	Reasons         []string
	CopyrightedWork string
	GoodFaith       bool
	Accuracy        bool
	Signature       string
	RequestKind     string
	RequestKinds    []string
	FormToken       string
	Review          bool
	ReviewToken     string
	ConfirmAfter    int
	Captcha         captchaDescriptor
}

var contactReportTypes = []string{"abuse", "dmca", "privacy"}
var contactAbuseReasons = []string{"illegal_content", "terms_violation", "harassment", "malware_phishing", "other"}
var contactPrivacyKinds = []string{"access", "export", "deletion", "correction", "opt_out", "other"}

// securityContactPageData is the data bound to page/contact_security.tmpl
// — the security-report mode of /server/contact, per AI.md PART 11
// "`/server/contact?security_id={id}` — Mode Switch".
type securityContactPageData struct {
	Base         PageData
	SecurityMode bool
	SecurityID   string
	FormToken    string
	Review       bool
	ReviewToken  string
	ConfirmAfter int
	ReviewValues url.Values
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
	if !fd.cfg.Server.Pages.Contact.Enabled {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, contactFormBodyLimit)
	}
	if fd.securityMode(r) {
		if r.Method == http.MethodPost {
			fd.securityReportPost(w, r)
			return
		}
		captcha, captchaErr := fd.issueCaptcha(r)
		data := securityContactPageData{
			Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "security_report.title"), fd.cfg.Server.Branding.Description),
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
	if r.FormValue("security_id") != "" {
		r.Form.Del("security_id")
		r.PostForm.Del("security_id")
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
		Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "contact.title"), fd.cfg.Server.Branding.Description),
		Enabled:      true,
		AbuseEmail:   fd.contactAbuseEmail(),
		Types:        contactReportTypes,
		Reasons:      contactAbuseReasons,
		RequestKinds: contactPrivacyKinds,
		FormToken:    fd.contactToken(time.Now()),
		Captcha:      captcha,
	}
	_ = renderPage(w, http.StatusOK, "contact", data)
}

// contactPost validates and submits the contact form. A validated message
// is relayed to the operator's contact address through the AI.md PART 17
// notifier; per that PART's SMTP Requirement the relay is best-effort, so
// the visitor is shown success either way and nothing is ever queued.
func (fd *frontendDeps) contactPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, contactFormBodyLimit)
	base := fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "contact.title"), fd.cfg.Server.Branding.Description)
	if !fd.cfg.Server.Pages.Contact.Enabled {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		captcha, issueErr := fd.issueCaptcha(r)
		if issueErr != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		data := fd.newContactPageData(r, base, t(r, "errors.form_read_failed"), captcha)
		_ = renderPage(w, http.StatusBadRequest, "contact", data)
		return
	}
	if !fd.contactOriginValid(r) {
		fd.contactSpamBlock(r, "origin")
		captcha, issueErr := fd.issueCaptcha(r)
		if issueErr != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = renderPage(w, http.StatusBadRequest, "contact", fd.newContactPageData(r, base, "Please submit the form from this site", captcha))
		return
	}
	if reason, valid := contactShapeValid(r.PostForm, false, false); !valid {
		fd.contactSpamBlock(r, reason)
		captcha, issueErr := fd.issueCaptcha(r)
		if issueErr != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = renderPage(w, http.StatusBadRequest, "contact", fd.newContactPageData(r, base, t(r, "errors.form_read_failed"), captcha))
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	message := strings.TrimSpace(r.FormValue("message"))
	data := fd.newContactPageData(r, base, "", captchaDescriptor{})
	data.Name, data.Email, data.Subject, data.Message = name, email, subject, message
	data.Type = r.FormValue("type")
	data.ReportedURL = r.FormValue("reported_url")
	data.Reason = r.FormValue("reason")
	data.CopyrightedWork = r.FormValue("copyrighted_work")
	data.GoodFaith = r.FormValue("good_faith_statement") == "true"
	data.Accuracy = r.FormValue("accuracy_statement") == "true"
	data.Signature = r.FormValue("signature")
	data.RequestKind = r.FormValue("request_kind")
	now := time.Now()
	step := r.FormValue("step")
	if step == "" {
		step = "submit"
	}
	decoy := fd.contactAntispam().Honeypot && r.FormValue("website") != ""
	if decoy {
		fd.contactSpamBlock(r, "honeypot")
	}

	if step == "submit" {
		if reason := fd.contactTokenValidationReason(r.FormValue("form_token"), now); reason != "" {
			fd.contactSpamBlock(r, reason)
			fd.renderContactFailure(w, r, http.StatusBadRequest, &data, t(r, "contact.fill_wait"))
			return
		}
	}
	switch {
	case name == "" || email == "" || subject == "" || message == "":
		fd.renderContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.all_fields_required"))
		return
	case step == "submit" && !fd.verifyCaptcha(r, r.PostForm):
		fd.contactSpamBlock(r, "captcha")
		fd.renderContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.captcha_failed"))
		return
	}
	if step == "edit" {
		data.FormToken = fd.contactToken(now)
		captcha, err := fd.issueCaptcha(r)
		if err != nil {
			http.Error(w, "captcha temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		data.Captcha = captcha
		_ = renderPage(w, http.StatusOK, "contact", data)
		return
	}
	if step != "submit" && step != "confirm" {
		fd.renderContactFailure(w, r, http.StatusBadRequest, &data, t(r, "errors.form_read_failed"))
		return
	}
	if step == "submit" {
		if reason, blocked := fd.contactRateReason(r, email, now); blocked {
			fd.contactSpamBlock(r, reason)
			fd.setContactRetryAfter(w, r, reason)
			fd.renderContactFailure(w, r, http.StatusTooManyRequests, &data, t(r, "errors.captcha_failed"))
			return
		}
		if !decoy {
			if reason := fd.contactContentReason(r, r.PostForm); reason != "" {
				fd.contactSpamBlock(r, reason)
				fd.renderContactFailure(w, r, http.StatusBadRequest, &data, "Your message could not be sent")
				return
			}
			if !fd.validReportTarget(r, r.PostForm) {
				fd.contactSpamBlock(r, "content")
				fd.renderContactFailure(w, r, http.StatusBadRequest, &data, "Your message could not be sent")
				return
			}
		}
		flags := ""
		if decoy {
			flags = "d"
		}
		data.Review, data.ReviewToken, data.ConfirmAfter = true, issueContactReviewToken(fd.installSecret, r.PostForm, now, flags), fd.contactAntispam().ReviewMinSeconds
		_ = renderPage(w, http.StatusOK, "contact", data)
		return
	}
	settings := fd.contactAntispam()
	nonce, valid := verifyContactReviewToken(fd.installSecret, r.FormValue("review_token"), r.PostForm, now, settings.ReviewMinSeconds, settings.ReviewMaxSeconds)
	if !valid || r.FormValue("confirmed") != "true" {
		fd.contactSpamBlock(r, "review")
		data.Error = "Please read the details and confirm"
		data.Review, data.ReviewToken, data.ConfirmAfter = true, issueContactReviewToken(fd.installSecret, r.PostForm, now, contactReviewFlags(r.FormValue("review_token"))), settings.ReviewMinSeconds
		_ = renderPage(w, http.StatusBadRequest, "contact", data)
		return
	}
	if !fd.consumeContactNonce(nonce, now) {
		fd.contactSpamBlock(r, "replay")
		data.Error = "Please read the details and confirm"
		data.Review, data.ReviewToken, data.ConfirmAfter = true, issueContactReviewToken(fd.installSecret, r.PostForm, now, contactReviewFlags(r.FormValue("review_token"))), settings.ReviewMinSeconds
		_ = renderPage(w, http.StatusBadRequest, "contact", data)
		return
	}
	if strings.Contains(r.FormValue("review_token"), ".d.") {
		data.Submitted, data.SuccessMessage = true, fd.contactSuccessMessage(r)
		_ = renderPage(w, http.StatusOK, "contact", data)
		return
	}
	if reason, blocked := fd.contactRateReason(r, email, now); blocked {
		fd.contactSpamBlock(r, reason)
		fd.setContactRetryAfter(w, r, reason)
		fd.renderContactFailure(w, r, http.StatusTooManyRequests, &data, t(r, "errors.captcha_failed"))
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

	fd.relayReportMessage(r.PostForm)

	data.Submitted = true
	data.SuccessMessage = fd.contactSuccessMessage(r)
	_ = renderPage(w, http.StatusOK, "contact", data)
}

func (fd *frontendDeps) consumeContactNonce(nonce string, now time.Time) bool {
	state := contactSpamStateFor(fd.installSecret)
	state.mu.Lock()
	defer state.mu.Unlock()
	for key, expires := range state.usedTokens {
		if !expires.After(now) {
			delete(state.usedTokens, key)
		}
	}
	if _, exists := state.usedTokens[nonce]; exists {
		return false
	}
	if len(state.usedTokens) >= 50000 {
		for key := range state.usedTokens {
			delete(state.usedTokens, key)
			break
		}
	}
	state.usedTokens[nonce] = now.Add(15 * time.Minute)
	return true
}

func (fd *frontendDeps) newContactPageData(r *http.Request, base PageData, message string, captcha captchaDescriptor) contactPageData {
	return contactPageData{
		Base: base, Enabled: true, AbuseEmail: fd.contactAbuseEmail(), Types: contactReportTypes,
		Reasons: contactAbuseReasons, RequestKinds: contactPrivacyKinds,
		FormToken: fd.contactToken(time.Now()), Captcha: captcha, Error: message,
		Type: r.FormValue("type"), ReportedURL: r.FormValue("reported_url"), Reason: r.FormValue("reason"),
		CopyrightedWork: r.FormValue("copyrighted_work"), GoodFaith: r.FormValue("good_faith_statement") == "true",
		Accuracy: r.FormValue("accuracy_statement") == "true", Signature: r.FormValue("signature"), RequestKind: r.FormValue("request_kind"),
	}
}

func (fd *frontendDeps) contactAbuseEmail() string {
	return firstNonEmpty(fd.cfg.Server.Contact.Abuse.Email, fd.cfg.Server.Contact.General.Email)
}

func (fd *frontendDeps) contactSuccessMessage(r *http.Request) string {
	if message := fd.cfg.Server.Pages.Contact.SuccessMessage; message != "" {
		return message
	}
	return t(r, "contact.success")
}

func (fd *frontendDeps) contactAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !fd.cfg.Server.Pages.Contact.Enabled {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	if r.Method == http.MethodGet {
		securityMode := fd.securityMode(r)
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
		types := contactReportTypes
		if securityMode {
			types = []string{"security"}
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
		} else {
			fields = append(fields, contactReportFieldDescriptors()...)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"fields":       fields,
			"types":        types,
			"captcha":      captcha,
			"form_token":   fd.contactToken(time.Now()),
			"security_url": fd.resolver.BuildURL(r, "/server/security"),
			"abuse_email":  fd.contactAbuseEmail(),
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
	if r.FormValue("security_id") != "" {
		r.Form.Del("security_id")
		r.PostForm.Del("security_id")
	}
	if reason, valid := contactShapeValid(r.PostForm, false, true); !valid {
		fd.contactSpamBlock(r, reason)
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "invalid form", true)
		return
	}
	if r.FormValue("step") == "edit" {
		challenge, err := fd.issueCaptcha(r)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "captcha unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"fields": contactReviewValues(r.PostForm), "form_token": fd.contactToken(time.Now()), "captcha": challenge}})
		return
	}
	step := r.FormValue("step")
	if step == "" {
		step = "submit"
	}
	decoy := fd.contactAntispam().Honeypot && r.FormValue("website") != ""
	if decoy {
		fd.contactSpamBlock(r, "honeypot")
	}
	now := time.Now()
	if step == "submit" {
		if reason := fd.contactTokenValidationReason(r.FormValue("form_token"), now); reason != "" {
			fd.contactSpamBlock(r, reason)
			fd.writeAPIContactFailure(w, r, http.StatusBadRequest, t(r, "contact.fill_wait"), true)
			return
		}
	}
	if step == "submit" && !fd.verifyCaptcha(r, r.PostForm) {
		fd.contactSpamBlock(r, "captcha")
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "validation failed", true)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	subject, message := strings.TrimSpace(r.FormValue("subject")), strings.TrimSpace(r.FormValue("message"))
	if step == "submit" {
		if reason, blocked := fd.contactRateReason(r, email, now); blocked {
			fd.contactSpamBlock(r, reason)
			fd.setContactRetryAfter(w, r, reason)
			fd.writeAPIContactFailure(w, r, http.StatusTooManyRequests, "rate limited", true)
			return
		}
		if !decoy {
			if reason := fd.contactContentReason(r, r.PostForm); reason != "" {
				fd.contactSpamBlock(r, reason)
				fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "Your message could not be sent", true)
				return
			}
			if !fd.validReportTarget(r, r.PostForm) {
				fd.contactSpamBlock(r, "content")
				fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "Your message could not be sent", true)
				return
			}
		}
		flags := ""
		if decoy {
			flags = "d"
		}
		token := issueContactReviewToken(fd.installSecret, r.PostForm, now, flags)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"review": map[string]string{"type": r.FormValue("type"), "email": email, "subject": subject}, "review_token": token, "confirm_after_seconds": fd.contactAntispam().ReviewMinSeconds}})
		return
	}
	settings := fd.contactAntispam()
	nonce, valid := verifyContactReviewToken(fd.installSecret, r.FormValue("review_token"), r.PostForm, now, settings.ReviewMinSeconds, settings.ReviewMaxSeconds)
	if step != "confirm" || !valid || r.FormValue("confirmed") != "true" {
		fd.contactSpamBlock(r, "review")
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "Please read the details and confirm", true)
		return
	}
	if !fd.consumeContactNonce(nonce, now) {
		fd.contactSpamBlock(r, "replay")
		fd.writeAPIContactFailure(w, r, http.StatusBadRequest, "Please read the details and confirm", true)
		return
	}
	if strings.Contains(r.FormValue("review_token"), ".d.") {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"message": fd.contactSuccessMessage(r)}})
		return
	}
	if reason, blocked := fd.contactRateReason(r, email, now); blocked {
		fd.contactSpamBlock(r, reason)
		fd.setContactRetryAfter(w, r, reason)
		fd.writeAPIContactFailure(w, r, http.StatusTooManyRequests, "rate limited", true)
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
	fd.relayReportMessage(r.PostForm)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"message": fd.contactSuccessMessage(r)}})
}

func contactReportFieldDescriptors() []map[string]any {
	return []map[string]any{
		{"name": "type", "type": "enum", "required": true, "max_length": nil, "enum": contactReportTypes},
		{"name": "reported_url", "type": "url", "required": false, "max_length": 2048, "required_when": map[string]any{"field": "type", "in": []string{"abuse", "dmca"}}},
		{"name": "reason", "type": "enum", "required": false, "max_length": nil, "enum": contactAbuseReasons, "required_when": map[string]string{"field": "type", "equals": "abuse"}},
		{"name": "copyrighted_work", "type": "textarea", "required": false, "max_length": 2000, "required_when": map[string]string{"field": "type", "equals": "dmca"}},
		{"name": "good_faith_statement", "type": "checkbox", "required": false, "max_length": nil, "required_when": map[string]string{"field": "type", "equals": "dmca"}},
		{"name": "accuracy_statement", "type": "checkbox", "required": false, "max_length": nil, "required_when": map[string]string{"field": "type", "equals": "dmca"}},
		{"name": "signature", "type": "text", "required": false, "max_length": 100, "required_when": map[string]string{"field": "type", "equals": "dmca"}},
		{"name": "request_kind", "type": "enum", "required": false, "max_length": nil, "enum": contactPrivacyKinds, "required_when": map[string]string{"field": "type", "equals": "privacy"}},
	}
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

// relayReportMessage sends one accepted report to the role selected by its type.
// Delivery is best-effort; the SMTP layer never queues or logs the report body.
func (fd *frontendDeps) relayReportMessage(values url.Values) {
	if !fd.notifier.Enabled() {
		return
	}
	var to string
	switch values.Get("type") {
	case "abuse", "dmca":
		to = firstNonEmpty(fd.cfg.Server.Contact.Abuse.Email, fd.cfg.Server.Contact.General.Email, fd.cfg.Server.Contact.Admin.Email)
	case "privacy":
		to = firstNonEmpty(fd.cfg.Server.Contact.DPO.Email, fd.cfg.Server.Contact.General.Email, fd.cfg.Server.Contact.Admin.Email)
	}
	if to == "" {
		return
	}
	var body strings.Builder
	fmt.Fprintf(&body, "A %s report was submitted through the report form.\n\nFrom: %s <%s>\nSubject: %s\n",
		values.Get("type"), values.Get("name"), values.Get("email"), values.Get("subject"))
	for _, field := range []struct{ key, label string }{
		{"reported_url", "Reported URL"}, {"reason", "Reason"}, {"copyrighted_work", "Copyrighted work"},
		{"good_faith_statement", "Good-faith statement"}, {"accuracy_statement", "Accuracy statement"},
		{"signature", "Signature"}, {"request_kind", "Privacy request"},
	} {
		if value := values.Get(field.key); value != "" {
			fmt.Fprintf(&body, "%s: %s\n", field.label, value)
		}
	}
	fmt.Fprintf(&body, "\n%s\n", values.Get("message"))
	appName := strings.TrimSpace(fd.branding.load(fd.cfg).Branding.Title)
	if appName == "" {
		appName = "Shortner"
	}
	_ = fd.notifier.SendRawReplyTo([]string{to}, values.Get("email"), "["+appName+" contact] "+values.Get("subject"), body.String())
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
	}{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "ccpa.do_not_sell"), fd.cfg.Server.Branding.Description)}
	_ = renderPage(w, http.StatusOK, "ccpa", data)
}

// healthzPageData is the data bound to page/healthz.tmpl.
type healthzPageData struct {
	Base           PageData
	Health         HealthResponse
	UptimeHuman    string
	TimestampHuman string
	BuildDateHuman string
	RequestsTotal  string
	Requests24h    string
	ActiveConns    string
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
		if resp.Status == "unhealthy" {
			status = http.StatusServiceUnavailable
		}
		data := healthzPageData{
			Base:           fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "health.title"), fd.cfg.Server.Branding.Description),
			Health:         resp,
			UptimeHuman:    formatHumanUptime(parseCompactUptime(resp.Uptime)),
			TimestampHuman: resp.Timestamp.UTC().Format("January 02, 2006 at 15:04:05 MST"),
			BuildDateHuman: humanHealthTimestamp(resp.Build.Date),
			RequestsTotal:  groupHealthCount(resp.Stats.RequestsTotal),
			Requests24h:    groupHealthCount(resp.Stats.Requests24h),
			ActiveConns:    groupHealthCount(int64(resp.Stats.ActiveConns)),
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
			Base:  fd.newPageData(r, requestCSRFToken(r, fd.cfg), tf(r, "stats.title_short", map[string]string{"code": resp.ShortCode}), fd.cfg.Server.Branding.Description),
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
			Base:       fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "list.title"), fd.cfg.Server.Branding.Description),
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
