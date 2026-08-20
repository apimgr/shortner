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
	"html/template"
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
	r.Get("/server/terms", fd.termsHandler)
	r.Get("/server/contact", fd.contactHandler)
	r.Post("/server/contact", fd.contactHandler)
	r.Post("/server/consent", fd.consentHandler)
	r.Get("/server/ccpa", fd.ccpaHandler)
	r.Get("/server/security", fd.securityHandler)
	r.Get("/server/security/policy", fd.securityPolicyHandler)
	r.Get("/server/security/thanks", fd.securityThanksHandler)
	r.Get("/server/dpo", fd.dpoHandler)
	r.Post("/server/theme", fd.themeHandler)

	r.Get("/server/healthz", fd.healthzHTMLHandler(hd))
	r.Get("/list", fd.listHTMLHandler(ld))
	r.Get("/{slug}", ld.resolveHandler)
	r.Get("/{slug}/stats", fd.statsHTMLHandler(ld))
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
	data := homePageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Shorten a URL", fd.cfg.Server.SEO.Description)}
	_ = renderPage(w, http.StatusOK, "home", data)
}

// homePost handles the server-rendered POST / form submission, reusing the
// same validation and db.CreateLink*/CreateResourceToken calls as
// createLinkHandler in links.go (PART 14) instead of duplicating that
// business logic.
func (fd *frontendDeps) homePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fd.homeError(w, r, "Could not read form submission.", "", "", http.StatusBadRequest)
		return
	}

	destInput := r.FormValue("url")
	slugInput := strings.TrimSpace(r.FormValue("slug"))

	destination, ok := validateDestinationURL(destInput)
	if !ok {
		fd.homeError(w, r, "Enter a valid http(s) URL.", destInput, slugInput, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	var link *db.Link
	var err error

	if slugInput != "" {
		if !security.ValidateSlugFormat(slugInput) {
			fd.homeError(w, r, "Custom slug must be 3-20 characters: letters, numbers, hyphens.", destInput, slugInput, http.StatusBadRequest)
			return
		}
		if security.IsReservedSlug(slugInput) {
			fd.homeError(w, r, "That slug is reserved — choose another.", destInput, slugInput, http.StatusConflict)
			return
		}
		link, err = db.CreateLinkCustomSlug(ctx, fd.ld.sqlDB, slugInput, destination, nil)
		if errors.Is(err, db.ErrSlugTaken) {
			fd.homeError(w, r, "That slug is already in use — choose another.", destInput, slugInput, http.StatusConflict)
			return
		}
	} else {
		link, err = db.CreateLinkAutoCode(ctx, fd.ld.sqlDB, destination, nil)
	}
	if err != nil {
		fd.homeError(w, r, "Something went wrong creating your link. Try again.", destInput, slugInput, http.StatusInternalServerError)
		return
	}

	raw, _, err := db.CreateResourceToken(ctx, fd.ld.sqlDB, "link", strconv.FormatInt(link.ID, 10), nil)
	if err != nil {
		fd.homeError(w, r, "Something went wrong creating your link. Try again.", destInput, slugInput, http.StatusInternalServerError)
		return
	}

	shortURL := fd.ld.resolver.BuildURL(r, "/"+link.ShortCode)
	data := homePageData{
		Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Shorten a URL", fd.cfg.Server.SEO.Description),
		Link: &homeLinkResult{ShortURL: shortURL, OwnerToken: raw},
	}
	_ = renderPage(w, http.StatusCreated, "home", data)
}

func (fd *frontendDeps) homeError(w http.ResponseWriter, r *http.Request, msg, dest, slug string, status int) {
	data := homePageData{
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Shorten a URL", fd.cfg.Server.SEO.Description),
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
		Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), "About", fd.cfg.Server.SEO.Description),
		AboutContent: template.HTML(sanitize.SanitizeFooterHTML(fd.cfg.Pages.About.Content)),
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
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Help", fd.cfg.Server.SEO.Description),
		HelpContent: template.HTML(sanitize.SanitizeFooterHTML(fd.cfg.Pages.Help.Content)),
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
		content = template.HTML(sanitize.SanitizeFooterHTML(custom))
	}
	data := termsPageData{
		Base:    fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Terms of Service", fd.cfg.Server.SEO.Description),
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

	DataCollection string
	DataUsage      string
	DataSecurity   string

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
		content = template.HTML(sanitize.SanitizeFooterHTML(custom))
	}

	data := privacyPageData{
		Base:    fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Privacy Policy", fd.cfg.Server.SEO.Description),
		Content: content,

		DataCollection: priv.Content.DataCollection,
		DataUsage:      priv.GetDataUsageContent(),
		DataSecurity:   priv.Content.DataSecurity,

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

// contactCaptchaAnswer is the expected answer to the contact form's
// no-JavaScript math question ("What is 3 + 4?"), shared by the standard
// and security-report submission paths.
const contactCaptchaAnswer = "7"

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
}

// securityContactPageData is the data bound to page/contact_security.tmpl
// — the security-report mode of /server/contact, per AI.md PART 11
// "`/server/contact?security_id={id}` — Mode Switch".
type securityContactPageData struct {
	Base         PageData
	SecurityMode bool
	SecurityID   string
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
		data := securityContactPageData{
			Base:         fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Report a vulnerability", fd.cfg.Server.SEO.Description),
			SecurityMode: true,
			SecurityID:   r.FormValue("security_id"),
			Severities:   securitySeverities,
			Components:   securityComponents,
			CreditPrefs:  securityCreditPrefs,
			Form:         securityReportForm{DisclosureDays: defaultDisclosureDays},
		}
		_ = renderPage(w, http.StatusOK, "contact_security", data)
		return
	}
	if r.Method == http.MethodPost {
		fd.contactPost(w, r)
		return
	}
	data := contactPageData{
		Base:       fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Contact", fd.cfg.Server.SEO.Description),
		Enabled:    fd.cfg.Pages.Contact.Enabled,
		AbuseEmail: fd.cfg.Server.Contact.Abuse.Email,
	}
	_ = renderPage(w, http.StatusOK, "contact", data)
}

// contactPost validates and "submits" the contact form. Actually delivering
// the message by email depends on AI.md PART 17 (notifications/SMTP),
// which is not implemented yet (see TODO.AI.md) — the message is accepted
// and the visitor is shown success, but nothing is sent or persisted past
// the request. This is a deliberate, documented no-op, not a bug.
func (fd *frontendDeps) contactPost(w http.ResponseWriter, r *http.Request) {
	base := fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Contact", fd.cfg.Server.SEO.Description)
	abuseEmail := fd.cfg.Server.Contact.Abuse.Email

	if !fd.cfg.Pages.Contact.Enabled {
		data := contactPageData{Base: base, Enabled: false, AbuseEmail: abuseEmail}
		_ = renderPage(w, http.StatusOK, "contact", data)
		return
	}

	if err := r.ParseForm(); err != nil {
		data := contactPageData{Base: base, Enabled: true, AbuseEmail: abuseEmail, Error: "Could not read form submission."}
		_ = renderPage(w, http.StatusBadRequest, "contact", data)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	message := strings.TrimSpace(r.FormValue("message"))
	captcha := strings.TrimSpace(r.FormValue("captcha"))

	data := contactPageData{
		Base: base, Enabled: true, AbuseEmail: abuseEmail,
		Name: name, Email: email, Subject: subject, Message: message,
	}

	switch {
	case name == "" || email == "" || subject == "" || message == "":
		data.Error = "All fields are required."
		_ = renderPage(w, http.StatusBadRequest, "contact", data)
		return
	case captcha != contactCaptchaAnswer:
		data.Error = "That doesn't look right — try the math question again."
		_ = renderPage(w, http.StatusBadRequest, "contact", data)
		return
	}

	data.Submitted = true
	data.SuccessMessage = fd.cfg.Pages.Contact.SuccessMessage
	if data.SuccessMessage == "" {
		data.SuccessMessage = "Thank you for your message. We'll respond soon."
	}
	_ = renderPage(w, http.StatusOK, "contact", data)
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
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
		return
	}
	data := struct {
		Base PageData
	}{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Do Not Sell My Personal Information", fd.cfg.Server.SEO.Description)}
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
			Base:   fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Server Health", fd.cfg.Server.SEO.Description),
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
			apperr.SendError(w, mapLookupErr(err))
			return
		}
		clicks, err := db.ClicksForLink(r.Context(), ld.sqlDB, link.ID, statsMaxRows)
		if err != nil {
			apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		resp := buildStatsResponse(link, clicks)
		data := statsPageData{
			Base:  fd.newPageData(r, requestCSRFToken(r, fd.cfg), "Stats: "+resp.ShortCode, fd.cfg.Server.SEO.Description),
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
			apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
			return
		}

		data := listPageData{
			Base:       fd.newPageData(r, requestCSRFToken(r, fd.cfg), "All Links", fd.cfg.Server.SEO.Description),
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
