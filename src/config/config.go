// Package config loads and saves server.yml, the sole source of truth for
// operator configuration. See AI.md PART 5 for the authoritative schema.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/apimgr/shortner/src/security"
)

// Config is the root of server.yml.
type Config struct {
	Server Server `yaml:"server"`
	Web    Web    `yaml:"web"`
	Pages  Pages  `yaml:"pages"`
}

// Server holds the top-level `server:` block of server.yml.
type Server struct {
	// Token is the global operator token (tok_ prefix). Auto-generated on
	// first run if empty. See AI.md PART 11 "API Token Model".
	Token          string         `yaml:"token"`
	Listen         string         `yaml:"listen"`
	Port           string         `yaml:"port"`
	BaseURL        string         `yaml:"baseurl"`
	Database       Database       `yaml:"database"`
	Limits         Limits         `yaml:"limits"`
	Compression    Compression    `yaml:"compression"`
	TrustedProxies TrustedProxies `yaml:"trusted_proxies"`
	RateLimit      RateLimit      `yaml:"rate_limit"`
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
	Schedule string `yaml:"schedule"`
	Enabled  bool   `yaml:"enabled"`
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

// Branding holds `server.branding`, per AI.md PART 16 "Branding & SEO".
// Remote image fetching/scaling is not implemented yet (see TODO.AI.md) —
// only the static local/URL fields are wired.
type Branding struct {
	SiteName string `yaml:"site_name"`
	Tagline  string `yaml:"tagline"`
	LogoURL  string `yaml:"logo_url"`
}

// SEO holds `server.seo`, per AI.md PART 16 "SEO Meta Tags". Site-
// verification meta tags are not implemented yet (see TODO.AI.md).
type SEO struct {
	Description string `yaml:"description"`
	Keywords    string `yaml:"keywords"`
}

// Contact holds `server.contact`, per AI.md PART 16 "/server/contact" ->
// "Configuration". Admin.Email is never rendered on the public contact
// page — only General.Email and Abuse.Email are, per the page's spec'd
// "Abuse Reports" section.
type Contact struct {
	General ContactRecipient `yaml:"general"`
	Admin   ContactRecipient `yaml:"admin"`
	Abuse   ContactRecipient `yaml:"abuse"`
}

// ContactRecipient is one named contact-form recipient.
type ContactRecipient struct {
	Email string `yaml:"email"`
}

// Privacy holds `server.privacy`, per AI.md PART 16 "/server/privacy" ->
// "Privacy Configuration (config file)".
type Privacy struct {
	Data       PrivacyData       `yaml:"data"`
	Consent    PrivacyConsent    `yaml:"consent"`
	Cookies    PrivacyCookies    `yaml:"cookies"`
	Content    PrivacyContent    `yaml:"content"`
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

// PrivacyContent holds `server.privacy.content` (Markdown-flagged fields;
// rendered as plain paragraphs — a Markdown renderer is not wired yet, see
// TODO.AI.md).
type PrivacyContent struct {
	DataCollection  string `yaml:"data_collection"`
	DataUsage       string `yaml:"data_usage"`
	DataUsageIfSold string `yaml:"data_usage_if_sold"`
	DataSecurity    string `yaml:"data_security"`
}

// GetDataUsageContent returns DataUsageIfSold when Data.Sold is true, else
// DataUsage, per AI.md PART 16 "Dynamic Fields" -> "content.data_usage".
func (p Privacy) GetDataUsageContent() string {
	if p.Data.Sold && p.Content.DataUsageIfSold != "" {
		return p.Content.DataUsageIfSold
	}
	return p.Content.DataUsage
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
	Footer        WebFooter        `yaml:"footer"`
	Announcements WebAnnouncements `yaml:"announcements"`
	Theme         string           `yaml:"theme"`
}

// WebFooter holds `web.footer`.
type WebFooter struct {
	// CustomHTML is sanitized via src/common/sanitize.SanitizeFooterHTML
	// before every render — never trust this field directly in a
	// template. "" = default branding; " " = branding disabled.
	CustomHTML string `yaml:"custom_html"`
}

// WebAnnouncements holds `web.announcements`, per AI.md PART 16
// "Announcements".
type WebAnnouncements struct {
	Items []Announcement `yaml:"items"`
}

// Announcement is one operator-configured announcement banner entry.
type Announcement struct {
	ID      string `yaml:"id"`
	Message string `yaml:"message"`
	Level   string `yaml:"level"`
	Enabled bool   `yaml:"enabled"`
}

// Pages holds the top-level `pages:` block of server.yml, per AI.md
// PART 16 "Pages Configuration (config file)".
type Pages struct {
	About   PageContent `yaml:"about"`
	Privacy PageContent `yaml:"privacy"`
	Contact ContactPage `yaml:"contact"`
	Help    PageContent `yaml:"help"`
	Terms   PageContent `yaml:"terms"`
}

// PageContent is a simple operator-overridable content block (Markdown-
// flagged; see PrivacyContent's doc comment on Markdown rendering status).
type PageContent struct {
	Content string `yaml:"content"`
}

// ContactPage holds `pages.contact`.
type ContactPage struct {
	Enabled        bool   `yaml:"enabled"`
	Captcha        string `yaml:"captcha"`
	SuccessMessage string `yaml:"success_message"`
}

// TLS holds `server.tls`, per AI.md PART 15 "Built-in Let's Encrypt
// Support". DNSCredentials is stored as plaintext YAML for now — AES-256-
// GCM encryption at rest (spec: "credentials_encrypted") depends on an app
// secret-encryption primitive this codebase does not have yet (tracked in
// TODO.AI.md).
type TLS struct {
	// Enabled turns on HTTPS/ACME certificate handling for the resolved
	// FQDN. When false (default), the server is HTTP-only.
	Enabled bool `yaml:"enabled"`
	// DNSProvider selects a DNS-01 provider (e.g. "cloudflare", "route53")
	// for wildcard certificate issuance. DNS-01 issuance itself is not
	// implemented yet — see TODO.AI.md; HTTP-01/TLS-ALPN-01 via the
	// certificate lookup order + ACME fallback in src/certmgr are.
	DNSProvider string `yaml:"dns_provider"`
	// DNSCredentials holds the provider-specific credential fields (e.g.
	// api_token, access_key_id), per AI.md PART 15 "Provider Credential
	// Storage".
	DNSCredentials map[string]string `yaml:"dns_credentials"`
}

// Database holds the `server.database` block.
type Database struct {
	// Driver is "sqlite" (default, pure Go modernc.org/sqlite) or "libsql".
	Driver string `yaml:"driver"`
	URL    string `yaml:"url"`
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
	Enabled     bool           `yaml:"enabled"`
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

// CacheConfig holds `server.cache`, per AI.md PART 12 "Cache
// Configuration". Only the "memory" (in-process, default) driver is
// implemented — "valkey"/"redis" depend on a client dependency not yet
// added (tracked in TODO.AI.md).
type CacheConfig struct {
	Type          string `yaml:"type"`
	URL           string `yaml:"url"`
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	DB            int    `yaml:"db"`
	TLS           bool   `yaml:"tls"`
	TLSSkipVerify bool   `yaml:"tls_skip_verify"`
	PoolSize      int    `yaml:"pool_size"`
	MinIdle       int    `yaml:"min_idle"`
	Timeout       string `yaml:"timeout"`
	Prefix        string `yaml:"prefix"`
	TTL           string `yaml:"ttl"`
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
			Listen:  "0.0.0.0",
			Port:    "8090",
			BaseURL: "/",
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
				Enabled:     true,
				Read:        RateLimitClass{Requests: 120, Window: 60},
				Write:       RateLimitClass{Requests: 10, Window: 60},
				Health:      RateLimitClass{Requests: 120, Window: 60},
				GlobalBurst: 240,
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
			Contact: Contact{},
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
					"i2p_health":       {Schedule: "@every 10m", Enabled: false},
				},
			},
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
		},
		Web: Web{
			// Dark is the required default per AI.md PART 16's "Three
			// Required Themes" table (Dark: Default = YES, Auto: No).
			Theme: "dark",
		},
		Pages: Pages{
			Contact: ContactPage{
				Enabled:        true,
				Captcha:        "simple",
				SuccessMessage: "Thank you for your message. We'll respond soon.",
			},
		},
	}
}

// Load reads server.yml from path. If the file does not exist, it returns
// Default(dbPath) without writing anything — callers decide when to persist
// a first-run config via Save.
func Load(path, dbPath string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(dbPath), nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := Default(dbPath)
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to path as server.yml, creating parent directories as
// needed.
//
// The write is atomic: the new content goes to a temporary file in the
// same directory and is renamed over path only once it is completely
// written and fsync'd. server.yml holds the operator token, so a crash or
// full disk part-way through an in-place truncate-and-write would leave
// the server with no way to authenticate.
func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("config: create dir for %s: %w", path, err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("config: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
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
	return nil
}

// EnsureToken generates a fresh operator token (`tok_` + 32 URL-safe base62
// chars) when cfg.Server.Token is empty, per AI.md PART 11 "API Token
// Model". Returns true when a new token was generated.
func EnsureToken(cfg *Config) (bool, error) {
	if cfg.Server.Token != "" {
		return false, nil
	}
	tok, err := generateToken()
	if err != nil {
		return false, err
	}
	cfg.Server.Token = tok
	return true, nil
}

func generateToken() (string, error) {
	tok, err := security.GenerateToken()
	if err != nil {
		return "", fmt.Errorf("config: generate token: %w", err)
	}
	return tok, nil
}
