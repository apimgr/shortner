// PageData is the set of fields every frontend page template needs,
// common to layout/public.tmpl and its partials regardless of which
// page/*.tmpl supplies the content block. Page-specific handlers embed
// this as the Base field of their own data struct so `.Base.SiteName`
// etc. resolve the same way no matter which page is rendering.
package httpserver

import (
	"html/template"
	"net/http"
	"time"

	"github.com/apimgr/shortner/src/common/sanitize"
	"github.com/apimgr/shortner/src/config"
)

// PageData holds the shared branding/footer/consent fields.
type PageData struct {
	Title          string
	Description    string
	SiteName       string
	Tagline        string
	ProjectOrg     string
	ProjectVersion string
	BuildDate      string
	CurrentYear    int
	Theme          string
	CSRFToken      string
	CSRFCookieName string

	FooterCustomHTML template.HTML
	// TorOnionAddress is always empty until PART 31 (Tor) is implemented —
	// the template slot exists so wiring it later needs no template edit.
	TorOnionAddress string

	ConsentMessage               string
	ConsentPolicyURL             string
	ConsentPolicyText            string
	ConsentPreferencesText       string
	ConsentDeclineLabel          string
	ConsentAcceptLabel           string
	CookieEssentialDescription   string
	CookiePreferencesDescription string
	CookieAnalyticsDescription   string
	CCPAEnabled                  bool
}

// frontendDeps bundles what every frontend page handler needs, built once
// by New() alongside the existing linkDeps/healthDeps. ld is the same
// *linkDeps used by the API routes, reused (never duplicated) for the home
// page's link-creation POST and the stats page's data lookup.
type frontendDeps struct {
	cfg       *config.Config
	version   string
	buildDate string
	ld        *linkDeps
}

// newPageData builds the common PageData for one request, using r's
// csrf_token cookie (set by csrfMiddleware on every GET) as the value a
// same-origin POST form must echo back.
func (fd *frontendDeps) newPageData(csrfToken, title, description string) PageData {
	cfg := fd.cfg
	footerHTML, _ := sanitize.ValidateFooterHTML(cfg.Web.Footer.CustomHTML)

	cookieName := cfg.Server.CSRF.CookieName
	if cookieName == "" {
		cookieName = "csrf_token"
	}

	return PageData{
		Title:          title,
		Description:    description,
		SiteName:       projectInfo.Name,
		Tagline:        projectInfo.Tagline,
		ProjectOrg:     "apimgr",
		ProjectVersion: fd.version,
		BuildDate:      fd.buildDate,
		CurrentYear:    time.Now().Year(),
		Theme:          cfg.Web.Theme,
		CSRFToken:      csrfToken,
		CSRFCookieName: cookieName,

		FooterCustomHTML: template.HTML(footerHTML),
		TorOnionAddress:  "",

		ConsentMessage:               cfg.Server.Privacy.GetConsentMessage(),
		ConsentPolicyURL:             cfg.Server.Privacy.Consent.Policy.URL,
		ConsentPolicyText:            defaultString(cfg.Server.Privacy.Consent.Policy.Text, "Privacy Policy"),
		ConsentPreferencesText:       defaultString(cfg.Server.Privacy.Consent.PreferencesText, "Cookie preferences"),
		ConsentDeclineLabel:          defaultString(cfg.Server.Privacy.Consent.Buttons.Decline, "Decline"),
		ConsentAcceptLabel:           defaultString(cfg.Server.Privacy.Consent.Buttons.Accept, "Accept"),
		CookieEssentialDescription:   cfg.Server.Privacy.Cookies.Essential.Description,
		CookiePreferencesDescription: cfg.Server.Privacy.Cookies.Preferences.Description,
		CookieAnalyticsDescription:   cfg.Server.Privacy.Cookies.Analytics.Description,
		CCPAEnabled:                  cfg.Server.Privacy.Data.Sold,
	}
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// requestCSRFToken returns the csrf_token cookie value carried by r, or ""
// if none was set (csrfMiddleware always sets one on GET, so this should
// only be empty when CSRF is disabled).
func requestCSRFToken(r *http.Request, cfg *config.Config) string {
	name := cfg.Server.CSRF.CookieName
	if name == "" {
		name = "csrf_token"
	}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
