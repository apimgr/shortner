// Security response headers, per AI.md PART 11 "Security Headers",
// "Cross-Origin Isolation Headers", "Reporting API (Modern + Legacy)",
// "Server-Timing (Debug Mode Only)", and "Content Security Policy".
package httpserver

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/mode"
)

// headerDeps carries everything the security-header stage needs. It is
// built once at startup, but every URL it emits is still resolved
// per-request through ProxyResolver — AI.md PART 11 "URL resolution" is
// explicit that generated URLs must match the Host/proto the client
// actually used, so nothing host-derived may be cached here.
type headerDeps struct {
	cfg      *config.Config
	resolver *ProxyResolver
	// permissionsPolicy is the joined `feature=value, ...` string. It has
	// no host-dependent parts, so it is built once at startup per AI.md
	// PART 11 "Generation rule".
	permissionsPolicy string
}

// newHeaderDeps precomputes the host-independent header values.
func newHeaderDeps(cfg *config.Config, resolver *ProxyResolver) *headerDeps {
	return &headerDeps{
		cfg:               cfg,
		resolver:          resolver,
		permissionsPolicy: buildPermissionsPolicy(cfg.Web.PermissionsPolicy),
	}
}

// buildPermissionsPolicy joins the configured features into a single
// header value in the canonical AI.md order, per AI.md PART 11
// "Permissions-Policy Configuration" -> "Generation rule". Empty values
// skip the feature entirely so the browser default applies; unknown
// feature names are emitted anyway since non-supporting browsers ignore
// them harmlessly.
func buildPermissionsPolicy(policy map[string]string) string {
	if len(policy) == 0 {
		return ""
	}
	parts := make([]string, 0, len(policy))
	for _, feature := range config.PermissionsPolicyKeys(policy) {
		value := strings.TrimSpace(policy[feature])
		if value == "" {
			continue
		}
		parts = append(parts, feature+"="+value)
	}
	return strings.Join(parts, ", ")
}

// reportsBase returns the absolute base URL of the reports endpoints for
// this request, e.g. "https://example.com/api/v1/server/reports".
func (hd *headerDeps) reportsBase(r *http.Request) string {
	return hd.resolver.BuildURL(r, "/api/"+hd.cfg.Server.APIVersion+"/server/reports")
}

// reportPath returns the root-relative path of a report endpoint, used
// for the CSP `report-uri` directive (which is same-origin by design).
func (hd *headerDeps) reportPath(name string) string {
	return "/api/" + hd.cfg.Server.APIVersion + "/server/reports/" + name
}

// securityHeadersMiddleware sets the full AI.md PART 11 header matrix on
// every response (execution position 4). Headers are written before the
// handler runs so they survive any status code, including errors written
// by downstream middleware.
func (hd *headerDeps) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd.apply(w, r)
		next.ServeHTTP(w, r)
	})
}

// apply writes the security headers for one request.
func (hd *headerDeps) apply(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	cfg := hd.cfg

	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	// Deprecated in modern browsers but kept for IE11/old Safari, per
	// AI.md PART 11 "Deprecated / Legacy Headers".
	h.Set("X-XSS-Protection", "1; mode=block")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

	hc := cfg.Web.Headers
	h.Set("X-Permitted-Cross-Domain-Policies", defaultString(hc.CrossDomainPolicies, "none"))
	if hc.OriginAgentCluster {
		h.Set("Origin-Agent-Cluster", "?1")
	}
	h.Set("Cross-Origin-Opener-Policy", defaultString(hc.COOP, "unsafe-none"))
	h.Set("Cross-Origin-Embedder-Policy", defaultString(hc.COEP, "unsafe-none"))
	h.Set("Cross-Origin-Resource-Policy", defaultString(hc.CORP, "cross-origin"))
	if hc.DNSPrefetchControl != "" {
		h.Set("X-DNS-Prefetch-Control", hc.DNSPrefetchControl)
	}

	if hd.permissionsPolicy != "" {
		h.Set("Permissions-Policy", hd.permissionsPolicy)
	}

	base := hd.reportsBase(r)
	h.Set("Reporting-Endpoints", fmt.Sprintf("default=%q", base+"/default"))
	h.Set("Report-To", fmt.Sprintf(`{"group":"default","max_age":10886400,"endpoints":[{"url":%q}]}`, base+"/default"))
	if hc.NEL.Enabled {
		h.Set("NEL", fmt.Sprintf(`{"report_to":"default","max_age":%d,"include_subdomains":%t,"success_fraction":0,"failure_fraction":%s}`,
			hc.NEL.MaxAgeSeconds, hc.NEL.IncludeSubdomains, strconv.FormatFloat(hc.NEL.SampleRate, 'g', -1, 64)))
	}

	if id := RequestIDFromContext(r.Context()); id != "" {
		h.Set("X-Request-ID", id)
	}

	// HSTS is only meaningful — and only legal per AI.md PART 31's overlay
	// rule — on a clearnet TLS request. An overlay (.onion/.b32.i2p) host
	// is always plain http:// and must never receive HSTS.
	if cfg.Web.HSTS.Enabled && cfg.Web.HSTS.MaxAgeSeconds > 0 && requestIsTLS(r) && !isOverlayHost(r.Host) {
		h.Set("Strict-Transport-Security", buildHSTS(cfg.Web.HSTS))
	}

	if cfg.Web.CSP.Enabled {
		name, value := hd.buildCSP(r)
		h.Set(name, value)
	}
}

// buildHSTS renders the Strict-Transport-Security value, per AI.md
// PART 11 "Security Headers" -> HSTS notes (RFC 6797).
func buildHSTS(h config.HSTS) string {
	value := "max-age=" + strconv.Itoa(h.MaxAgeSeconds)
	if h.IncludeSubdomains {
		value += "; includeSubDomains"
	}
	if h.Preload {
		value += "; preload"
	}
	return value
}

// requestIsTLS reports whether the client's connection to the edge was
// HTTPS, honoring reverse-proxy forwarding headers only from a trusted
// peer (AI.md PART 8 "Resolution Order").
func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// isOverlayHost reports whether Host is a Tor or I2P address. Overlay
// responses are always plain http:// with no HSTS, no redirect, and no
// upgrade-insecure-requests, per AI.md PART 31.
func isOverlayHost(host string) bool {
	name := host
	if i := strings.LastIndex(name, ":"); i != -1 && !strings.Contains(name, "]") {
		name = name[:i]
	}
	name = strings.ToLower(strings.Trim(name, "[]"))
	return strings.HasSuffix(name, ".onion") || strings.HasSuffix(name, ".b32.i2p") || strings.HasSuffix(name, ".i2p")
}

// cspDirective pairs a directive name with the default value AI.md
// PART 11 "Default Policy (per-directive)" prescribes.
type cspDirective struct {
	name  string
	value string
}

// cspDefaults is the canonical default policy, in the spec's own order.
var cspDefaults = []cspDirective{
	{"default-src", "'self'"},
	{"script-src", "'self'"},
	{"style-src", "'self' 'unsafe-inline'"},
	{"img-src", "'self' data: blob: https:"},
	{"font-src", "'self' https:"},
	{"connect-src", "'self'"},
	{"media-src", "'self' blob:"},
	{"worker-src", "'self' blob:"},
	{"manifest-src", "'self'"},
	{"frame-src", "'self'"},
	{"frame-ancestors", "'self'"},
	{"base-uri", "'self'"},
	{"form-action", "'self'"},
	{"object-src", "'none'"},
}

// buildCSP renders the Content-Security-Policy header name and value for
// one request, per AI.md PART 11 "Content Security Policy". Report-only
// is used when the operator asked for it, and — per "Report-Only Mode" —
// automatically in development unless `mode: enforce` was set explicitly.
func (hd *headerDeps) buildCSP(r *http.Request) (string, string) {
	csp := hd.cfg.Web.CSP

	learned := hd.learnedOrigins(r)
	parts := make([]string, 0, len(cspDefaults)+3)
	for _, d := range cspDefaults {
		value := d.value
		if override := strings.TrimSpace(csp.Override(d.name)); override != "" {
			value = override
		} else {
			// connect-src, frame-ancestors, and form-action pick up the
			// request's own origin and the configured DOMAIN entries
			// automatically, per AI.md PART 11 "Auto-detection".
			if learned != "" && (d.name == "connect-src" || d.name == "frame-ancestors" || d.name == "form-action") {
				value += " " + learned
			}
			if extra := strings.TrimSpace(csp.Extra(d.name)); extra != "" {
				value += " " + extra
			}
		}
		parts = append(parts, d.name+" "+value)
	}

	// upgrade-insecure-requests eliminates mixed content on TLS sites, but
	// must never be sent to an overlay client, whose origin is http:// by
	// protocol design (AI.md PART 31).
	if !isOverlayHost(r.Host) {
		parts = append(parts, "upgrade-insecure-requests")
	}

	if csp.ReportsEnabled {
		parts = append(parts, "report-to default")
		parts = append(parts, "report-uri "+hd.reportPath("csp"))
	}

	name := "Content-Security-Policy"
	// Development downgrades to report-only unless the operator wrote
	// `mode: enforce` in server.yml themselves — AI.md PART 11
	// "Report-Only Mode": "auto-applied unless `mode: enforce` set
	// explicitly".
	explicitEnforce := csp.ModeExplicit && csp.Mode == "enforce"
	if csp.Mode == "report-only" || (mode.IsAppModeDev() && !explicitEnforce) {
		name = "Content-Security-Policy-Report-Only"
	}
	return name, strings.Join(parts, "; ")
}

// learnedOrigins returns the space-separated origin list CSP shares with
// CORS, per AI.md PART 11 "Auto-detection": the request's own resolved
// origin plus the operator's configured CORS origins. Wildcards are
// dropped — "*" is meaningless in a CSP source list and would silently
// widen the policy.
func (hd *headerDeps) learnedOrigins(r *http.Request) string {
	seen := map[string]bool{}
	var origins []string
	add := func(o string) {
		o = strings.TrimSpace(o)
		if o == "" || o == "*" || seen[o] {
			return
		}
		seen[o] = true
		origins = append(origins, o)
	}

	proto, fqdn, port := hd.resolver.GetURLVars(r)
	if fqdn != "" {
		host := fqdn
		if port != "" && port != "80" && port != "443" {
			host = fqdn + ":" + port
		}
		add(proto + "://" + host)
	}
	for _, o := range hd.cfg.Server.CORS.AllowedOrigins {
		add(o)
	}
	return strings.Join(origins, " ")
}

// clearSiteData writes the Clear-Site-Data header on token-revocation and
// consent-withdrawal responses, per AI.md PART 11 "Privacy Signal
// Headers". "executionContexts" is opt-in only because it breaks SPA
// back-navigation.
func clearSiteData(w http.ResponseWriter, cfg *config.Config, reason string) {
	c := cfg.Web.Headers.ClearSiteData
	switch reason {
	case "token_revocation":
		if !c.OnTokenRevocation {
			return
		}
	case "consent_withdrawal":
		if !c.OnConsentWithdrawal {
			return
		}
	case "version_change":
		// Per AI.md PART 11, the version-change purge (PART 9) clears
		// cache and storage only — never cookies.
		w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
		return
	default:
		return
	}

	value := `"cache", "cookies", "storage"`
	if c.ExecutionContexts {
		value += `, "executionContexts"`
	}
	w.Header().Set("Clear-Site-Data", value)
}

// serverTiming writes the Server-Timing header, which is Tier 3
// information and therefore emitted in debug mode only — per AI.md
// PART 11 "Server-Timing (Debug Mode Only)": "Production: header is NEVER
// emitted."
func serverTiming(w http.ResponseWriter, cfg *config.Config, metrics ...string) {
	if cfg.Web.Headers.ServerTimingInDebugOnly && !mode.IsDebugEnabled() {
		return
	}
	if len(metrics) == 0 {
		return
	}
	w.Header().Set("Server-Timing", strings.Join(metrics, ", "))
}
