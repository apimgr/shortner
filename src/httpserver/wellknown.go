// The /.well-known namespace, /robots.txt, and /llms.txt, per AI.md
// PART 11 "Well-Known Files".
package httpserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/security"
)

// wellKnownDeps carries what the well-known handlers need. installSecret
// is the persisted `installation_secret` used to derive `{security_id}`;
// it is never logged and never appears anywhere but the security.txt
// Contact line, per AI.md PART 11 "Cryptographic Keys".
type wellKnownDeps struct {
	cfg           *config.Config
	resolver      *ProxyResolver
	dataDir       string
	configDir     string
	installSecret string
}

// wellKnownAllowlist is the complete set of allowlisted `/.well-known`
// entries, per AI.md PART 11 "Well-Known Support Matrix". Anything not
// listed here returns 404 and never redirects.
var wellKnownAllowlist = []string{
	"security.txt",
	"llms.txt",
	"pgp-key.asc",
	"webfinger",
	"openid-configuration",
	"assetlinks.json",
	"apple-app-site-association",
	"mta-sts.txt",
}

// registerWellKnownRoutes mounts /robots.txt, /llms.txt, and the whole
// /.well-known subtree. The subtree is a single wildcard handler rather
// than per-entry routes so the namespace contract (allowlist-only,
// GET/HEAD only, 404 never redirect, no directory index) is enforced in
// exactly one place and cannot be bypassed by a future route addition.
//
// /.well-known/acme-challenge is deliberately NOT handled here: it is a
// protocol-owned dynamic handler served by the ACME layer (src/certmgr,
// AI.md PART 15), and per the spec's serving-order rule nothing may
// override it.
func (wd *wellKnownDeps) registerWellKnownRoutes(r chi.Router) {
	r.HandleFunc("/robots.txt", wd.robotsHandler)
	r.HandleFunc("/llms.txt", wd.llmsHandler)
	r.HandleFunc("/.well-known/*", wd.wellKnownHandler)
}

// wellKnownHandler enforces the Well-Known Namespace Contract and
// dispatches to the entry's generator.
func (wd *wellKnownDeps) wellKnownHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		apperr.SendError(w, apperr.New(apperr.CodeMethodNotAllowed))
		return
	}

	entry := strings.TrimPrefix(r.URL.Path, "/.well-known/")
	// The bare directory must not list an index, and a nested path under
	// a flat entry is not a different resource — both are 404.
	if entry == "" || strings.Contains(entry, "/") {
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
		return
	}
	if !wellKnownEnabled(wd.cfg, entry) {
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
		return
	}

	switch entry {
	case "security.txt":
		writeText(w, r, wd.securityTxt(r))
	case "llms.txt":
		wd.llmsHandler(w, r)
	case "pgp-key.asc":
		wd.pgpKeyHandler(w, r)
	default:
		// Remaining allowlisted entries are file-backed: an operator drops
		// the file in {data_dir}/web/.well-known/ and flips the toggle.
		wd.serveFileBacked(w, r, entry)
	}
}

// wellKnownEnabled reports whether entry is both allowlisted and enabled
// for this deployment. Optional entries stay disabled until the matching
// product feature is explicitly configured, per AI.md PART 11's
// "Optional-entry rule".
func wellKnownEnabled(cfg *config.Config, entry string) bool {
	allowlisted := false
	for _, e := range wellKnownAllowlist {
		if e == entry {
			allowlisted = true
			break
		}
	}
	if !allowlisted {
		return false
	}

	wk := cfg.Web.WellKnown
	switch entry {
	case "security.txt", "pgp-key.asc":
		return true
	case "llms.txt":
		return cfg.Web.LLMs.Enabled
	case "webfinger":
		return wk.Webfinger.Enabled
	case "openid-configuration":
		return wk.OpenIDConfiguration.Enabled
	case "assetlinks.json":
		return wk.Assetlinks.Enabled
	case "apple-app-site-association":
		return wk.AppleAppSiteAssociation.Enabled
	case "mta-sts.txt":
		return wk.MTASTS.Enabled
	}
	return false
}

// wellKnownContentType returns the Content-Type each entry must be served
// with, per the Well-Known Support Matrix.
func wellKnownContentType(entry string) string {
	switch entry {
	case "webfinger":
		return "application/jrd+json"
	case "openid-configuration", "assetlinks.json", "apple-app-site-association":
		return "application/json"
	case "pgp-key.asc":
		return "application/pgp-keys"
	}
	return "text/plain; charset=utf-8"
}

// serveFileBacked serves an allowlisted entry from
// {data_dir}/web/.well-known/. The entry name is matched against the
// allowlist before this runs, so no user-controlled path segment ever
// reaches the filesystem.
func (wd *wellKnownDeps) serveFileBacked(w http.ResponseWriter, r *http.Request, entry string) {
	body, err := os.ReadFile(filepath.Join(wd.dataDir, "web", ".well-known", entry))
	if err != nil {
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
		return
	}
	w.Header().Set("Content-Type", wellKnownContentType(entry))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// pgpKeyHandler serves the project's ASCII-armored public key, or 404
// when no keypair has been generated, per AI.md PART 11 "Public Pages".
func (wd *wellKnownDeps) pgpKeyHandler(w http.ResponseWriter, r *http.Request) {
	body, err := os.ReadFile(wd.pgpPublicKeyPath())
	if err != nil {
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
		return
	}
	w.Header().Set("Content-Type", "application/pgp-keys")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// pgpPublicKeyPath is where `--maintenance pgp generate` stores the
// public key, per AI.md PART 11 "GPG Keypair Management".
func (wd *wellKnownDeps) pgpPublicKeyPath() string {
	return filepath.Join(wd.configDir, "security", "pgp.pub.asc")
}

// hasPGPKey reports whether a keypair exists, which decides whether
// security.txt carries an Encryption line.
func (wd *wellKnownDeps) hasPGPKey() bool {
	info, err := os.Stat(wd.pgpPublicKeyPath())
	return err == nil && !info.IsDir()
}

// writeText writes a generated plain-text well-known body.
func writeText(w http.ResponseWriter, r *http.Request, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// robotsHandler serves /robots.txt, generated from `web.robots`, per
// AI.md PART 11 "robots.txt".
func (wd *wellKnownDeps) robotsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		apperr.SendError(w, apperr.New(apperr.CodeMethodNotAllowed))
		return
	}

	var b strings.Builder
	b.WriteString("User-agent: *\n")
	for _, path := range wd.cfg.Web.Robots.Allow {
		fmt.Fprintf(&b, "Allow: %s\n", path)
	}
	for _, path := range wd.cfg.Web.Robots.Deny {
		fmt.Fprintf(&b, "Disallow: %s\n", path)
	}
	fmt.Fprintf(&b, "Sitemap: %s\n", wd.resolver.BuildURL(r, "/sitemap.xml"))
	writeText(w, r, b.String())
}

// securityTxt renders the RFC 9116 file. Contact lines are emitted in the
// spec's preference order: the repo's private vulnerability reporting
// URL, then this instance's security-report form carrying the rotating
// {security_id}, then the mailto: CC address.
func (wd *wellKnownDeps) securityTxt(r *http.Request) string {
	sec := wd.cfg.Web.Security
	var b strings.Builder

	if url := strings.TrimSpace(sec.ReportURL); url != "" {
		fmt.Fprintf(&b, "Contact: %s\n", url)
	}
	if id := security.SecurityID(wd.installSecret, time.Now()); id != "" {
		fmt.Fprintf(&b, "Contact: %s\n", wd.resolver.BuildURL(r, "/server/contact?security_id="+id))
	}
	if contact := wd.securityContact(r); contact != "" {
		fmt.Fprintf(&b, "Contact: mailto:%s\n", contact)
	}

	if sec.Expires != "" {
		fmt.Fprintf(&b, "Expires: %s\n", sec.Expires)
	} else {
		// Auto-calculated one year from generation, per AI.md PART 11's
		// `expires: "{1year}"` default.
		fmt.Fprintf(&b, "Expires: %s\n", time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339))
	}

	if wd.hasPGPKey() && sec.PublishPGPKey {
		fmt.Fprintf(&b, "Encryption: %s\n", wd.resolver.BuildURL(r, "/.well-known/pgp-key.asc"))
		for _, ks := range sec.Keyservers {
			fmt.Fprintf(&b, "Encryption: %s\n", ks)
		}
	}
	if sec.PreferredLanguages != "" {
		fmt.Fprintf(&b, "Preferred-Languages: %s\n", sec.PreferredLanguages)
	}
	fmt.Fprintf(&b, "Policy: %s\n", wd.resolver.BuildURL(r, "/server/security"))
	fmt.Fprintf(&b, "Acknowledgments: %s\n", wd.resolver.BuildURL(r, "/server/security/thanks"))
	fmt.Fprintf(&b, "Canonical: %s\n", wd.resolver.BuildURL(r, "/.well-known/security.txt"))

	return b.String()
}

// securityContact resolves the CC email address, defaulting to
// security@{fqdn} for the host this client actually used, per AI.md
// PART 11's `contact: "security@{fqdn}"` default and its per-request URL
// resolution rule.
func (wd *wellKnownDeps) securityContact(r *http.Request) string {
	if c := strings.TrimSpace(wd.cfg.Web.Security.Contact); c != "" {
		return c
	}
	if c := strings.TrimSpace(wd.cfg.Server.Contact.Security.Email); c != "" {
		return c
	}
	_, fqdn, _ := wd.resolver.GetURLVars(r)
	if fqdn == "" {
		return ""
	}
	return "security@" + fqdn
}

// llmsHandler serves the AI-agent discovery file at both /llms.txt and
// /.well-known/llms.txt, per AI.md PART 11 "llms.txt (AI Discovery)".
func (wd *wellKnownDeps) llmsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		apperr.SendError(w, apperr.New(apperr.CodeMethodNotAllowed))
		return
	}
	if !wd.cfg.Web.LLMs.Enabled {
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
		return
	}
	writeText(w, r, wd.llmsTxt(r))
}

// llmsEndpoints is the advertised route list. Per AI.md PART 11's
// "Endpoint Inclusion Rules", public and authenticated API routes plus
// the health endpoint are listed; metrics endpoints are NEVER advertised.
var llmsEndpoints = []struct {
	method string
	path   string
	desc   string
}{
	{"GET", "/server/healthz", "Health check (no auth)"},
	{"GET", "/server/about", "Server information (no auth)"},
	{"GET", "/links", "List public links (no auth)"},
	{"POST", "/links", "Create a short link (no auth; returns an owner token)"},
	{"GET", "/links/{slug}", "Link details (no auth)"},
	{"PATCH", "/links/{slug}", "Update a link (owner or operator token)"},
	{"DELETE", "/links/{slug}", "Delete a link (owner or operator token)"},
	{"GET", "/links/{slug}/stats", "Click analytics (no auth)"},
}

// llmsTxt renders the discovery document from live config and the route
// table.
func (wd *wellKnownDeps) llmsTxt(r *http.Request) string {
	cfg := wd.cfg
	apiBase := wd.resolver.BuildURL(r, "/api/"+cfg.Server.APIVersion)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", defaultString(cfg.Server.Branding.SiteName, "shortner"))
	if desc := strings.TrimSpace(cfg.Server.SEO.Description); desc != "" {
		fmt.Fprintf(&b, "> %s\n", desc)
	}

	b.WriteString("\n## API\n")
	fmt.Fprintf(&b, "Base URL: %s\n", apiBase)
	b.WriteString("Authentication: Bearer token (server token from server.yml, or resource owner token issued on resource creation)\n")
	fmt.Fprintf(&b, "Rate limit: %d read and %d write requests/minute per IP\n",
		cfg.Server.RateLimit.Read.Requests, cfg.Server.RateLimit.Write.Requests)

	if cfg.Web.LLMs.IncludeEndpoints {
		b.WriteString("\n## Endpoints\n")
		for _, e := range llmsEndpoints {
			fmt.Fprintf(&b, "- %s %s - %s\n", e.method, e.path, e.desc)
		}
	}

	b.WriteString("\n## Capabilities\n")
	for _, c := range wd.capabilities() {
		fmt.Fprintf(&b, "- %s\n", c)
	}

	for _, section := range cfg.Web.LLMs.CustomSections {
		fmt.Fprintf(&b, "\n%s\n", section)
	}

	b.WriteString("\n## Contact\n")
	fmt.Fprintf(&b, "API issues: %s\n", wd.resolver.BuildURL(r, "/server/contact"))
	if contact := wd.securityContact(r); contact != "" {
		fmt.Fprintf(&b, "Security: %s\n", contact)
	}
	return b.String()
}

// capabilities lists what this deployment can do, derived from enabled
// features, per AI.md PART 11's "Capabilities derived from enabled
// features".
func (wd *wellKnownDeps) capabilities() []string {
	cfg := wd.cfg
	caps := []string{
		"Create short links with an auto-generated or custom slug",
		"Resolve a slug to its destination (expired links return 410 Gone)",
		"Per-link click analytics with anonymized visitor IPs",
	}
	if cfg.Server.GeoIP.Enabled {
		caps = append(caps, "Approximate country/region on click analytics (GeoIP)")
	}
	if cfg.Server.TLS.Enabled {
		caps = append(caps, "HTTPS with automatic certificate management")
	}
	if standards := cfg.Server.Compliance.EnabledStandards(); len(standards) > 0 {
		sort.Strings(standards)
		caps = append(caps, "Compliance mode: "+strings.Join(standards, ", "))
	}
	return caps
}
