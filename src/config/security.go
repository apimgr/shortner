// Security, header, well-known, and compliance configuration blocks, per
// AI.md PART 11 "Security Headers", "Cross-Origin Isolation Headers",
// "Privacy Signal Headers", "Sec-Fetch-* Request Validation", "Reporting
// API", "Content Security Policy", "Well-Known Files", "Security Reports",
// "Abuse Detection", and "IP Block Management".
//
// Everything here is operator-configurable from server.yml alone: there is
// no admin web UI and no admin API route for any of it, per AI.md PART 11
// "Security Administration (config file)".
package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sort"
)

// Security holds `server.security`, per AI.md PART 11 "Cryptographic Keys"
// (the `encryption_key` row), "Abuse Detection", and "IP Block Management".
type Security struct {
	// EncryptionKey is the canonical at-rest AES-256-GCM key, base64
	// (standard encoding) of 32 random bytes. Auto-generated on first run
	// by EnsureEncryptionKey. NEVER returned by any API response and never
	// logged, per AI.md PART 11 "Cryptographic Keys".
	EncryptionKey string `yaml:"encryption_key"`
	// Allowlist entries bypass blocklists, rate limiting, GeoIP country
	// blocking, and auto-blocking — never CSRF, path security, or TLS.
	Allowlist []AllowlistEntry `yaml:"allowlist"`
	// BlockedIPs are permanent, config-file-only blocks. Temporary blocks
	// are created at runtime by abuse detection and are never persisted
	// here, per AI.md PART 11 "IP Block Management" -> "Block Types".
	BlockedIPs     []BlockedIPEntry `yaml:"blocked_ips"`
	AbuseDetection AbuseDetection   `yaml:"abuse_detection"`
}

// AllowlistEntry is one trusted IP/CIDR, per AI.md PART 11 "IP Block
// Management" -> "Data Model". A bare IP auto-expands to /32 (IPv4) or
// /128 (IPv6) at load time.
type AllowlistEntry struct {
	CIDR        string `yaml:"cidr" json:"cidr"`
	Description string `yaml:"description" json:"description"`
}

// BlockedIPEntry is one permanently blocked IP/CIDR.
type BlockedIPEntry struct {
	CIDR   string `yaml:"cidr" json:"cidr"`
	Reason string `yaml:"reason" json:"reason"`
}

// AbuseDetection holds `server.security.abuse_detection`, per AI.md
// PART 11 "Abuse Detection" -> "Detection Configuration".
type AbuseDetection struct {
	Enabled      bool         `yaml:"enabled"`
	RequestFlood RequestFlood `yaml:"request_flood"`
	// AutoBlockIP adds a temporary block when a flood is detected.
	AutoBlockIP bool `yaml:"auto_block_ip"`
	// AutoAlert writes the detection to the audit log so the configured
	// alert transports can pick it up.
	AutoAlert bool `yaml:"auto_alert"`
}

// RequestFlood configures flood detection: Multiplier x the applicable
// rate-limit allowance within one window triggers a block for
// BlockDuration.
type RequestFlood struct {
	Multiplier    int    `yaml:"multiplier"`
	BlockDuration string `yaml:"block_duration"`
}

// HSTS holds `web.hsts`, per AI.md PART 11 "Security Header Config".
type HSTS struct {
	Enabled           bool `yaml:"enabled"`
	MaxAgeSeconds     int  `yaml:"max_age_seconds"`
	IncludeSubdomains bool `yaml:"include_subdomains"`
	Preload           bool `yaml:"preload"`
}

// Headers holds `web.headers` — the modern cross-origin, privacy-signal,
// Sec-Fetch, Server-Timing, Clear-Site-Data, and NEL switches.
type Headers struct {
	COOP                    string        `yaml:"coop"`
	COEP                    string        `yaml:"coep"`
	CORP                    string        `yaml:"corp"`
	OriginAgentCluster      bool          `yaml:"origin_agent_cluster"`
	CrossDomainPolicies     string        `yaml:"cross_domain_policies"`
	DNSPrefetchControl      string        `yaml:"dns_prefetch_control"`
	HonorSecGPC             bool          `yaml:"honor_sec_gpc"`
	HonorDNT                bool          `yaml:"honor_dnt"`
	SecFetchValidation      bool          `yaml:"sec_fetch_validation"`
	ServerTimingInDebugOnly bool          `yaml:"server_timing_in_debug_only"`
	ClearSiteData           ClearSiteData `yaml:"clear_site_data"`
	NEL                     NEL           `yaml:"nel"`
}

// ClearSiteData holds `web.headers.clear_site_data`.
type ClearSiteData struct {
	OnTokenRevocation   bool `yaml:"on_token_revocation"`
	OnConsentWithdrawal bool `yaml:"on_consent_withdrawal"`
	// ExecutionContexts is opt-in only — it breaks SPA back-navigation.
	ExecutionContexts bool `yaml:"execution_contexts"`
}

// NEL holds `web.headers.nel` (Network Error Logging).
type NEL struct {
	Enabled           bool    `yaml:"enabled"`
	MaxAgeSeconds     int     `yaml:"max_age_seconds"`
	IncludeSubdomains bool    `yaml:"include_subdomains"`
	SampleRate        float64 `yaml:"sample_rate"`
}

// Reports holds `web.reports`, per AI.md PART 11 "Security Header Config".
type Reports struct {
	RateLimitPerMinute  int `yaml:"rate_limit_per_minute"`
	RateLimitPerIPBurst int `yaml:"rate_limit_per_ip_burst"`
}

// CSP holds `web.csp`, per AI.md PART 11 "Content Security Policy" ->
// "Configuration (per-directive append)". Every directive supports both an
// `_extra` append and an `_override` replacement.
type CSP struct {
	Enabled bool `yaml:"enabled"`
	// Mode is enforce | report-only.
	Mode string `yaml:"mode"`
	// ModeExplicit records that the operator wrote `mode:` in server.yml
	// rather than inheriting Default()'s value. AI.md PART 11
	// "Report-Only Mode" auto-applies report-only in development "unless
	// `mode: enforce` set explicitly" — without this flag the enforcing
	// default would make that downgrade unreachable. Never serialized.
	ModeExplicit bool `yaml:"-"`

	DefaultSrcExtra     string `yaml:"default_src_extra"`
	ScriptSrcExtra      string `yaml:"script_src_extra"`
	StyleSrcExtra       string `yaml:"style_src_extra"`
	ImgSrcExtra         string `yaml:"img_src_extra"`
	FontSrcExtra        string `yaml:"font_src_extra"`
	ConnectSrcExtra     string `yaml:"connect_src_extra"`
	MediaSrcExtra       string `yaml:"media_src_extra"`
	WorkerSrcExtra      string `yaml:"worker_src_extra"`
	ManifestSrcExtra    string `yaml:"manifest_src_extra"`
	FrameSrcExtra       string `yaml:"frame_src_extra"`
	FrameAncestorsExtra string `yaml:"frame_ancestors_extra"`
	BaseURIExtra        string `yaml:"base_uri_extra"`
	FormActionExtra     string `yaml:"form_action_extra"`
	ObjectSrcExtra      string `yaml:"object_src_extra"`

	DefaultSrcOverride     string `yaml:"default_src_override"`
	ScriptSrcOverride      string `yaml:"script_src_override"`
	StyleSrcOverride       string `yaml:"style_src_override"`
	ImgSrcOverride         string `yaml:"img_src_override"`
	FontSrcOverride        string `yaml:"font_src_override"`
	ConnectSrcOverride     string `yaml:"connect_src_override"`
	MediaSrcOverride       string `yaml:"media_src_override"`
	WorkerSrcOverride      string `yaml:"worker_src_override"`
	ManifestSrcOverride    string `yaml:"manifest_src_override"`
	FrameSrcOverride       string `yaml:"frame_src_override"`
	FrameAncestorsOverride string `yaml:"frame_ancestors_override"`
	BaseURIOverride        string `yaml:"base_uri_override"`
	FormActionOverride     string `yaml:"form_action_override"`
	ObjectSrcOverride      string `yaml:"object_src_override"`

	ReportsEnabled    bool    `yaml:"reports_enabled"`
	ReportsSampleRate float64 `yaml:"reports_sample_rate"`
}

// Extra returns the operator's per-directive append string for the given
// CSP directive name ("script-src", "frame-ancestors", ...), or "" when
// the directive has no configured extension. Per AI.md PART 11
// "Configuration (per-directive append)", these values are added to the
// default so an operator never has to redefine the whole policy.
func (c CSP) Extra(directive string) string {
	switch directive {
	case "default-src":
		return c.DefaultSrcExtra
	case "script-src":
		return c.ScriptSrcExtra
	case "style-src":
		return c.StyleSrcExtra
	case "img-src":
		return c.ImgSrcExtra
	case "font-src":
		return c.FontSrcExtra
	case "connect-src":
		return c.ConnectSrcExtra
	case "media-src":
		return c.MediaSrcExtra
	case "worker-src":
		return c.WorkerSrcExtra
	case "manifest-src":
		return c.ManifestSrcExtra
	case "frame-src":
		return c.FrameSrcExtra
	case "frame-ancestors":
		return c.FrameAncestorsExtra
	case "base-uri":
		return c.BaseURIExtra
	case "form-action":
		return c.FormActionExtra
	case "object-src":
		return c.ObjectSrcExtra
	}
	return ""
}

// Override returns the operator's replacement value for the given CSP
// directive, or "" to keep the spec default. Per AI.md PART 11
// "Override-style: REPLACE the directive instead of appending", an
// override suppresses both the default and any configured extra.
func (c CSP) Override(directive string) string {
	switch directive {
	case "default-src":
		return c.DefaultSrcOverride
	case "script-src":
		return c.ScriptSrcOverride
	case "style-src":
		return c.StyleSrcOverride
	case "img-src":
		return c.ImgSrcOverride
	case "font-src":
		return c.FontSrcOverride
	case "connect-src":
		return c.ConnectSrcOverride
	case "media-src":
		return c.MediaSrcOverride
	case "worker-src":
		return c.WorkerSrcOverride
	case "manifest-src":
		return c.ManifestSrcOverride
	case "frame-src":
		return c.FrameSrcOverride
	case "frame-ancestors":
		return c.FrameAncestorsOverride
	case "base-uri":
		return c.BaseURIOverride
	case "form-action":
		return c.FormActionOverride
	case "object-src":
		return c.ObjectSrcOverride
	}
	return ""
}

// Robots holds `web.robots`, per AI.md PART 11 "robots.txt".
type Robots struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// WebSecurity holds `web.security`, per AI.md PART 11 "security.txt
// (RFC 9116)".
type WebSecurity struct {
	// ReportURL is the primary reporting channel: the repository's private
	// vulnerability reporting form. Listed first in security.txt.
	ReportURL string `yaml:"report_url"`
	// Contact is the secondary/CC mailto address. The "mailto:" prefix is
	// added automatically when rendering security.txt.
	Contact string `yaml:"contact"`
	// Expires is the RFC 9116 Expires value. Empty means "auto-calculate
	// one year from the request", which keeps the file permanently valid
	// without an operator ever having to touch it.
	Expires string `yaml:"expires"`
	// Keyservers receive the project's PGP public key on generate/rotate.
	Keyservers []string `yaml:"keyservers"`
	// PublishPGPKey controls whether security.txt advertises the
	// `Encryption:` line and whether /.well-known/pgp-key.asc serves.
	PublishPGPKey bool `yaml:"publish_pgp_key"`
	// PreferredLanguages is the RFC 9116 Preferred-Languages value.
	PreferredLanguages string `yaml:"preferred_languages"`
	// Policy is the disclosure-policy body rendered at
	// /server/security/policy. Empty uses the built-in default text.
	Policy string `yaml:"policy"`
	// Thanks lists researchers who opted into credit, rendered at
	// /server/security/thanks.
	Thanks []SecurityThanks `yaml:"thanks"`
}

// SecurityThanks is one acknowledgment entry on /server/security/thanks.
type SecurityThanks struct {
	Name   string `yaml:"name"`
	Year   int    `yaml:"year"`
	Credit string `yaml:"credit"`
}

// LLMs holds `web.llms`, per AI.md PART 11 "llms.txt (AI Discovery)".
type LLMs struct {
	Enabled          bool     `yaml:"enabled"`
	IncludeEndpoints bool     `yaml:"include_endpoints"`
	IncludeSchemas   bool     `yaml:"include_schemas"`
	CustomSections   []string `yaml:"custom_sections"`
}

// WellKnown holds `web.well_known`, per AI.md PART 11 "Well-Known Support
// Matrix". Every optional entry is disabled by default and MUST only be
// enabled when the matching product feature actually exists.
type WellKnown struct {
	// UnsupportedBehavior is fixed at 404 by the Well-Known Namespace
	// Contract ("Unknown entries never redirect"); the key exists so the
	// rendered server.yml documents the behavior.
	UnsupportedBehavior     int             `yaml:"unsupported_behavior"`
	Webfinger               WellKnownToggle `yaml:"webfinger"`
	OpenIDConfiguration     WellKnownToggle `yaml:"openid_configuration"`
	Assetlinks              WellKnownToggle `yaml:"assetlinks"`
	AppleAppSiteAssociation WellKnownToggle `yaml:"apple_app_site_association"`
	MTASTS                  WellKnownToggle `yaml:"mta_sts"`
}

// WellKnownToggle is a single optional well-known entry switch.
type WellKnownToggle struct {
	Enabled bool `yaml:"enabled"`
}

// PermissionsPolicyOrder is the canonical emission order for the
// Permissions-Policy header, matching the key order of AI.md PART 11
// "Permissions-Policy Configuration". Keys an operator adds that are not
// in this list are emitted afterwards in sorted order, so an unknown (not
// yet standardized) feature name still reaches the browser.
var PermissionsPolicyOrder = []string{
	"accelerometer",
	"ambient-light-sensor",
	"battery",
	"camera",
	"display-capture",
	"geolocation",
	"gyroscope",
	"hid",
	"idle-detection",
	"magnetometer",
	"microphone",
	"midi",
	"screen-wake-lock",
	"serial",
	"usb",
	"xr-spatial-tracking",
	"attribution-reporting",
	"browsing-topics",
	"interest-cohort",
	"autoplay",
	"encrypted-media",
	"fullscreen",
	"payment",
	"picture-in-picture",
	"publickey-credentials-get",
	"storage-access",
	"web-share",
}

// DefaultPermissionsPolicy returns the spec-default per-feature values.
// Sensors, capture devices, and every advertising/tracking proposal are
// locked to "()"; only the features the spec itself uses are scoped to
// "(self)". This project's IDEA.md declares no camera/microphone/
// geolocation use, so nothing is pre-unlocked.
func DefaultPermissionsPolicy() map[string]string {
	return map[string]string{
		"accelerometer":        "()",
		"ambient-light-sensor": "()",
		"battery":              "()",
		"camera":               "()",
		"display-capture":      "()",
		"geolocation":          "()",
		"gyroscope":            "()",
		"hid":                  "()",
		"idle-detection":       "()",
		"magnetometer":         "()",
		"microphone":           "()",
		"midi":                 "()",
		"screen-wake-lock":     "()",
		"serial":               "()",
		"usb":                  "()",
		"xr-spatial-tracking":  "()",

		"attribution-reporting": "()",
		"browsing-topics":       "()",
		"interest-cohort":       "()",

		"autoplay":                  "(self)",
		"encrypted-media":           "(self)",
		"fullscreen":                "(self)",
		"payment":                   "(self)",
		"picture-in-picture":        "(self)",
		"publickey-credentials-get": "(self)",
		"storage-access":            "(self)",
		"web-share":                 "(self)",
	}
}

// PermissionsPolicyKeys returns policy's keys in emission order: the
// canonical PermissionsPolicyOrder first, then any operator-added extras
// sorted alphabetically for a stable header value.
func PermissionsPolicyKeys(policy map[string]string) []string {
	seen := make(map[string]bool, len(PermissionsPolicyOrder))
	out := make([]string, 0, len(policy))
	for _, k := range PermissionsPolicyOrder {
		seen[k] = true
		if _, ok := policy[k]; ok {
			out = append(out, k)
		}
	}
	extra := make([]string, 0)
	for k := range policy {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// DefaultSecurity returns the `server.security` defaults. Abuse detection
// is on with the spec's 10x multiplier and 1h block; the allowlist and the
// permanent blocklist start empty.
func DefaultSecurity() Security {
	return Security{
		Allowlist:  []AllowlistEntry{},
		BlockedIPs: []BlockedIPEntry{},
		AbuseDetection: AbuseDetection{
			Enabled: true,
			RequestFlood: RequestFlood{
				Multiplier:    10,
				BlockDuration: "1h",
			},
			AutoBlockIP: true,
			AutoAlert:   true,
		},
	}
}

// DefaultHSTS returns the `web.hsts` defaults: 2 years, subdomains and
// preload on, per AI.md PART 11 "Security Header Config".
func DefaultHSTS() HSTS {
	return HSTS{
		Enabled:           true,
		MaxAgeSeconds:     63072000,
		IncludeSubdomains: true,
		Preload:           true,
	}
}

// DefaultHeaders returns the `web.headers` defaults. Cross-origin
// isolation stays loose ("unsafe-none"/"cross-origin") so ordinary
// embedding keeps working; only a project that declares it needs isolation
// tightens these.
func DefaultHeaders() Headers {
	return Headers{
		COOP:                    "unsafe-none",
		COEP:                    "unsafe-none",
		CORP:                    "cross-origin",
		OriginAgentCluster:      true,
		CrossDomainPolicies:     "none",
		DNSPrefetchControl:      "",
		HonorSecGPC:             true,
		HonorDNT:                false,
		SecFetchValidation:      true,
		ServerTimingInDebugOnly: true,
		ClearSiteData: ClearSiteData{
			OnTokenRevocation:   true,
			OnConsentWithdrawal: true,
			ExecutionContexts:   false,
		},
		NEL: NEL{
			Enabled:           true,
			MaxAgeSeconds:     2592000,
			IncludeSubdomains: true,
			SampleRate:        1.0,
		},
	}
}

// DefaultCSP returns the `web.csp` defaults: enforcing mode with reports
// enabled at full sample rate. Development mode downgrades to report-only
// at render time unless the operator set `mode: enforce` explicitly.
func DefaultCSP() CSP {
	return CSP{
		Enabled:           true,
		Mode:              "enforce",
		ReportsEnabled:    true,
		ReportsSampleRate: 1.0,
	}
}

// DefaultReports returns the `web.reports` rate-limit defaults.
func DefaultReports() Reports {
	return Reports{
		RateLimitPerMinute:  60,
		RateLimitPerIPBurst: 10,
	}
}

// DefaultRobots returns the `web.robots` defaults from AI.md PART 11
// "robots.txt".
func DefaultRobots() Robots {
	return Robots{
		Allow: []string{"/", "/api"},
		Deny:  []string{},
	}
}

// DefaultWebSecurity returns the `web.security` defaults. ReportURL points
// at this repository's private vulnerability reporting form (the primary
// channel per AI.md PART 11 "Security Reports"); Contact is left empty so
// it resolves per-request to "security@{fqdn}" from the client's own Host,
// which is what RFC 2142 and the URL-resolution rule both require.
func DefaultWebSecurity() WebSecurity {
	return WebSecurity{
		ReportURL:          "https://github.com/apimgr/shortner/security/advisories/new",
		Contact:            "",
		Expires:            "",
		Keyservers:         []string{"https://keys.openpgp.org"},
		PublishPGPKey:      true,
		PreferredLanguages: "en",
		Policy:             "",
		Thanks:             []SecurityThanks{},
	}
}

// DefaultLLMs returns the `web.llms` defaults.
func DefaultLLMs() LLMs {
	return LLMs{
		Enabled:          true,
		IncludeEndpoints: true,
		IncludeSchemas:   false,
		CustomSections:   []string{},
	}
}

// DefaultWellKnown returns the `web.well_known` defaults: every optional
// entry disabled, unknown entries 404.
func DefaultWellKnown() WellKnown {
	return WellKnown{UnsupportedBehavior: 404}
}

// EnsureEncryptionKey generates the 32-byte AES-256-GCM at-rest key when
// cfg.Server.Security.EncryptionKey is empty, per AI.md PART 11
// "Cryptographic Keys" -> "Pre-existing key (auto-generated in server.yml
// on first run)". Returns true when a new key was generated so the caller
// knows the config needs saving.
func EnsureEncryptionKey(cfg *Config) (bool, error) {
	if cfg.Server.Security.EncryptionKey != "" {
		return false, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return false, fmt.Errorf("config: generate encryption key: %w", err)
	}
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(buf)
	return true, nil
}

// DecodeEncryptionKey returns the raw 32-byte at-rest key. It is the only
// accessor callers should use — it enforces the length invariant so a
// truncated or hand-edited key fails loudly instead of silently weakening
// every AES-256-GCM seal.
func DecodeEncryptionKey(cfg *Config) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("config: decode server.security.encryption_key: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("config: server.security.encryption_key must decode to 32 bytes, got %d", len(raw))
	}
	return raw, nil
}
