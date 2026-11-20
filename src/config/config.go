// Package config loads and saves the merged server.yml, yaml.d, and secrets.yml
// operator configuration. See AI.md PART 5 for the authoritative schema.
package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/apimgr/shortner/src/security"
)

// Config is the root of the merged configuration.
type Config struct {
	Server                Server `yaml:"server"`
	Web                   Web    `yaml:"web"`
	databaseURLEnv        bool
	cacheURLEnv           bool
	databaseURLConfigured bool
	cacheURLConfigured    bool
	databaseURLSeeded     bool
	cacheURLSeeded        bool
}

// Server holds the top-level `server:` block of server.yml.
type Server struct {
	// Mode is the persisted operational mode, overridden by MODE and --mode.
	Mode string `yaml:"mode"`
	// Token is the global operator token (adm_ prefix). Auto-generated on
	// first run if empty. See AI.md PART 11 "API Token Model".
	Token   string `yaml:"token"`
	Listen  string `yaml:"address"`
	Port    string `yaml:"port"`
	BaseURL string `yaml:"baseurl"`
	// APIVersion is the version segment of /api/{api_version}/ routes. The
	// router itself accepts any segment (AI.md PART 14 route naming); this
	// value is what the server writes into generated URLs — the Reporting
	// API headers, the CSP report-uri, security.txt, and llms.txt.
	APIVersion     string         `yaml:"api_version"`
	Database       Database       `yaml:"database"`
	Limits         Limits         `yaml:"limits"`
	Compression    Compression    `yaml:"compression"`
	TrustedProxies TrustedProxies `yaml:"trusted_proxies"`
	RateLimit      RateLimit      `yaml:"rate_limit"`
	Logs           Logs           `yaml:"logs"`
	Cache          CacheConfig    `yaml:"cache"`
	Healthz        Healthz        `yaml:"healthz"`
	TLS            TLS            `yaml:"tls"`
	CORS           CORS           `yaml:"cors"`
	CSRF           CSRF           `yaml:"csrf"`
	Branding       Branding       `yaml:"branding"`
	SEO            SEO            `yaml:"seo"`
	Contact        Contact        `yaml:"contact"`
	Privacy        Privacy        `yaml:"privacy"`
	Scheduler      Scheduler      `yaml:"scheduler"`
	GeoIP          GeoIP          `yaml:"geoip"`
	Metrics        Metrics        `yaml:"metrics"`
	Backup         Backup         `yaml:"backup"`
	Compliance     Compliance     `yaml:"compliance"`
	Update         Update         `yaml:"update"`
	Security       Security       `yaml:"security"`
	Notifications  Notifications  `yaml:"notifications"`
	I18N           I18N           `yaml:"i18n"`
	Tor            Tor            `yaml:"tor"`
	I2P            I2P            `yaml:"i2p"`
	Tokens         Tokens         `yaml:"tokens"`
	Pages          Pages          `yaml:"pages"`
}

type Tokens struct {
	MaxLinked     int    `yaml:"max_linked"`
	CookieMaxAge  string `yaml:"cookie_max_age"`
	IdlePurgeDays int    `yaml:"idle_purge_days"`
}

func (c Tokens) CookieMaxAgeSeconds() int {
	v := strings.TrimSpace(c.CookieMaxAge)
	if v == "" {
		return 365 * 24 * 60 * 60
	}
	if strings.HasSuffix(v, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err == nil && days > 0 {
			return days * 24 * 60 * 60
		}
	}
	if strings.HasSuffix(v, "y") {
		years, err := strconv.Atoi(strings.TrimSuffix(v, "y"))
		if err == nil && years > 0 {
			return years * 365 * 24 * 60 * 60
		}
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return int(d.Seconds())
	}
	return 365 * 24 * 60 * 60
}

func (c Tokens) cookieMaxAgeValid() bool {
	v := strings.TrimSpace(c.CookieMaxAge)
	if strings.HasSuffix(v, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		return err == nil && days > 0
	}
	if strings.HasSuffix(v, "y") {
		years, err := strconv.Atoi(strings.TrimSuffix(v, "y"))
		return err == nil && years > 0
	}
	d, err := time.ParseDuration(v)
	return err == nil && d > 0
}

// Update holds `server.update`, per AI.md PART 22 "Update Configuration".
//
// Branch is also what `--update branch {name}` writes: the config file is
// the single source of truth for the channel, and there is no separate
// CLI-side state.
type Update struct {
	// CLIMinVersion is the minimum accepted CLI version returned by autodiscover.
	CLIMinVersion string `yaml:"cli_min_version"`
	// Branch is the release channel: stable | beta | daily.
	Branch string `yaml:"branch"`
	// AutoInstall lets the update_check task run the full `--update yes`
	// flow. Default off — installing is always an explicit operator
	// decision, so the task otherwise only notifies.
	AutoInstall bool `yaml:"auto_install"`
	// DeferDays (0-365) is the age a release must reach before the
	// update_check task considers it. It gates the scheduled task only;
	// a manual --update check/yes always sees the true latest release.
	DeferDays int `yaml:"defer_days"`
}

// Backup holds `server.backup`, per AI.md PART 21 "Backup Encryption" and
// "Backup Retention".
//
// EncryptionPassword is the one spec-sanctioned home for the backup
// password ("Set during initial config: `backup.encryption_password` in
// `server.yml`"). PART 21 also says the password is "NEVER stored" — the
// two statements are reconciled the way the spec's own CLI section
// describes: the app never generates, derives, or persists this value on
// its own (the CLI always prompts interactively instead, and never accepts
// a password flag), and it is never written to a backup, the database, a
// log line, or any API response. It exists only so unattended scheduled
// backups can be encrypted at all; leaving it empty is the more secure
// choice for interactive-only operators.
type Backup struct {
	Encryption         BackupEncryption `yaml:"encryption"`
	EncryptionPassword string           `yaml:"encryption_password"`
	Retention          BackupRetention  `yaml:"retention"`
	// DiskThreshold is the disk-usage percentage above which the
	// backup_daily task refuses to create a backup, per AI.md PART 21
	// "Backup Creation Flow" step 2 ("disk usage > disk_threshold (default
	// 90%)").
	DiskThreshold int `yaml:"disk_threshold"`
}

// BackupEncryption holds `server.backup.encryption`, per AI.md PART 21
// "Encryption Configuration". Enabled is "true if password was set"; Hint
// is the optional, non-secret reminder PART 21 permits ("Password hint can
// be stored (optional)").
type BackupEncryption struct {
	Enabled bool   `yaml:"enabled"`
	Hint    string `yaml:"hint"`
}

// BackupRetention holds `server.backup.retention`, per AI.md PART 21
// "Backup Retention" -> "Configuration".
type BackupRetention struct {
	MaxBackups  int `yaml:"max_backups"`
	KeepWeekly  int `yaml:"keep_weekly"`
	KeepMonthly int `yaml:"keep_monthly"`
	KeepYearly  int `yaml:"keep_yearly"`
	// MaxTotalSize is a percent of the backup volume ("10%") or an absolute
	// size ("50G"); any falsey value disables the cap.
	MaxTotalSize string `yaml:"max_total_size"`
}

// Compliance holds `server.compliance`, per AI.md PART 21 "Compliance Mode
// Enforcement" and PART 11 "Compliance Standards". When Enabled, backups
// refuse to run unencrypted. Every individual standard is disabled by
// default and enabled one key at a time.
type Compliance struct {
	Enabled  bool `yaml:"enabled"`
	GDPR     bool `yaml:"gdpr"`
	CCPA     bool `yaml:"ccpa"`
	HIPAA    bool `yaml:"hipaa"`
	SOC2     bool `yaml:"soc2"`
	PCIDSS   bool `yaml:"pci_dss"`
	ISO27001 bool `yaml:"iso27001"`
	FedRAMP  bool `yaml:"fedramp"`
	LGPD     bool `yaml:"lgpd"`
	PIPEDA   bool `yaml:"pipeda"`
	APPI     bool `yaml:"appi"`
	PDPA     bool `yaml:"pdpa"`
}

// EnabledStandards returns the enabled standard names in the AI.md PART 11
// "Available Standards" table order, for the compliance report and the
// compliance audit events.
func (c Compliance) EnabledStandards() []string {
	pairs := []struct {
		name string
		on   bool
	}{
		{"gdpr", c.GDPR}, {"ccpa", c.CCPA}, {"hipaa", c.HIPAA},
		{"soc2", c.SOC2}, {"pci_dss", c.PCIDSS}, {"iso27001", c.ISO27001},
		{"fedramp", c.FedRAMP}, {"lgpd", c.LGPD}, {"pipeda", c.PIPEDA},
		{"appi", c.APPI}, {"pdpa", c.PDPA},
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p.on {
			out = append(out, p.name)
		}
	}
	return out
}

// AuditRetentionYears returns the longest audit-log retention any enabled
// standard requires, per AI.md PART 11 "Compliance Requirements Matrix"
// and "Overlap Resolution" ("Retention period: use longest"). 0 means no
// standard imposes one.
func (c Compliance) AuditRetentionYears() int {
	years := 0
	bump := func(on bool, n int) {
		if on && n > years {
			years = n
		}
	}
	bump(c.GDPR, 1)
	bump(c.CCPA, 1)
	bump(c.HIPAA, 6)
	bump(c.SOC2, 1)
	bump(c.PCIDSS, 1)
	bump(c.ISO27001, 3)
	return years
}

// RequiresConsentBanner reports whether any enabled standard requires the
// cookie-consent banner before tracking, per AI.md PART 11
// "Compliance-Specific Behaviors" (GDPR/CCPA/LGPD/PIPEDA/APPI/PDPA).
func (c Compliance) RequiresConsentBanner() bool {
	return c.GDPR || c.CCPA || c.LGPD || c.PIPEDA || c.APPI || c.PDPA
}

// RequiresDPOContact reports whether the GDPR Data Protection Officer
// contact page (/server/dpo) must be served, per AI.md PART 11
// "Compliance Routes" and "GDPR (gdpr: true)" -> "DPO contact".
func (c Compliance) RequiresDPOContact() bool {
	return c.GDPR || c.LGPD
}

// Metrics holds `server.metrics`, per AI.md PART 20 "Metrics". Metrics are
// internal-only: never advertised on healthz, gated behind mandatory
// per-service bearer tokens unless Auth.AllowUnauthenticated is set for a
// firewalled deployment.
type Metrics struct {
	Enabled bool        `yaml:"enabled"`
	Root    MetricsRoot `yaml:"root"`
	Auth    MetricsAuth `yaml:"auth"`
	// IncludeSystem toggles the gopsutil-backed system_* metrics (CPU,
	// memory, disk).
	IncludeSystem bool `yaml:"include_system"`
	// IncludeRuntime toggles the go_* Go-runtime metrics.
	IncludeRuntime  bool        `yaml:"include_runtime"`
	Loki            MetricsLoki `yaml:"loki"`
	DurationBuckets []float64   `yaml:"duration_buckets"`
	SizeBuckets     []float64   `yaml:"size_buckets"`
}

// MetricsRoot holds `server.metrics.root`, the `/metrics[/{service}]`
// unversioned root-alias gate (default true — Prometheus scrapers expect
// `/metrics`).
type MetricsRoot struct {
	Enabled bool `yaml:"enabled"`
}

// MetricsAuth holds `server.metrics.auth`, per AI.md PART 20
// "Authentication". Each service (prometheus/grafana/loki) requires its own
// bearer token; an empty token disables that service (403).
// AllowUnauthenticated is a firewalled-only escape hatch that skips ALL
// service token checks.
type MetricsAuth struct {
	AllowUnauthenticated bool              `yaml:"allow_unauthenticated"`
	Tokens               MetricsAuthTokens `yaml:"tokens"`
}

// MetricsAuthTokens holds `server.metrics.auth.tokens`.
type MetricsAuthTokens struct {
	Prometheus string `yaml:"prometheus"`
	Grafana    string `yaml:"grafana"`
	Loki       string `yaml:"loki"`
}

// MetricsLoki holds `server.metrics.loki`, bounding the recent-log-entries
// window served by the `loki` metrics service.
type MetricsLoki struct {
	MaxEntries int    `yaml:"max_entries"`
	MaxAge     string `yaml:"max_age"`
}

// GeoIP holds `server.geoip`, per AI.md PART 19 "GeoIP". Zero-config on
// first run: enabled with no country restrictions, all three databases on.
// Dir defaults to "" here (config.Default only receives a DB file path, not
// a data directory) and is resolved by main.go to
// "{data_dir}/security/geoip", mirroring how DataDir is resolved elsewhere.
type GeoIP struct {
	Enabled        bool           `yaml:"enabled"`
	Dir            string         `yaml:"dir"`
	DenyCountries  []string       `yaml:"deny_countries"`
	AllowCountries []string       `yaml:"allow_countries"`
	Databases      GeoIPDatabases `yaml:"databases"`
}

// GeoIPDatabases toggles which of the three MMDB categories are
// downloaded/loaded, per AI.md PART 19's `server.geoip.databases` block.
type GeoIPDatabases struct {
	ASN     bool `yaml:"asn"`
	Country bool `yaml:"country"`
	City    bool `yaml:"city"`
}

// Scheduler holds `server.scheduler`, per AI.md PART 18 "Task
// Configuration". The scheduler itself is always running — there is no
// top-level enable/disable, only per-task overrides.
type Scheduler struct {
	Timezone      string                       `yaml:"timezone"`
	CatchUpWindow string                       `yaml:"catch_up_window"`
	Tasks         map[string]SchedulerTaskYAML `yaml:"tasks"`
}

// SchedulerTaskYAML is one entry under `server.scheduler.tasks`.
type SchedulerTaskYAML struct {
	Schedule    string `yaml:"schedule"`
	Enabled     bool   `yaml:"enabled"`
	RetryOnFail bool   `yaml:"retry_on_fail"`
	RetryDelay  string `yaml:"retry_delay"`
	Retention   int    `yaml:"retention,omitempty"`
}

// CORS holds `server.cors`, per AI.md PART 16 "CORS" -> "Configuration".
type CORS struct {
	AllowedOrigins   []string `yaml:"allowed_origins"`
	AllowCredentials bool     `yaml:"allow_credentials"`
	MaxAge           int      `yaml:"max_age"`
}

// CSRF holds `server.csrf`, per AI.md PART 16 "CSRF Protection" ->
// "Configuration".
type CSRF struct {
	Enabled     bool   `yaml:"enabled"`
	TokenLength int    `yaml:"token_length"`
	CookieName  string `yaml:"cookie_name"`
	HeaderName  string `yaml:"header_name"`
	// Secure is "auto" (Secure when the request is HTTPS), "true", or
	// "false".
	Secure      string   `yaml:"secure"`
	ExemptPaths []string `yaml:"exempt_paths"`
}

// Branding holds `server.branding`, per AI.md PART 16 "Branding File".
type Branding struct {
	Title       string          `yaml:"title"`
	Tagline     string          `yaml:"tagline"`
	Description string          `yaml:"description"`
	Favicon     string          `yaml:"favicon"`
	Logo        string          `yaml:"logo"`
	Content     BrandingContent `yaml:"content"`
}

type BrandingContent struct {
	Header  OperatorPageContent `yaml:"header"`
	About   OperatorPageContent `yaml:"about"`
	Privacy OperatorPageContent `yaml:"privacy"`
	Footer  OperatorPageContent `yaml:"footer"`
	Terms   OperatorPageContent `yaml:"terms"`
}

type OperatorPageContent struct {
	Format string            `yaml:"format"`
	Body   LocalizedPageBody `yaml:"body"`
}

type LocalizedPageBody struct {
	Plain  string
	Values map[string]string
	Has    bool
}

func (body *LocalizedPageBody) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		body.Plain = node.Value
		body.Values = nil
		body.Has = true
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("page body must be a string or language map")
	}
	values := make(map[string]string, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		key, value := node.Content[index], node.Content[index+1]
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return fmt.Errorf("page body language %q must be a string", key.Value)
		}
		values[key.Value] = value.Value
	}
	body.Values = values
	body.Has = true
	return nil
}

func (body LocalizedPageBody) Resolve(language string) (string, bool) {
	if !body.Has {
		return "", false
	}
	if body.Values == nil {
		return body.Plain, true
	}
	if value, ok := body.Values[language]; ok {
		return value, true
	}
	value, ok := body.Values["default"]
	return value, ok
}

// SEO holds `server.seo`, per AI.md PART 16.
type SEO struct {
	Keywords       []string          `yaml:"keywords"`
	Author         string            `yaml:"author"`
	OGImage        string            `yaml:"og_image"`
	TwitterHandle  string            `yaml:"twitter_handle"`
	TitleSeparator string            `yaml:"title_separator"`
	MetaTags       map[string]string `yaml:"meta_tags"`
	Canonical      string            `yaml:"canonical"`
	NoIndex        bool              `yaml:"noindex"`
	OpenGraph      SEOOpenGraph      `yaml:"opengraph"`
	Twitter        SEOTwitter        `yaml:"twitter"`
	Verification   SEOVerification   `yaml:"verification"`
	Sitemap        Sitemap           `yaml:"sitemap"`
	Feed           Feed              `yaml:"feed"`
}

type SEOOpenGraph struct {
	Enabled  bool   `yaml:"enabled"`
	Type     string `yaml:"type"`
	SiteName string `yaml:"site_name"`
}

type SEOTwitter struct {
	Enabled bool   `yaml:"enabled"`
	Card    string `yaml:"card"`
	Creator string `yaml:"creator"`
}

type SEOVerification struct {
	Google    string             `yaml:"google"`
	Bing      string             `yaml:"bing"`
	Yandex    string             `yaml:"yandex"`
	Baidu     string             `yaml:"baidu"`
	Pinterest string             `yaml:"pinterest"`
	Facebook  string             `yaml:"facebook"`
	Custom    []SEOCustomMetaTag `yaml:"custom"`
}

type SEOCustomMetaTag struct {
	Name     string `yaml:"name"`
	Property string `yaml:"property"`
	Content  string `yaml:"content"`
}

// Sitemap controls the public sitemap endpoint, per AI.md PART 16.
type Sitemap struct {
	Enabled       bool `yaml:"enabled"`
	MaxURLs       int  `yaml:"max_urls"`
	IncludeImages bool `yaml:"include_images"`
}

type Feed struct {
	MaxItems int    `yaml:"max_items"`
	Title    string `yaml:"title"`
}

// Contact holds `server.contact`, per AI.md PART 16 "/server/contact" ->
// "Configuration". Admin.Email is never rendered publicly. General.Email
// is a private-recipient fallback and appears only on approved report pages.
type Contact struct {
	General  ContactRecipient `yaml:"general"`
	Admin    ContactRecipient `yaml:"admin"`
	Abuse    ContactRecipient `yaml:"abuse"`
	Antispam ContactAntispam  `yaml:"antispam"`
	// Security is the RFC 2142 security@ role mailbox — the CC channel for
	// vulnerability reports, surfaced in security.txt's mailto Contact
	// line and in the PGP keypair's UID.
	Security ContactRecipient `yaml:"security"`
	// DPO backs the GDPR Data Protection Officer page (AI.md PART 11
	// "Compliance Routes"). The page 404s unless a standard requiring a
	// DPO is enabled, so these stay empty for most deployments.
	DPO ContactDPO `yaml:"dpo"`
}

// ContactAntispam controls the server-side layers protecting anonymous
// report and security-report submissions, per AI.md PART 16.
type ContactAntispam struct {
	Honeypot             bool     `yaml:"honeypot"`
	MinFillSeconds       int      `yaml:"min_fill_seconds"`
	MaxAgeSeconds        int      `yaml:"max_age_seconds"`
	ReviewMinSeconds     int      `yaml:"review_min_seconds"`
	ReviewMaxSeconds     int      `yaml:"review_max_seconds"`
	PerIP                int      `yaml:"per_ip"`
	PerEmail             int      `yaml:"per_email"`
	Global               int      `yaml:"global"`
	Window               string   `yaml:"window"`
	MaxLinks             int      `yaml:"max_links"`
	RejectMarkup         bool     `yaml:"reject_markup"`
	BlockedWords         []string `yaml:"blocked_words"`
	BlockedEmailDomains  []string `yaml:"blocked_email_domains"`
	Strikes              int      `yaml:"strikes"`
	BanHours             int      `yaml:"ban_hours"`
	DuplicateWindowHours int      `yaml:"duplicate_window_hours"`
}

// ContactRecipient is one named contact-form recipient.
type ContactRecipient struct {
	Email string `yaml:"email"`
}

// ContactDPO is the Data Protection Officer's published contact details.
type ContactDPO struct {
	Name    string `yaml:"name"`
	Email   string `yaml:"email"`
	Address string `yaml:"address"`
}

// Privacy holds `server.privacy`, per AI.md PART 16 "/server/privacy" ->
// "Privacy Configuration (config file)".
type Privacy struct {
	Data       PrivacyData       `yaml:"data"`
	Consent    PrivacyConsent    `yaml:"consent"`
	Cookies    PrivacyCookies    `yaml:"cookies"`
	Retention  PrivacyRetention  `yaml:"retention"`
	ThirdParty PrivacyThirdParty `yaml:"third_party"`
}

// PrivacyData holds `server.privacy.data`.
type PrivacyData struct {
	Sold           bool               `yaml:"sold"`
	StoredOnServer bool               `yaml:"stored_on_server"`
	Sharing        []PrivacyDataShare `yaml:"sharing"`
}

// PrivacyDataShare is one `server.privacy.data.sharing[]` entry.
type PrivacyDataShare struct {
	Condition string `yaml:"condition"`
	When      string `yaml:"when"`
	Data      string `yaml:"data"`
}

// PrivacyConsent holds `server.privacy.consent`, the cookie-consent banner
// text, per AI.md PART 16 "Cookie Consent Banner" -> "Implementation".
type PrivacyConsent struct {
	Message         string                `yaml:"message"`
	MessageIfSold   string                `yaml:"message_if_sold"`
	Policy          PrivacyConsentPolicy  `yaml:"policy"`
	Buttons         PrivacyConsentButtons `yaml:"buttons"`
	PreferencesText string                `yaml:"preferences_text"`
}

// PrivacyConsentPolicy holds the consent banner's policy link.
type PrivacyConsentPolicy struct {
	URL  string `yaml:"url"`
	Text string `yaml:"text"`
}

// PrivacyConsentButtons holds the consent banner's button labels.
type PrivacyConsentButtons struct {
	Decline string `yaml:"decline"`
	Accept  string `yaml:"accept"`
}

// GetConsentMessage returns MessageIfSold when Data.Sold is true, else
// Message, per AI.md PART 16 "Dynamic Message Selection".
func (p Privacy) GetConsentMessage() string {
	if p.Data.Sold && p.Consent.MessageIfSold != "" {
		return p.Consent.MessageIfSold
	}
	return p.Consent.Message
}

// PrivacyCookies holds `server.privacy.cookies`.
type PrivacyCookies struct {
	Essential   PrivacyCookieCategory `yaml:"essential"`
	Preferences PrivacyCookieCategory `yaml:"preferences"`
	Analytics   PrivacyCookieCategory `yaml:"analytics"`
}

// PrivacyCookieCategory describes one cookie category shown in the
// consent-preferences dialog and the privacy policy's Cookie Policy
// section.
type PrivacyCookieCategory struct {
	Enabled     bool   `yaml:"enabled"`
	Description string `yaml:"description"`
}

// PrivacyRetention holds `server.privacy.retention`.
type PrivacyRetention struct {
	Period            string `yaml:"period"`
	ExportAvailable   bool   `yaml:"export_available"`
	DeletionAvailable bool   `yaml:"deletion_available"`
}

// PrivacyThirdParty holds `server.privacy.third_party`.
type PrivacyThirdParty struct {
	Services []PrivacyThirdPartyService `yaml:"services"`
}

// PrivacyThirdPartyService is one third-party service disclosure entry.
type PrivacyThirdPartyService struct {
	Name      string `yaml:"name"`
	Purpose   string `yaml:"purpose"`
	DataSent  string `yaml:"data_sent"`
	PolicyURL string `yaml:"policy_url"`
}

// Web holds the top-level `web:` block of server.yml, per AI.md PART 16
// "Footer Customization" and "Announcements".
type Web struct {
	Announcements WebAnnouncements `yaml:"announcements"`
	UI            WebUI            `yaml:"ui"`

	// The PART 11 header/well-known blocks. See src/config/security.go for
	// each type's spec reference.
	HSTS      HSTS        `yaml:"hsts"`
	Headers   Headers     `yaml:"headers"`
	CSP       CSP         `yaml:"csp"`
	Reports   Reports     `yaml:"reports"`
	Robots    Robots      `yaml:"robots"`
	Security  WebSecurity `yaml:"security"`
	LLMs      LLMs        `yaml:"llms"`
	WellKnown WellKnown   `yaml:"well_known"`
}

type WebUI struct {
	Theme  string `yaml:"theme"`
	Accent string `yaml:"accent"`
}

// WebAnnouncements holds `web.announcements`, per AI.md PART 16
// "Announcements".
type WebAnnouncements struct {
	Enabled  bool           `yaml:"enabled"`
	Messages []Announcement `yaml:"messages"`
}

// Announcement is one operator-configured announcement banner entry.
type Announcement struct {
	ID          string `yaml:"id"`
	Type        string `yaml:"type"`
	Title       string `yaml:"title"`
	Message     string `yaml:"message"`
	Start       string `yaml:"start"`
	End         string `yaml:"end"`
	Dismissible bool   `yaml:"dismissible"`
}

// Pages holds `server.pages`, per AI.md
// PART 16 "Pages Configuration (config file)".
type Pages struct {
	Contact ContactPage `yaml:"contact"`
	Help    PageContent `yaml:"help"`
}

// PageContent is the Markdown source for server-rendered help content.
type PageContent struct {
	Content string `yaml:"content"`
}

// ContactPage holds `server.pages.contact`.
type ContactPage struct {
	Enabled          bool   `yaml:"enabled"`
	Captcha          string `yaml:"captcha"`
	CaptchaSiteKey   string `yaml:"captcha_site_key"`
	CaptchaSecretKey string `yaml:"captcha_secret_key"`
	SuccessMessage   string `yaml:"success_message"`
}

// TLS holds `server.tls`, per AI.md PART 15 "Built-in Let's Encrypt
// Support". DNSCredentials stores the flat lego environment-name map;
// SealDNSCredentials encrypts every value before it is written to YAML.
type TLS struct {
	// Enabled turns on HTTPS/ACME certificate handling for the resolved
	// FQDN. When false (default), the server is HTTP-only.
	Enabled bool `yaml:"enabled"`
	// DNSProvider selects a DNS-01 provider (e.g. "cloudflare", "route53")
	// for certificate issuance when configured.
	DNSProvider string `yaml:"dns_provider"`
	// DNSCredentials is the flat map of lego environment variable names to
	// encrypted values, per AI.md PART 15 "DNS-01 credential contract".
	DNSCredentials map[string]string `yaml:"dns_credentials"`
}

// Database holds the `server.database` block.
type Database struct {
	// Driver is "sqlite" (default, pure Go modernc.org/sqlite) or "libsql".
	Driver string `yaml:"driver"`
	URL    string `yaml:"url"`
	Token  string `yaml:"token"`
}

// Limits holds `server.limits`, per AI.md PART 12 "Request Limits".
// Durations and sizes are stored as their raw YAML strings ("30s",
// "10MB") and parsed on demand via ParseDuration/ParseSize so an invalid
// value can be replaced with a default (Validate) instead of failing
// startup.
type Limits struct {
	MaxBodySize  string `yaml:"max_body_size"`
	ReadTimeout  string `yaml:"read_timeout"`
	WriteTimeout string `yaml:"write_timeout"`
	IdleTimeout  string `yaml:"idle_timeout"`
}

// Compression holds `server.compression`, per AI.md PART 12 "Response
// Compression".
type Compression struct {
	Enabled bool     `yaml:"enabled"`
	Level   int      `yaml:"level"`
	Types   []string `yaml:"types"`
}

// TrustedProxies holds `server.trusted_proxies`, per AI.md PART 12
// "Trusted Proxies". Private ranges are always trusted regardless of this
// list; Additional extends the trust gate with public IPs/CIDRs/hostnames
// of upstream proxies.
type TrustedProxies struct {
	Additional []string `yaml:"additional"`
}

// RateLimit holds `server.rate_limit`, per AI.md PART 12 "Rate Limiting".
type RateLimit struct {
	Read        RateLimitClass `yaml:"read"`
	Write       RateLimitClass `yaml:"write"`
	Health      RateLimitClass `yaml:"health"`
	GlobalBurst int            `yaml:"global_burst"`
}

// RateLimitClass is one per-minute-per-IP limit tier (read/write/health).
type RateLimitClass struct {
	Requests int `yaml:"requests"`
	Window   int `yaml:"window"`
}

// Logs describes the named log streams and their shared defaults.
type Logs struct {
	Level    string      `yaml:"level"`
	Defaults LogDefaults `yaml:"defaults"`
	Access   LogKind     `yaml:"access"`
	Server   LogKind     `yaml:"server"`
	Error    LogKind     `yaml:"error"`
	Audit    LogKind     `yaml:"audit"`
	Security LogKind     `yaml:"security"`
	Debug    LogKind     `yaml:"debug"`
}

type LogDefaults struct {
	Custom string `yaml:"custom"`
	Rotate string `yaml:"rotate"`
	Keep   string `yaml:"keep"`
}

type LogKind struct {
	Enabled  *bool  `yaml:"enabled,omitempty"`
	Format   string `yaml:"format,omitempty"`
	Custom   string `yaml:"custom,omitempty"`
	Rotate   string `yaml:"rotate,omitempty"`
	Keep     string `yaml:"keep,omitempty"`
	Compress bool   `yaml:"compress,omitempty"`
}

// EffectiveLogKind overlays per-kind values on the shared defaults.
func (l Logs) EffectiveLogKind(kind string) (LogKind, bool) {
	var value LogKind
	switch kind {
	case "access":
		value = l.Access
	case "server":
		value = l.Server
	case "error":
		value = l.Error
	case "audit":
		value = l.Audit
	case "security":
		value = l.Security
	case "debug":
		value = l.Debug
	default:
		return LogKind{}, false
	}
	if value.Custom == "" {
		value.Custom = l.Defaults.Custom
	}
	if value.Rotate == "" {
		value.Rotate = l.Defaults.Rotate
	}
	if value.Keep == "" {
		value.Keep = l.Defaults.Keep
	}
	return value, true
}

func boolPtr(value bool) *bool {
	return &value
}

// CacheConfig holds `server.cache`, per AI.md PART 12 "Cache
// Configuration". Only the "memory" (in-process, default) driver is
// implemented — "valkey"/"redis" depend on a client dependency not yet
// added (tracked in TODO.AI.md).
type CacheConfig struct {
	Type             string `yaml:"type"`
	URL              string `yaml:"url"`
	Host             string `yaml:"host"`
	Port             int    `yaml:"port"`
	Username         string `yaml:"username"`
	Password         string `yaml:"password"`
	DB               int    `yaml:"db"`
	TLS              bool   `yaml:"tls"`
	TLSSkipVerify    bool   `yaml:"tls_skip_verify"`
	TLSCA            string `yaml:"tls_ca"`
	TLSCert          string `yaml:"tls_cert"`
	TLSKey           string `yaml:"tls_key"`
	tlsSkipVerifySet bool
	PoolSize         int    `yaml:"pool_size"`
	MinIdle          int    `yaml:"min_idle"`
	Timeout          string `yaml:"timeout"`
	Prefix           string `yaml:"prefix"`
	TTL              string `yaml:"ttl"`
}

// Healthz holds `server.healthz`, per AI.md PART 13 "Health Checks".
type Healthz struct {
	Root HealthzRoot `yaml:"root"`
}

// HealthzRoot controls the optional `/healthz` root alias.
type HealthzRoot struct {
	Enabled bool `yaml:"enabled"`
}

// Default returns a Config populated with the framework defaults, using
// dbPath as the SQLite database location for the current OS/privilege
// level (see src/paths).
func Default(dbPath string) *Config {
	return &Config{
		Server: Server{
			Mode:       "production",
			Listen:     "[::]",
			Port:       "8090",
			BaseURL:    "/",
			APIVersion: "v1",
			Database: Database{
				Driver: "sqlite",
				URL:    dbPath,
			},
			Limits: Limits{
				MaxBodySize:  "10MB",
				ReadTimeout:  "30s",
				WriteTimeout: "30s",
				IdleTimeout:  "120s",
			},
			Compression: Compression{
				Enabled: true,
				Level:   5,
				Types: []string{
					"text/html",
					"text/css",
					"text/javascript",
					"application/json",
					"application/xml",
				},
			},
			RateLimit: RateLimit{
				Read:        RateLimitClass{Requests: 120, Window: 60},
				Write:       RateLimitClass{Requests: 10, Window: 60},
				Health:      RateLimitClass{Requests: 120, Window: 60},
				GlobalBurst: 240,
			},
			Logs: Logs{
				Level:    "warn",
				Defaults: LogDefaults{Rotate: "weekly,50MB", Keep: "none"},
				Access:   LogKind{Format: "apache", Rotate: "monthly"},
				Server:   LogKind{Format: "text"},
				Error:    LogKind{Format: "text"},
				Audit:    LogKind{Enabled: boolPtr(true), Format: "json", Rotate: "daily"},
				Security: LogKind{Format: "fail2ban"},
				Debug:    LogKind{Enabled: boolPtr(false), Format: "text"},
			},
			Cache: CacheConfig{
				Type:     "memory",
				Host:     "localhost",
				Port:     6379,
				PoolSize: 10,
				MinIdle:  2,
				Timeout:  "5s",
				Prefix:   "shortner:",
				TTL:      "1h",
			},
			TLS: TLS{
				Enabled: false,
			},
			CORS: CORS{
				AllowedOrigins:   []string{"*"},
				AllowCredentials: false,
				MaxAge:           600,
			},
			CSRF: CSRF{
				Enabled:     true,
				TokenLength: 32,
				CookieName:  "csrf_token",
				HeaderName:  "X-CSRF-Token",
				Secure:      "auto",
			},
			SEO: SEO{
				TitleSeparator: " - ",
				OpenGraph:      SEOOpenGraph{Enabled: true, Type: "website"},
				Twitter:        SEOTwitter{Enabled: true, Card: "summary_large_image"},
				Sitemap:        Sitemap{Enabled: true, MaxURLs: 50000},
				Feed:           Feed{MaxItems: 50},
			},
			Contact: Contact{Antispam: ContactAntispam{
				Honeypot: true, MinFillSeconds: 3, MaxAgeSeconds: 43200, ReviewMinSeconds: 2, ReviewMaxSeconds: 900,
				PerIP: 5, PerEmail: 3, Global: 60, Window: "1h",
				MaxLinks: 10, RejectMarkup: true, Strikes: 3, BanHours: 24, DuplicateWindowHours: 24,
			}},
			GeoIP: GeoIP{
				Enabled:        true,
				Dir:            "",
				DenyCountries:  []string{},
				AllowCountries: []string{},
				Databases: GeoIPDatabases{
					ASN:     true,
					Country: true,
					City:    true,
				},
			},
			Scheduler: Scheduler{
				Timezone:      "America/New_York",
				CatchUpWindow: "1h",
				Tasks: map[string]SchedulerTaskYAML{
					"ssl_renewal":      {Schedule: "0 3 * * *", Enabled: true},
					"geoip_update":     {Schedule: "0 3 * * 0", Enabled: true},
					"blocklist_update": {Schedule: "0 4 * * *", Enabled: true},
					"cve_update":       {Schedule: "0 5 * * *", Enabled: true},
					"update_check":     {Schedule: "0 6 * * *", Enabled: true},
					"token_cleanup":    {Schedule: "@every 15m", Enabled: true},
					"log_rotation":     {Schedule: "0 0 * * *", Enabled: true},
					"backup_daily":     {Schedule: "0 2 * * *", Enabled: true},
					"backup_hourly":    {Schedule: "@hourly", Enabled: false},
					"healthcheck_self": {Schedule: "@every 5m", Enabled: true},
					"tor_health":       {Schedule: "@every 10m", Enabled: true},
					"i2p_health":       {Schedule: "@every 10m", Enabled: true},
				},
			},
			Metrics: Metrics{
				Enabled: true,
				Root:    MetricsRoot{Enabled: true},
				Auth: MetricsAuth{
					AllowUnauthenticated: false,
					Tokens: MetricsAuthTokens{
						Prometheus: "",
						Grafana:    "",
						Loki:       "",
					},
				},
				IncludeSystem:  true,
				IncludeRuntime: true,
				Loki: MetricsLoki{
					MaxEntries: 1000,
					MaxAge:     "1h",
				},
				DurationBuckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
				SizeBuckets:     []float64{100, 1000, 10000, 100000, 1000000, 10000000},
			},
			Backup: Backup{
				Encryption: BackupEncryption{Enabled: false},
				Retention: BackupRetention{
					MaxBackups:   1,
					KeepWeekly:   0,
					KeepMonthly:  0,
					KeepYearly:   0,
					MaxTotalSize: "10%",
				},
				DiskThreshold: 90,
			},
			Compliance: Compliance{Enabled: false},
			// AI.md PART 22 "Update Configuration": stable channel,
			// notify-only, no defer window.
			Update: Update{
				Branch:      "stable",
				AutoInstall: false,
				DeferDays:   0,
			},
			Security:      DefaultSecurity(),
			Notifications: DefaultNotifications(),
			I18N:          DefaultI18N(),
			Tor:           DefaultTor(),
			I2P:           DefaultI2P(),
			Tokens:        Tokens{MaxLinked: 25, CookieMaxAge: "1y", IdlePurgeDays: 0},
			Privacy: Privacy{
				Consent: PrivacyConsent{
					Message: "We use essential cookies to make this site work. " +
						"With your consent, we may also use cookies to improve " +
						"your experience.",
					Buttons: PrivacyConsentButtons{
						Decline: "Decline",
						Accept:  "Accept",
					},
					PreferencesText: "Cookie preferences",
				},
				Cookies: PrivacyCookies{
					Essential: PrivacyCookieCategory{
						Enabled:     true,
						Description: "Required for the site to function (e.g. theme, CSRF protection).",
					},
					Preferences: PrivacyCookieCategory{
						Enabled:     true,
						Description: "Remembers your display preferences.",
					},
					Analytics: PrivacyCookieCategory{
						Enabled:     false,
						Description: "Not used by this server.",
					},
				},
				Retention: PrivacyRetention{
					Period:            "Link and click data is retained until deleted by the owner.",
					ExportAvailable:   false,
					DeletionAvailable: false,
				},
			},
			Pages: Pages{
				Contact: ContactPage{
					Enabled:          true,
					Captcha:          "simple",
					CaptchaSiteKey:   "",
					CaptchaSecretKey: "",
					SuccessMessage:   "Thank you for your message. We'll respond soon.",
				},
			},
		},
		Web: Web{
			// Dark is the required default per AI.md PART 16's "Three
			// Required Themes" table (Dark: Default = YES, Auto: No).
			UI:        WebUI{Theme: "dark"},
			HSTS:      DefaultHSTS(),
			Headers:   DefaultHeaders(),
			CSP:       DefaultCSP(),
			Reports:   DefaultReports(),
			Robots:    DefaultRobots(),
			Security:  DefaultWebSecurity(),
			LLMs:      DefaultLLMs(),
			WellKnown: DefaultWellKnown(),
		},
	}
}

// Load reads server.yml from path. If the file does not exist, it returns
// Default(dbPath) without writing anything — callers decide when to persist
// a first-run config via Save.
func Load(path, dbPath string) (*Config, error) {
	data, err := readMergedConfig(path)
	if err != nil {
		return nil, err
	}
	baseData, baseErr := os.ReadFile(path)
	if baseErr != nil && !os.IsNotExist(baseErr) {
		log.Printf("config: ignoring unreadable server.yml %s: %v", path, baseErr)
		baseData, baseErr = nil, os.ErrNotExist
	}
	cfg := Default(dbPath)
	var legacy Config
	if baseErr == nil {
		if err := yaml.Unmarshal(baseData, &legacy); err != nil {
			log.Printf("config: ignoring invalid server.yml %s: %v", path, err)
			legacy = Config{}
		}
	}
	legacyToken, legacyKey := legacy.Server.Token, legacy.Server.Security.EncryptionKey
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse merged configuration: %w", err)
	}
	var addressProbe struct {
		Server struct {
			Address *string `yaml:"address"`
			Listen  *string `yaml:"listen"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &addressProbe); err != nil {
		return nil, fmt.Errorf("config: parse merged configuration: %w", err)
	}
	if addressProbe.Server.Address == nil && addressProbe.Server.Listen != nil {
		cfg.Server.Listen = *addressProbe.Server.Listen
	}
	cfg.Web.CSP.ModeExplicit = cspModeIsExplicit(data)
	secretsPath := filepath.Join(filepath.Dir(path), "secrets.yml")
	secretData, secretErr := os.ReadFile(secretsPath)
	if secretErr != nil && !os.IsNotExist(secretErr) {
		log.Printf("config: ignoring unreadable secrets file %s: %v", secretsPath, secretErr)
	}
	secretToken, secretKey := "", ""
	if secretErr == nil {
		var secretValues Config
		if err := yaml.Unmarshal(secretData, &secretValues); err != nil {
			log.Printf("config: ignoring invalid secrets file %s: %v", secretsPath, err)
		} else {
			secretToken = secretValues.Server.Token
			secretKey = secretValues.Server.Security.EncryptionKey
			merged, mergeErr := mergeYAML(data, secretData)
			if mergeErr != nil {
				log.Printf("config: ignoring unmergeable secrets file %s: %v", secretsPath, mergeErr)
			} else {
				cfg = Default(dbPath)
				if err := yaml.Unmarshal(merged, cfg); err != nil {
					log.Printf("config: ignoring invalid merged secrets file %s: %v", secretsPath, err)
				} else {
					cfg.Web.CSP.ModeExplicit = cspModeIsExplicit(data)
				}
			}
		}
	}
	needsMigration := false
	if secretToken == "" && legacyToken != "" {
		cfg.Server.Token = legacyToken
		needsMigration = true
	}
	if secretKey == "" && legacyKey != "" {
		cfg.Server.Security.EncryptionKey = legacyKey
		needsMigration = true
	}
	if needsMigration {
		migration := &Config{}
		if secretToken == "" {
			migration.Server.Token = legacyToken
		}
		if secretKey == "" {
			migration.Server.Security.EncryptionKey = legacyKey
		}
		if err := SaveSecrets(path, migration); err != nil {
			return nil, fmt.Errorf("config: migrate secrets: %w", err)
		}
		if err := removeMigratedSecrets(path, migration); err != nil {
			return nil, fmt.Errorf("config: remove migrated secrets: %w", err)
		}
	}
	if cfg.Web.UI.Theme == "" {
		cfg.Web.UI.Theme = "dark"
	}
	if document, parseErr := parseConfigDocument(data); parseErr == nil {
		cfg.Server.Cache.tlsSkipVerifySet = yamlPathValue(document.Content[0], "server.cache.tls_skip_verify") != nil
		cfg.databaseURLConfigured = yamlPathValue(document.Content[0], "server.database.url") != nil
		cfg.cacheURLConfigured = yamlPathValue(document.Content[0], "server.cache.url") != nil
	}
	if secretErr == nil {
		if document, parseErr := parseConfigDocument(secretData); parseErr == nil {
			cfg.databaseURLConfigured = cfg.databaseURLConfigured || yamlPathValue(document.Content[0], "server.database.url") != nil
			cfg.cacheURLConfigured = cfg.cacheURLConfigured || yamlPathValue(document.Content[0], "server.cache.url") != nil
		}
	}
	ApplyConnectionEnvironment(cfg)
	return cfg, nil
}

// readMergedConfig reads server.yml, ordered yaml.d drop-ins, then secrets.yml.
func readMergedConfig(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("config: ignoring unreadable server.yml %s: %v", path, err)
		data = []byte("{}\n")
	}
	if err != nil {
		data = []byte("{}\n")
	}
	if _, err := parseConfigDocument(data); err != nil {
		log.Printf("config: ignoring invalid server.yml %s: %v", path, err)
		data = []byte("{}\n")
	}
	configDir := filepath.Dir(path)
	dropDir := filepath.Join(configDir, "yaml.d")
	entries, err := os.ReadDir(dropDir)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("config: ignoring unreadable drop-in directory %s: %v", dropDir, err)
	}
	merged := data
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		candidate := filepath.Join(dropDir, name)
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			log.Printf("config: ignoring unreadable drop-in %s: %v", candidate, statErr)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		real, realErr := filepath.EvalSymlinks(candidate)
		if realErr != nil {
			log.Printf("config: ignoring unresolvable drop-in %s: %v", candidate, realErr)
			continue
		}
		realDir, dirErr := filepath.EvalSymlinks(dropDir)
		if dirErr != nil {
			log.Printf("config: ignoring drop-in %s: %v", candidate, dirErr)
			continue
		}
		rel, relErr := filepath.Rel(realDir, real)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		part, readErr := os.ReadFile(candidate)
		if readErr != nil {
			log.Printf("config: ignoring unreadable drop-in %s: %v", candidate, readErr)
			continue
		}
		if _, parseErr := parseConfigDocument(part); parseErr != nil {
			log.Printf("config: ignoring invalid drop-in %s: %v", candidate, parseErr)
			continue
		}
		merged, err = mergeYAML(merged, part)
		if err != nil {
			log.Printf("config: ignoring unmergeable drop-in %s: %v", candidate, err)
			continue
		}
	}
	return merged, nil
}

func parseConfigDocument(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}, nil
	}
	if document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("configuration root must be a mapping")
	}
	if err := validateConfigNode(document.Content[0]); err != nil {
		return nil, err
	}
	return &document, nil
}

func removeMigratedSecrets(path string, migrated *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	document, err := parseConfigDocument(data)
	if err != nil {
		return err
	}
	root := document.Content[0]
	server := yamlMapValue(root, "server")
	if server == nil {
		return nil
	}
	if migrated.Server.Token != "" {
		yamlRemoveMapKey(server, "token")
	}
	if migrated.Server.Security.EncryptionKey != "" {
		security := yamlMapValue(server, "security")
		if security != nil {
			yamlRemoveMapKey(security, "encryption_key")
			if len(security.Content) == 0 {
				yamlRemoveMapKey(server, "security")
			}
		}
	}
	if connectionHasCredentials(migrated.Server.Database.URL) {
		if database := yamlMapValue(server, "database"); database != nil {
			yamlRemoveMapKey(database, "url")
		}
	}
	if migrated.Server.Database.Token != "" {
		if database := yamlMapValue(server, "database"); database != nil {
			yamlRemoveMapKey(database, "token")
		}
	}
	if connectionHasCredentials(migrated.Server.Cache.URL) {
		if cache := yamlMapValue(server, "cache"); cache != nil {
			yamlRemoveMapKey(cache, "url")
		}
	}
	var output strings.Builder
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return writeAtomic(path, []byte(strings.TrimRight(output.String(), "\n")+"\n"), 0o600)
}

func yamlMapValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key && mapping.Content[i+1].Kind == yaml.MappingNode {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func yamlRemoveMapKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func validateConfigNode(node *yaml.Node) error {
	if node.Tag == "!!null" {
		return errors.New("explicit null values are not allowed")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("duplicate key %q", key.Value)
			}
			seen[key.Value] = struct{}{}
			if err := validateConfigNode(node.Content[i+1]); err != nil {
				return fmt.Errorf("key %q: %w", key.Value, err)
			}
		}
	} else {
		for _, child := range node.Content {
			if err := validateConfigNode(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func mergeYAML(base, overlay []byte) ([]byte, error) {
	left, err := parseConfigDocument(base)
	if err != nil {
		return nil, err
	}
	right, err := parseConfigDocument(overlay)
	if err != nil {
		return nil, err
	}
	mergeYAMLNode(left.Content[0], right.Content[0])
	var output strings.Builder
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(left); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte(output.String()), nil
}

func mergeYAMLNode(base, overlay *yaml.Node) {
	for i := 0; i+1 < len(overlay.Content); i += 2 {
		key, value := overlay.Content[i], overlay.Content[i+1]
		found := -1
		for j := 0; j+1 < len(base.Content); j += 2 {
			if base.Content[j].Value == key.Value {
				found = j + 1
				break
			}
		}
		if found >= 0 && base.Content[found].Kind == yaml.MappingNode && value.Kind == yaml.MappingNode {
			mergeYAMLNode(base.Content[found], value)
			continue
		}
		copyKey, copyValue := *key, *value
		if found >= 0 {
			base.Content[found] = &copyValue
		} else {
			base.Content = append(base.Content, &copyKey, &copyValue)
		}
	}
}

// EnforcePrivatePaths creates the config directory and applies the private
// modes required for server.yml, secrets.yml, yaml.d, legacy operator HTML,
// and the branding assets directory.
func EnforcePrivatePaths(configDir string) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("config: create private directory %s: %w", configDir, err)
	}
	if err := os.Chmod(configDir, 0o700); err != nil {
		return fmt.Errorf("config: secure private directory %s: %w", configDir, err)
	}
	assetsDir := filepath.Join(configDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		return fmt.Errorf("config: create branding assets directory %s: %w", assetsDir, err)
	}
	if err := os.Chmod(assetsDir, 0o700); err != nil {
		return fmt.Errorf("config: secure branding assets directory %s: %w", assetsDir, err)
	}
	for _, name := range []string{"server.yml", "secrets.yml"} {
		path := filepath.Join(configDir, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("config: inspect private file %s: %w", path, err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("config: secure private file %s: %w", path, err)
		}
	}
	for _, name := range []string{"yaml.d", "html", "assets"} {
		root := filepath.Join(configDir, name)
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("config: inspect private directory %s: %w", root, err)
		}
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(path)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(root, target)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
					return fmt.Errorf("symlink escapes private directory: %s", path)
				}
				info, err = os.Stat(path)
				if err != nil {
					return err
				}
			}
			perm := os.FileMode(0o600)
			if info.IsDir() {
				perm = 0o700
			}
			return os.Chmod(path, perm)
		}); err != nil {
			return fmt.Errorf("config: secure private directory %s: %w", root, err)
		}
	}
	return nil
}

// PeekServerPort reads only server.port for the root startup phase, before
// the full configuration is loaded after privileged ports have been bound.
func PeekServerSettings(path string) (address, port string, err error) {
	type startupProbe struct {
		Server struct {
			Address *string `yaml:"address"`
			Listen  *string `yaml:"listen"`
			Port    *string `yaml:"port"`
		} `yaml:"server"`
	}
	files := []string{path}
	dropDir := filepath.Join(filepath.Dir(path), "yaml.d")
	entries, readErr := os.ReadDir(dropDir)
	if readErr == nil {
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && (filepath.Ext(entry.Name()) == ".yml" || filepath.Ext(entry.Name()) == ".yaml") {
				files = append(files, filepath.Join(dropDir, entry.Name()))
			}
		}
	}
	probe := startupProbe{}
	for _, file := range files {
		data, fileErr := os.ReadFile(file)
		if fileErr != nil {
			continue
		}
		var overlay startupProbe
		if yaml.Unmarshal(data, &overlay) != nil {
			continue
		}
		if overlay.Server.Address != nil {
			probe.Server.Address = overlay.Server.Address
			probe.Server.Listen = nil
		} else if overlay.Server.Listen != nil {
			probe.Server.Listen = overlay.Server.Listen
			probe.Server.Address = nil
		}
		if overlay.Server.Port != nil {
			probe.Server.Port = overlay.Server.Port
		}
	}
	if probe.Server.Address != nil {
		address = *probe.Server.Address
	} else if probe.Server.Listen != nil {
		address = *probe.Server.Listen
	}
	if probe.Server.Port != nil {
		port = *probe.Server.Port
	}
	return strings.TrimSpace(address), strings.TrimSpace(port), nil
}

// PeekServerPort remains a narrow convenience for callers that need only
// the port; startup uses PeekServerSettings to bind the configured address.
func PeekServerPort(path string) (string, error) {
	_, port, err := PeekServerSettings(path)
	return port, err
}

// cspModeExplicitProbe re-decodes just `web.csp.mode` as a pointer so a
// key the operator actually wrote can be told apart from the default
// Default() supplies.
type cspModeExplicitProbe struct {
	Web struct {
		CSP struct {
			Mode *string `yaml:"mode"`
		} `yaml:"csp"`
	} `yaml:"web"`
}

// cspModeIsExplicit reports whether the config file itself sets
// `web.csp.mode`. AI.md PART 11 "Report-Only Mode" auto-applies
// report-only in development "unless `mode: enforce` set explicitly", so
// the enforcing default alone must not defeat the downgrade.
func cspModeIsExplicit(data []byte) bool {
	var probe cspModeExplicitProbe
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Web.CSP.Mode != nil
}

// Save writes cfg to path as server.yml, creating parent directories as
// needed.
//
// The write is atomic: the new content goes to a temporary file in the
// same directory and is renamed over path only once it is completely
// written and fsync'd. A crash or full disk part-way through an in-place
// truncate-and-write would leave the server with invalid configuration.
func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("config: create dir for %s: %w", path, err)
	}
	if cfg.Server.Token != "" || cfg.Server.Security.EncryptionKey != "" || cfg.Server.Database.Token != "" || cfg.Server.Metrics.Auth.Tokens.Prometheus != "" || cfg.Server.Metrics.Auth.Tokens.Grafana != "" || cfg.Server.Metrics.Auth.Tokens.Loki != "" || ((!cfg.databaseURLEnv || cfg.databaseURLSeeded) && connectionHasCredentials(cfg.Server.Database.URL)) || ((!cfg.cacheURLEnv || cfg.cacheURLSeeded) && connectionHasCredentials(cfg.Server.Cache.URL)) {
		if err := SaveSecrets(path, cfg); err != nil {
			return err
		}
	}
	var indented strings.Builder
	enc := yaml.NewEncoder(&indented)
	enc.SetIndent(2)
	fileConfig := *cfg
	fileConfig.Server.Token = ""
	fileConfig.Server.Security.EncryptionKey = ""
	fileConfig.Server.Database.Token = ""
	if cfg.databaseURLEnv && !cfg.databaseURLSeeded || connectionHasCredentials(cfg.Server.Database.URL) {
		fileConfig.Server.Database.URL = ""
	}
	if cfg.cacheURLEnv && !cfg.cacheURLSeeded || connectionHasCredentials(cfg.Server.Cache.URL) {
		fileConfig.Server.Cache.URL = ""
	}
	if err := enc.Encode(&fileConfig); err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	data := []byte(strings.TrimRight(indented.String(), "\n") + "\n")
	data, err := stripBrandingConfig(data)
	if err != nil {
		return fmt.Errorf("config: remove branding from server.yml: %w", err)
	}
	data = append([]byte(serverConfigHeader), data...)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("config: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	if err := MigrateSecretsFiles(path); err != nil {
		return err
	}
	return MigrateAreaFiles(path)
}

func stripBrandingConfig(data []byte) ([]byte, error) {
	document, err := parseConfigDocument(data)
	if err != nil {
		return nil, err
	}
	root := document.Content[0]
	if server := yamlMapValue(root, "server"); server != nil {
		yamlRemoveMapKey(server, "branding")
		if seo := yamlMapValue(server, "seo"); seo != nil {
			for _, key := range []string{"keywords", "author", "og_image", "twitter_handle", "verification"} {
				yamlRemoveMapKey(seo, key)
			}
			if len(seo.Content) == 0 {
				yamlRemoveMapKey(server, "seo")
			}
		}
	}
	if web := yamlMapValue(root, "web"); web != nil {
		if ui := yamlMapValue(web, "ui"); ui != nil {
			yamlRemoveMapKey(ui, "theme")
			yamlRemoveMapKey(ui, "accent")
			if len(ui.Content) == 0 {
				yamlRemoveMapKey(web, "ui")
			}
		}
	}
	var output strings.Builder
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(output.String(), "\n") + "\n"), nil
}

// SaveSecrets atomically writes the server-generated credentials to the
// private secrets.yml file beside server.yml.
func SaveSecrets(configPath string, cfg *Config) error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create secrets dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("config: secure secrets dir %s: %w", dir, err)
	}
	secrets := map[string]any{}
	if existing, err := os.ReadFile(filepath.Join(dir, "secrets.yml")); err == nil {
		if err := yaml.Unmarshal(existing, &secrets); err != nil {
			return fmt.Errorf("config: parse secrets.yml: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("config: read secrets.yml: %w", err)
	}
	server, _ := secrets["server"].(map[string]any)
	if server == nil {
		server = map[string]any{}
	}
	if cfg.Server.Token != "" {
		server["token"] = cfg.Server.Token
	}
	security, _ := server["security"].(map[string]any)
	if security == nil {
		security = map[string]any{}
	}
	if cfg.Server.Security.EncryptionKey != "" {
		security["encryption_key"] = cfg.Server.Security.EncryptionKey
	}
	if (!cfg.databaseURLEnv || cfg.databaseURLSeeded) && connectionHasCredentials(cfg.Server.Database.URL) {
		database, _ := server["database"].(map[string]any)
		if database == nil {
			database = map[string]any{}
		}
		database["url"] = cfg.Server.Database.URL
		server["database"] = database
	}
	if cfg.Server.Database.Token != "" {
		database, _ := server["database"].(map[string]any)
		if database == nil {
			database = map[string]any{}
		}
		database["token"] = cfg.Server.Database.Token
		server["database"] = database
	}
	if (!cfg.cacheURLEnv || cfg.cacheURLSeeded) && connectionHasCredentials(cfg.Server.Cache.URL) {
		cache, _ := server["cache"].(map[string]any)
		if cache == nil {
			cache = map[string]any{}
		}
		cache["url"] = cfg.Server.Cache.URL
		server["cache"] = cache
	}
	if cfg.Server.Security.EncryptionKeyVersion > 0 {
		security["encryption_key_version"] = cfg.Server.Security.EncryptionKeyVersion
	}
	if len(security) > 0 {
		server["security"] = security
	}
	metrics, _ := server["metrics"].(map[string]any)
	if metrics == nil {
		metrics = map[string]any{}
	}
	auth, _ := metrics["auth"].(map[string]any)
	if auth == nil {
		auth = map[string]any{}
	}
	tokens, _ := auth["tokens"].(map[string]any)
	if tokens == nil {
		tokens = map[string]any{}
	}
	for key, value := range map[string]string{"prometheus": cfg.Server.Metrics.Auth.Tokens.Prometheus, "grafana": cfg.Server.Metrics.Auth.Tokens.Grafana, "loki": cfg.Server.Metrics.Auth.Tokens.Loki} {
		if value != "" {
			tokens[key] = value
		}
	}
	if len(tokens) > 0 {
		auth["tokens"] = tokens
		metrics["auth"] = auth
		server["metrics"] = metrics
	}
	secrets["server"] = server
	var output strings.Builder
	enc := yaml.NewEncoder(&output)
	enc.SetIndent(2)
	if err := enc.Encode(secrets); err != nil {
		return fmt.Errorf("config: encode secrets: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("config: encode secrets: %w", err)
	}
	return writeAtomic(filepath.Join(dir, "secrets.yml"), []byte(strings.TrimRight(output.String(), "\n")+"\n"), 0o600)
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	return nil
}

// EnsureToken generates a fresh operator token (`adm_` + 32 URL-safe base62
// chars) when cfg.Server.Token is empty, per AI.md PART 11 "API Token
// Model". Returns true when a new token was generated.
func EnsureToken(cfg *Config) (bool, error) {
	if validGeneratedToken(cfg.Server.Token, "adm") {
		return false, nil
	}
	tok, err := generateToken("adm")
	if err != nil {
		return false, err
	}
	cfg.Server.Token = tok
	return true, nil
}

// EnsureMetricsTokens creates missing bearer tokens for each metrics service.
func EnsureMetricsTokens(cfg *Config) (bool, error) {
	generated := false
	values := []*string{&cfg.Server.Metrics.Auth.Tokens.Prometheus, &cfg.Server.Metrics.Auth.Tokens.Grafana, &cfg.Server.Metrics.Auth.Tokens.Loki}
	for _, value := range values {
		if validGeneratedToken(*value, "mtr") {
			continue
		}
		token, err := generateToken("mtr")
		if err != nil {
			return false, err
		}
		*value = token
		generated = true
	}
	return generated, nil
}

func validGeneratedToken(token, prefix string) bool {
	if len(token) != len(prefix)+1+security.TokenLength || !strings.HasPrefix(token, prefix+"_") {
		return false
	}
	for _, char := range token[len(prefix)+1:] {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		default:
			return false
		}
	}
	return true
}

// ValidMetricsToken reports whether a configured metrics credential uses the
// required mtr_ prefix and 32-character base62 token format.
func ValidMetricsToken(token string) bool { return validGeneratedToken(token, "mtr") }

func generateToken(prefix string) (string, error) {
	tok, err := security.GenerateTokenWithPrefix(prefix)
	if err != nil {
		return "", fmt.Errorf("config: generate token: %w", err)
	}
	return tok, nil
}
