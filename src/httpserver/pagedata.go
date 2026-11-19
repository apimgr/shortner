// PageData is the set of fields every frontend page template needs,
// common to layout/public.tmpl and its partials regardless of which
// page/*.tmpl supplies the content block. Page-specific handlers embed
// this as the Base field of their own data struct so `.Base.SiteName`
// etc. resolve the same way no matter which page is rendering.
package httpserver

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/common/sanitize"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/notify"
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
	Keywords       string
	Author         string
	CanonicalURL   string
	Robots         string
	OGImage        string
	TwitterHandle  string
	LogoURL        string
	FaviconURL     string
	StructuredData template.JS
	Verification   config.SEOVerification
	SiteName       string
	Tagline        string
	ProjectOrg     string
	ProjectVersion string
	AssetStamp     string
	BuildDate      string
	// BuildDateTime is BuildDate rendered in the user-facing footer format
	// (AI.md PART 16: `%B %d, %Y at %H:%M:%S %Z`); BuildDate itself stays
	// machine-readable for any non-display use.
	BuildDateTime          string
	RepoURL                string
	CurrentYear            int
	Theme                  string
	CurrentPath            string
	CSRFToken              string
	CSRFCookieName         string
	OwnerTokenCookieExists bool
	ToastPosition          string
	ToastDuration          int

	// Lang, Dir, and AvailableLanguages drive AI.md PART 30: the active
	// language of every {{t .Base.Lang ...}} call, the <html dir> attribute
	// taken from that locale's meta.direction, and the language selector's
	// option list. I18NEnabled hides the selector entirely when the
	// operator disabled language negotiation.
	Lang               string
	Dir                string
	AvailableLanguages []i18n.Language
	I18NEnabled        bool

	FooterCustomHTML template.HTML
	// TorOnionAddress and I2PAddress carry the live overlay addresses
	// (AI.md PART 31) so the footer can advertise them. Both are empty
	// whenever that network is not published, which is how a host with no
	// Tor binary — or with I2P left opt-out — renders no overlay block at
	// all. They are the addresses themselves, never a full URL: the
	// templates prepend `http://`, which is the only scheme an overlay
	// address may ever be served over.
	TorOnionAddress string
	I2PAddress      string
	// IsTorRequest marks a request that arrived over the hidden service.
	// Templates use it to keep clearnet-identifying details off onion
	// responses (AI.md PART 31 "Tor Privacy Rules").
	IsTorRequest bool

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
	Announcements                []announcementView
}

type announcementView struct {
	ID          string
	Type        string
	Title       string
	Message     string
	Dismissible bool
}

// frontendDeps bundles what every frontend page handler needs, built once
// by New() alongside the existing linkDeps/healthDeps. ld is the same
// *linkDeps used by the API routes, reused (never duplicated) for the home
// page's link-creation POST and the stats page's data lookup.
type frontendDeps struct {
	cfg       *config.Config
	version   string
	commitID  string
	buildDate string
	ld        *linkDeps
	// The PART 11 security-page inputs: resolver builds the per-request
	// URLs the pages display, installSecret derives the rotating
	// {security_id} the contact form validates, and audit records
	// security.security_id_invalid.
	resolver              *ProxyResolver
	installSecret         string
	previousInstallSecret string
	audit                 *applog.AuditLogger
	configDir             string
	// notifier sends the AI.md PART 17 email events the frontend raises.
	// Nil-safe: every notify.Notifier method is inert on a nil receiver.
	notifier *notify.Notifier
	metrics  *metrics.Metrics
	antispam atomic.Pointer[config.ContactAntispam]
	store    *db.Store
}

func (fd *frontendDeps) contactAntispam() config.ContactAntispam {
	settings := fd.antispam.Load()
	if settings == nil {
		return fd.cfg.Server.Contact.Antispam
	}
	copy := *settings
	copy.BlockedWords = append([]string(nil), settings.BlockedWords...)
	copy.BlockedEmailDomains = append([]string(nil), settings.BlockedEmailDomains...)
	return copy
}

func (fd *frontendDeps) setContactAntispam(settings config.ContactAntispam) {
	copy := settings
	copy.BlockedWords = append([]string(nil), settings.BlockedWords...)
	copy.BlockedEmailDomains = append([]string(nil), settings.BlockedEmailDomains...)
	fd.antispam.Store(&copy)
}

// newPageData builds the common PageData for one request, using r's
// csrf_token cookie (set by csrfMiddleware on every GET) as the value a
// same-origin POST form must echo back, and r's theme cookie (falling
// back to the operator's configured default, then "dark") to render the
// theme-{dark,light,auto} class on <html> with zero FOUC — see
// requestTheme in theme.go and AI.md PART 16 "Theme Detection Flow".
func (fd *frontendDeps) newPageData(r *http.Request, csrfToken, title, description string) PageData {
	cfg := fd.cfg
	resolver := fd.resolver
	if resolver == nil {
		resolver = NewProxyResolver(nil)
	}
	overlay := resolver.IsOverlay(r)
	siteName := firstNonEmpty(cfg.Server.Branding.Title, cfg.Server.Branding.SiteName, projectInfo.Name)
	tagline := cfg.Server.Branding.Tagline
	if tagline == "" {
		tagline = projectInfo.Tagline
	}
	description = firstNonEmpty(cfg.Server.SEO.Description, cfg.Server.Branding.Description, description)
	canonical := resolver.BuildURL(r, r.URL.Path)
	robots := pageRobots(r.URL.Path)
	ogImage := pageAssetURL(r, resolver, cfg.Server.SEO.OGImage, overlay)
	logoURL := configuredBrandAssetURL(r, resolver, cfg.Server.Branding.Logo, "/branding/logo", overlay, false)
	if logoURL == "" {
		logoURL = configuredBrandAssetURL(r, resolver, cfg.Server.Branding.LogoURL, "/branding/logo", overlay, true)
	}
	faviconURL := configuredBrandAssetURL(r, resolver, cfg.Server.Branding.Favicon, "/branding/favicon", overlay, false)
	seoData, _ := json.Marshal(map[string]string{"@context": "https://schema.org", "@type": "WebSite", "name": siteName, "description": description, "url": canonical})
	onionAddress := resolver.overlayHost(OverlayTor)
	i2pAddress := resolver.overlayHost(OverlayI2P)
	// Variables are expanded BEFORE sanitizing so the sanitizer validates
	// the markup that actually renders, never a pre-substitution shape.
	footerHTML, _ := sanitize.ValidateFooterHTML(expandFooterVariables(cfg.Web.Footer.CustomHTML, footerVariables{
		currentYear:    strconv.Itoa(overlayTime(time.Now(), overlay).Year()),
		projectName:    projectInfo.Name,
		projectOrg:     "apimgr",
		projectVersion: fd.version,
		buildDateTime:  footerBuildDateTime(fd.buildDate, overlay),
		onionAddress:   onionAddress,
		i2pAddress:     i2pAddress,
	}))

	cookieName := cfg.Server.CSRF.CookieName
	if cookieName == "" {
		cookieName = "csrf_token"
	}

	lang := langFromContext(r)
	announcements := activeAnnouncements(cfg.Web.Announcements, r, time.Now())

	currentPath := r.URL.Path
	if r.URL.RawQuery != "" {
		currentPath += "?" + r.URL.RawQuery
	}

	return PageData{
		Title:                  title,
		Description:            description,
		Keywords:               strings.Join(cfg.Server.SEO.Keywords, ", "),
		Author:                 cfg.Server.SEO.Author,
		CanonicalURL:           canonical,
		Robots:                 robots,
		OGImage:                ogImage,
		TwitterHandle:          cfg.Server.SEO.TwitterHandle,
		LogoURL:                logoURL,
		FaviconURL:             faviconURL,
		StructuredData:         template.JS(seoData),
		Verification:           cfg.Server.SEO.Verification,
		SiteName:               siteName,
		Tagline:                tagline,
		ProjectOrg:             "apimgr",
		ProjectVersion:         fd.version,
		AssetStamp:             assetStamp(fd.version, fd.commitID),
		BuildDate:              fd.buildDate,
		BuildDateTime:          footerBuildDateTime(fd.buildDate, overlay),
		RepoURL:                footerRepoURL,
		CurrentYear:            overlayTime(time.Now(), overlay).Year(),
		Theme:                  requestTheme(r, cfg),
		CurrentPath:            currentPath,
		CSRFToken:              csrfToken,
		CSRFCookieName:         cookieName,
		OwnerTokenCookieExists: rCookieValue(r, ownerTokenCookieName) != "",
		ToastPosition:          cfg.Server.Notifications.WebUI.Position,
		ToastDuration:          cfg.Server.Notifications.WebUI.Duration,

		Lang:               lang,
		Dir:                i18n.Direction(lang),
		AvailableLanguages: i18n.LanguagesFor(cfg.Server.I18N.AvailableLanguages),
		I18NEnabled:        cfg.Server.I18N.Enabled,

		FooterCustomHTML: template.HTML(footerHTML),
		TorOnionAddress:  onionAddress,
		I2PAddress:       i2pAddress,
		IsTorRequest:     resolver.OverlayOf(r) == OverlayTor,

		HasConsentCookie:             hasConsentCookie(r),
		ConsentMessage:               defaultString(cfg.Server.Privacy.GetConsentMessage(), i18n.Translate(lang, "cookie_consent.message")),
		ConsentPolicyURL:             cfg.Server.Privacy.Consent.Policy.URL,
		ConsentPolicyText:            defaultString(cfg.Server.Privacy.Consent.Policy.Text, i18n.Translate(lang, "privacy.title")),
		ConsentPreferencesText:       defaultString(cfg.Server.Privacy.Consent.PreferencesText, i18n.Translate(lang, "cookie_consent.manage_preferences")),
		ConsentDeclineLabel:          defaultString(cfg.Server.Privacy.Consent.Buttons.Decline, i18n.Translate(lang, "cookie_consent.decline")),
		ConsentAcceptLabel:           defaultString(cfg.Server.Privacy.Consent.Buttons.Accept, i18n.Translate(lang, "cookie_consent.accept")),
		CookieEssentialDescription:   defaultString(cfg.Server.Privacy.Cookies.Essential.Description, i18n.Translate(lang, "cookie_consent.essential_description")),
		CookiePreferencesDescription: defaultString(cfg.Server.Privacy.Cookies.Preferences.Description, i18n.Translate(lang, "cookie_consent.preference_description")),
		CookieAnalyticsDescription:   defaultString(cfg.Server.Privacy.Cookies.Analytics.Description, i18n.Translate(lang, "cookie_consent.analytics_description")),
		CCPAEnabled:                  cfg.Server.Privacy.Data.Sold,
		Announcements:                announcements,
	}
}

func pageAssetURL(r *http.Request, resolver *ProxyResolver, raw string, overlay bool) string {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, "\\\r\n\t") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return resolver.BuildURL(r, value)
	}
	if overlay || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func configuredBrandAssetURL(r *http.Request, resolver *ProxyResolver, raw, localRoute string, overlay, allowRootURL bool) string {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, "\\\r\n\t") {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "" {
		return pageAssetURL(r, resolver, value, overlay)
	}
	if allowRootURL && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return pageAssetURL(r, resolver, value, overlay)
	}
	return resolver.BuildURL(r, localRoute)
}

func activeAnnouncements(configured config.WebAnnouncements, r *http.Request, now time.Time) []announcementView {
	if !configured.Enabled {
		return nil
	}
	dismissed := map[string]struct{}{}
	if cookie, err := r.Cookie("dismissed_announcements"); err == nil {
		value, decodeErr := url.PathUnescape(cookie.Value)
		if decodeErr != nil {
			value = cookie.Value
		}
		for _, id := range strings.Split(value, ",") {
			if id != "" {
				dismissed[id] = struct{}{}
			}
		}
	}
	var active []announcementView
	for _, item := range configured.Messages {
		if !validAnnouncementID(item.ID) || item.Message == "" {
			continue
		}
		if _, ok := dismissed[item.ID]; ok {
			continue
		}
		if item.Start != "" {
			start, err := time.Parse(time.RFC3339, item.Start)
			if err != nil || now.Before(start) {
				continue
			}
		}
		if item.End != "" {
			end, err := time.Parse(time.RFC3339, item.End)
			if err != nil || !now.Before(end) {
				continue
			}
		}
		typ := item.Type
		if typ != "info" && typ != "warning" && typ != "error" && typ != "success" {
			typ = "info"
		}
		active = append(active, announcementView{ID: item.ID, Type: typ, Title: item.Title, Message: item.Message, Dismissible: item.Dismissible})
	}
	return active
}

func validAnnouncementID(id string) bool {
	if len(id) == 0 || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// footerBuildDateTime renders the embedded build timestamp (RFC 3339 UTC,
// derived from the BuildEpoch ldflag in src/common/version) in the
// user-facing footer format. Unparseable or unset values ("N/A" on a
// `go run`/`go test` build) pass through as-is.
//
// overlay forces UTC: AI.md PART 31 "Tor Timestamp Normalization" treats
// the server's local zone as a deanonymization vector, since it narrows the
// operator's location for anyone reading an onion page.
func footerBuildDateTime(buildDate string, overlay bool) string {
	t, err := time.Parse(time.RFC3339, buildDate)
	if err != nil {
		return buildDate
	}
	return overlayTime(t, overlay).Format(footerTimeLayout)
}

// footerVariables holds the values for AI.md PART 16 "Available Footer
// Variables", the placeholder set an operator may use in
// `web.footer.custom_html`.
type footerVariables struct {
	currentYear    string
	projectName    string
	projectOrg     string
	projectVersion string
	buildDateTime  string
	// onionAddress and i2pAddress are empty unless the matching overlay is
	// enabled, running, AND has published an address (AI.md PART 31) — an
	// unpublished overlay expands to nothing rather than to a stale value.
	onionAddress string
	i2pAddress   string
}

// expandFooterVariables substitutes every AI.md PART 16 footer variable in
// html. Replacement is literal, never fmt-based, so a stray `%` in operator
// HTML can never corrupt the output; an unrecognized `{token}` is left
// untouched so the operator can see their own typo.
func expandFooterVariables(html string, v footerVariables) string {
	if html == "" {
		return ""
	}
	return strings.NewReplacer(
		"{current_year}", v.currentYear,
		"{project_name}", v.projectName,
		"{project_org}", v.projectOrg,
		"{project_version}", v.projectVersion,
		"{build_datetime}", v.buildDateTime,
		"{onion_address}", v.onionAddress,
		"{i2p_address}", v.i2pAddress,
	).Replace(html)
}

// overlayTime renders t in UTC for an overlay request and in the server's
// local zone otherwise.
func overlayTime(t time.Time, overlay bool) time.Time {
	if overlay {
		return t.UTC()
	}
	return t.Local()
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func pageRobots(path string) string {
	switch path {
	case "/", "/list", "/server/about", "/server/privacy", "/server/contact", "/server/help", "/server/terms", "/server/security", "/server/security/policy", "/server/security/thanks", "/server/dpo", "/server/docs/swagger", "/server/docs/graphql", "/server/ccpa":
		return "index,follow"
	default:
		if strings.Count(path, "/") == 2 && strings.HasSuffix(path, "/stats") && !strings.HasPrefix(path, "/server/") {
			return "index,follow"
		}
		return "noindex,nofollow"
	}
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
