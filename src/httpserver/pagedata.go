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

// footerRepoURL is the {PLATFORM_REPO_URL} of the default application
// footer's "Made with ❤️" row, per AI.md PART 16 "Default Application
// Footer". Taken from the repository's own origin remote.
const footerRepoURL = "https://github.com/apimgr/shortner"

// footerTimeLayout is AI.md PART 16's user-facing timestamp format
// (`%B %d, %Y at %H:%M:%S %Z`). RFC 3339 is reserved for machine-readable
// output (API responses, logs, health endpoints).
const footerTimeLayout = "January 02, 2006 at 15:04:05 MST"

// PageData holds the shared branding/footer/consent fields.
type PageData struct {
	Title          string
	Description    string
	SiteName       string
	Tagline        string
	ProjectOrg     string
	ProjectVersion string
	BuildDate      string
	// BuildDateTime is BuildDate rendered in the user-facing footer format
	// (AI.md PART 16: `%B %d, %Y at %H:%M:%S %Z`); BuildDate itself stays
	// machine-readable for any non-display use.
	BuildDateTime  string
	RepoURL        string
	CurrentYear    int
	Theme          string
	CurrentPath    string
	CSRFToken      string
	CSRFCookieName string

	FooterCustomHTML template.HTML
	// TorOnionAddress is always empty until PART 31 (Tor) is implemented —
	// the template slot exists so wiring it later needs no template edit.
	TorOnionAddress string

	// HasConsentCookie gates the cookie-consent banner server-side, per
	// AI.md PART 16 "Cookie Consent Banner" -> "Server-side behavior": the
	// banner is rendered visible only when no cookie_consent cookie exists,
	// never hidden and revealed by a script.
	HasConsentCookie             bool
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
// same-origin POST form must echo back, and r's theme cookie (falling
// back to the operator's configured default, then "dark") to render the
// theme-{dark,light,auto} class on <html> with zero FOUC — see
// requestTheme in theme.go and AI.md PART 16 "Theme Detection Flow".
func (fd *frontendDeps) newPageData(r *http.Request, csrfToken, title, description string) PageData {
	cfg := fd.cfg
	footerHTML, _ := sanitize.ValidateFooterHTML(cfg.Web.Footer.CustomHTML)

	cookieName := cfg.Server.CSRF.CookieName
	if cookieName == "" {
		cookieName = "csrf_token"
	}

	currentPath := r.URL.Path
	if r.URL.RawQuery != "" {
		currentPath += "?" + r.URL.RawQuery
	}

	return PageData{
		Title:          title,
		Description:    description,
		SiteName:       projectInfo.Name,
		Tagline:        projectInfo.Tagline,
		ProjectOrg:     "apimgr",
		ProjectVersion: fd.version,
		BuildDate:      fd.buildDate,
		BuildDateTime:  footerBuildDateTime(fd.buildDate),
		RepoURL:        footerRepoURL,
		CurrentYear:    time.Now().Year(),
		Theme:          requestTheme(r, cfg),
		CurrentPath:    currentPath,
		CSRFToken:      csrfToken,
		CSRFCookieName: cookieName,

		FooterCustomHTML: template.HTML(footerHTML),
		TorOnionAddress:  "",

		HasConsentCookie:             hasConsentCookie(r),
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

// footerBuildDateTime renders the embedded build timestamp (RFC 3339 UTC,
// derived from the BuildEpoch ldflag in src/common/version) in the
// user-facing footer format, in the server's local zone. Unparseable or
// unset values ("N/A" on a `go run`/`go test` build) pass through as-is.
func footerBuildDateTime(buildDate string) string {
	t, err := time.Parse(time.RFC3339, buildDate)
	if err != nil {
		return buildDate
	}
	return t.Local().Format(footerTimeLayout)
}

// hasConsentCookie reports whether r already carries a cookie_consent
// cookie, i.e. whether the visitor has answered the banner.
func hasConsentCookie(r *http.Request) bool {
	c, err := r.Cookie(consentCookieName)
	return err == nil && c.Value != ""
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
