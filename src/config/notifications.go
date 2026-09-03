// Notification configuration, per AI.md PART 17 "SMTP Config",
// "Environment Variable Priority", and "Configuration".
//
// Everything here is operator-configurable from server.yml alone; the
// SMTP_* environment variables layered on top exist for containers, where
// the config file is often baked into the image.
package config

import (
	"os"
	"strconv"
	"strings"
)

// Notifications holds `server.notifications`, per AI.md PART 17
// "Configuration".
type Notifications struct {
	WebUI NotificationsWebUI `yaml:"webui"`
	Email EmailNotifications `yaml:"email"`
}

// NotificationsWebUI holds `server.notifications.webui` — the visitor-
// facing toast settings of AI.md PART 17 "Public WebUI Notification
// System". Nothing here is ever used for an operator event.
type NotificationsWebUI struct {
	// Position is one of top-right, top-left, bottom-right, bottom-left.
	Position string `yaml:"position"`
	// Duration is the auto-dismiss time in seconds (0 = manual dismiss).
	Duration int `yaml:"duration"`
}

// EmailNotifications holds `server.notifications.email`.
//
// Enabled is deliberately not an operator toggle: AI.md PART 17 says it
// "is auto-set based on SMTP availability (no manual toggle)". It is
// written at startup by the SMTP detection/connection test and is
// persisted only so `--status` and the startup log can report it.
type EmailNotifications struct {
	Enabled bool      `yaml:"enabled"`
	SMTP    SMTP      `yaml:"smtp"`
	From    EmailFrom `yaml:"from"`
	// ReplyTo backs the `{notification_reply_to}` template variable and the
	// Reply-To header, per AI.md PART 17 "Default Sender". Omitted from the
	// message entirely when empty.
	ReplyTo string      `yaml:"reply_to"`
	Events  EmailEvents `yaml:"events"`
}

// SMTP holds `server.notifications.email.smtp`. An empty Host means
// "autodetect on first run"; a set Host means "test this on every
// startup".
type SMTP struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// TLS is one of auto, starttls, tls, none.
	TLS string `yaml:"tls"`
}

// EmailFrom holds `server.notifications.email.from`. Empty values fall
// back to the app title and `no-reply@{fqdn}` at send time.
type EmailFrom struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// EmailEvents holds `server.notifications.email.events` — the per-event
// switches of AI.md PART 17 "Configuration". The defaults mirror the
// spec's own block exactly; anything the "Operator Notifications" matrix
// marks Optional defaults to false, anything it marks required defaults to
// true.
type EmailEvents struct {
	Startup          bool `yaml:"startup"`
	Shutdown         bool `yaml:"shutdown"`
	BackupComplete   bool `yaml:"backup_complete"`
	BackupFailed     bool `yaml:"backup_failed"`
	SSLExpiring      bool `yaml:"ssl_expiring"`
	SSLRenewed       bool `yaml:"ssl_renewed"`
	SSLRenewalFailed bool `yaml:"ssl_renewal_failed"`
	SecurityAlert    bool `yaml:"security_alert"`
	SchedulerError   bool `yaml:"scheduler_error"`
	UpdateAvailable  bool `yaml:"update_available"`
	UpdateInstalled  bool `yaml:"update_installed"`
}

// TLS mode values accepted by `server.notifications.email.smtp.tls`.
const (
	SMTPTLSAuto     = "auto"
	SMTPTLSStartTLS = "starttls"
	SMTPTLSDirect   = "tls"
	SMTPTLSNone     = "none"
)

// smtpTLSModes is the closed vocabulary of AI.md PART 17 "SMTP Config".
var smtpTLSModes = []string{SMTPTLSAuto, SMTPTLSStartTLS, SMTPTLSDirect, SMTPTLSNone}

// webUIPositions is the closed vocabulary of AI.md PART 17
// "Configuration" -> `notifications.webui.position`.
var webUIPositions = []string{"top-right", "top-left", "bottom-right", "bottom-left"}

// DefaultNotifications returns the AI.md PART 17 "Configuration" block
// verbatim: autodetect SMTP, port 587, TLS auto, and the spec's own
// per-event defaults.
func DefaultNotifications() Notifications {
	return Notifications{
		WebUI: NotificationsWebUI{
			Position: "top-right",
			Duration: 5,
		},
		Email: EmailNotifications{
			SMTP: SMTP{
				Host: "",
				Port: 587,
				TLS:  SMTPTLSAuto,
			},
			Events: EmailEvents{
				Startup:          false,
				Shutdown:         false,
				BackupComplete:   false,
				BackupFailed:     true,
				SSLExpiring:      true,
				SSLRenewed:       false,
				SSLRenewalFailed: true,
				SecurityAlert:    true,
				SchedulerError:   true,
				UpdateAvailable:  false,
				UpdateInstalled:  true,
			},
		},
	}
}

// ApplySMTPEnv layers the SMTP_* environment variables over the config
// file, per AI.md PART 17 "Environment Variable Priority". An unset (or
// empty) variable changes nothing, so a container can override exactly the
// fields it cares about. It returns the names of the variables that were
// actually applied so startup can log which settings came from the
// environment.
func ApplySMTPEnv(cfg *Config) []string {
	e := &cfg.Server.Notifications.Email
	var applied []string

	str := func(name string, dst *string) {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			*dst = v
			applied = append(applied, name)
		}
	}

	str("SMTP_HOST", &e.SMTP.Host)
	if v, ok := os.LookupEnv("SMTP_PORT"); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && validSMTPPort(n) {
			e.SMTP.Port = n
			applied = append(applied, "SMTP_PORT")
		}
	}
	str("SMTP_USERNAME", &e.SMTP.Username)
	str("SMTP_PASSWORD", &e.SMTP.Password)
	str("SMTP_TLS", &e.SMTP.TLS)
	str("SMTP_FROM_NAME", &e.From.Name)
	str("SMTP_FROM_EMAIL", &e.From.Email)

	return applied
}

// validSMTPPort reports whether n is a usable TCP port.
func validSMTPPort(n int) bool {
	return n > 0 && n < 65536
}

// validateNotifications applies the AI.md PART 12 "Config Validation Rule"
// to `server.notifications`: an invalid value is replaced with its
// framework default and reported, never fatal.
func validateNotifications(cfg *Config, defaults *Config) []string {
	var warnings []string
	n := &cfg.Server.Notifications
	def := defaults.Server.Notifications

	n.WebUI.Position = validateEnum("server.notifications.webui.position",
		n.WebUI.Position, def.WebUI.Position, webUIPositions, &warnings)
	if n.WebUI.Duration < 0 {
		warnings = append(warnings, "invalid server.notifications.webui.duration "+
			strconv.Itoa(n.WebUI.Duration)+", using default "+strconv.Itoa(def.WebUI.Duration))
		n.WebUI.Duration = def.WebUI.Duration
	}

	n.Email.SMTP.TLS = validateEnum("server.notifications.email.smtp.tls",
		n.Email.SMTP.TLS, def.Email.SMTP.TLS, smtpTLSModes, &warnings)
	if !validSMTPPort(n.Email.SMTP.Port) {
		warnings = append(warnings, "invalid server.notifications.email.smtp.port "+
			strconv.Itoa(n.Email.SMTP.Port)+", using default "+strconv.Itoa(def.Email.SMTP.Port))
		n.Email.SMTP.Port = def.Email.SMTP.Port
	}

	n.Email.SMTP.Host = strings.TrimSpace(n.Email.SMTP.Host)
	n.Email.From.Email = strings.TrimSpace(n.Email.From.Email)
	n.Email.ReplyTo = strings.TrimSpace(n.Email.ReplyTo)

	// A From/Reply-To address that is not an address at all would be
	// rejected by every MTA at RCPT time, so it is dropped here rather than
	// left to fail one delivery at a time.
	if n.Email.From.Email != "" && !looksLikeEmail(n.Email.From.Email) {
		warnings = append(warnings, "invalid server.notifications.email.from.email "+
			strconv.Quote(n.Email.From.Email)+", falling back to no-reply@{fqdn}")
		n.Email.From.Email = ""
	}
	if n.Email.ReplyTo != "" && !looksLikeEmail(n.Email.ReplyTo) {
		warnings = append(warnings, "invalid server.notifications.email.reply_to "+
			strconv.Quote(n.Email.ReplyTo)+", Reply-To omitted")
		n.Email.ReplyTo = ""
	}

	return warnings
}

// looksLikeEmail is a deliberately minimal check: exactly one @, non-empty
// local and domain parts, a dot-bearing domain, and no whitespace or
// header-injection characters. Full RFC 5322 validation is not the point —
// keeping a malformed value out of an SMTP envelope and out of a header is.
func looksLikeEmail(addr string) bool {
	if strings.ContainsAny(addr, " \t\r\n<>,;\"") {
		return false
	}
	at := strings.Index(addr, "@")
	if at <= 0 || at != strings.LastIndex(addr, "@") || at == len(addr)-1 {
		return false
	}
	domain := addr[at+1:]
	dot := strings.Index(domain, ".")
	return dot > 0 && dot < len(domain)-1
}
