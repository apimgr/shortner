package notify

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

// Options builds a Notifier. Everything is resolved once at startup:
// AI.md PART 17 "SMTP Requirement" requires the SMTP status be checked
// "once at startup and on config change", never per send.
type Options struct {
	Email config.EmailNotifications
	Store *Store

	// AppName, AppURL and FQDN back the global template variables of the
	// same name.
	AppName string
	AppURL  string
	FQDN    string
	// OnionAddress and I2PAddress are the PART 31 overlay addresses, empty
	// until those subsystems exist. An empty value renders the matching
	// `{onion_*}`/`{i2p_*}` placeholders as empty, never as a broken URL.
	OnionAddress string
	I2PAddress   string

	// Recipients are the operator addresses every event email goes to.
	Recipients []string
	// Version and Mode back the startup/shutdown templates.
	Version string
	Mode    string
	// Log receives one ERROR line per failed delivery. Nil disables it.
	Log *applog.Logger
}

// Notifier is the single entry point for every outbound email. A nil
// *Notifier is valid and inert: every method reports "not enabled" and
// sends nothing, so callers never need a nil check of their own.
type Notifier struct {
	mu    sync.RWMutex
	opts  Options
	ready bool
}

// New builds a Notifier in the disabled state. Email stays off until
// Startup succeeds — AI.md PART 17's "No SMTP = No emails. Don't even
// try." is enforced by construction, not by a runtime flag someone might
// forget to check.
func New(opts Options) *Notifier {
	if opts.Store == nil {
		opts.Store = &Store{}
	}
	return &Notifier{opts: opts}
}

// StartupResult describes what the startup SMTP check did, so main can
// print the AI.md PART 17 "Display Note" console line (the detected
// `{host}:{port}` is deliberately shown even when it is a loopback
// address).
type StartupResult struct {
	// Enabled is the resulting `email.configured` state.
	Enabled bool
	// Host and Port are the server that was tested or detected.
	Host string
	Port int
	// Detected is true when auto-detection (rather than an existing
	// config value) chose this server, which means the caller must persist
	// the config.
	Detected bool
	// Source is the auto-detection priority label, when Detected.
	Source string
	// Err is the connection-test failure that disabled email, if any.
	Err error
}

// Startup performs the AI.md PART 17 startup sequence. With no host
// configured it runs "SMTP Auto-Detection" and, on success, returns the
// detected host/port for the caller to save to server.yml. With a host
// configured it runs the "Connection Test (when host is set)" instead: a
// failure disables email, logs a warning, and lets the server keep
// running — retried on the next startup, never queued.
func (n *Notifier) Startup(host string) StartupResult {
	if n == nil {
		return StartupResult{}
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	smtpCfg := n.opts.Email.SMTP
	if strings.TrimSpace(smtpCfg.Host) == "" {
		c, ok := Detect(host, smtpCfg)
		if !ok {
			n.ready = false
			n.opts.Email.Enabled = false
			return StartupResult{}
		}
		n.opts.Email.SMTP.Host = c.Host
		n.opts.Email.SMTP.Port = c.Port
		n.ready = true
		n.opts.Email.Enabled = true
		return StartupResult{Enabled: true, Host: c.Host, Port: c.Port, Detected: true, Source: c.Source}
	}

	if err := probe(smtpCfg); err != nil {
		n.ready = false
		n.opts.Email.Enabled = false
		return StartupResult{Host: smtpCfg.Host, Port: smtpCfg.Port, Err: err}
	}
	n.ready = true
	n.opts.Email.Enabled = true
	return StartupResult{Enabled: true, Host: smtpCfg.Host, Port: smtpCfg.Port}
}

// Enabled is the AI.md PART 17 "SMTP Check Before Sending" predicate:
// email notifications are enabled and a host is actually configured. It is
// also what gates every email-dependent UI element.
func (n *Notifier) Enabled() bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.ready && n.opts.Email.Enabled && strings.TrimSpace(n.opts.Email.SMTP.Host) != ""
}

// SMTPAddress returns the active `host:port`, or "" when email is off.
func (n *Notifier) SMTPAddress() string {
	if !n.Enabled() {
		return ""
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.opts.Email.SMTP.Host + ":" + strconv.Itoa(n.opts.Email.SMTP.Port)
}

// Store exposes the template store so the CLI and the template editor can
// list, save, and reset templates.
func (n *Notifier) Store() *Store {
	if n == nil {
		return &Store{}
	}
	return n.opts.Store
}

// ErrDisabled is returned by every send path when SMTP is unavailable. It
// is a sentinel, not a failure to report: callers log their own event line
// regardless and simply skip the email.
var ErrDisabled = errors.New("notify: email disabled (no working SMTP)")

// Send renders event's template and delivers it to the operator
// recipients. It performs, in order, the AI.md PART 17 "Decision Logic"
// gates: SMTP configured, then the per-event switch. A disabled event or
// disabled SMTP returns ErrDisabled without touching the network.
func (n *Notifier) Send(event string, vars map[string]string) error {
	if !n.Enabled() {
		return ErrDisabled
	}
	n.mu.RLock()
	events := n.opts.Email.Events
	recipients := append([]string(nil), n.opts.Recipients...)
	n.mu.RUnlock()

	if !EventEnabled(events, event) {
		return ErrDisabled
	}
	if len(recipients) == 0 {
		return ErrDisabled
	}
	return n.SendTo(event, recipients, vars)
}

// SendTo renders event's template and delivers it to explicit recipients,
// bypassing the per-event switch. It backs the addressed emails the spec
// defines outside the operator matrix — the researcher acknowledgment of
// PART 11 "Submission Flow" step 5 and the `email test` command.
func (n *Notifier) SendTo(event string, to []string, vars map[string]string) error {
	if !n.Enabled() {
		return ErrDisabled
	}
	if len(to) == 0 {
		return ErrDisabled
	}

	n.mu.RLock()
	store := n.opts.Store
	n.mu.RUnlock()

	tmpl, err := store.Load(event)
	if err != nil {
		// A broken custom override is reported once per send and, where
		// Load could fall back, has already been replaced by the embedded
		// default — so this is a log line, not a dropped notification.
		n.logf(applog.LevelError, "notify: template %s: %v", event, err)
		if tmpl.Name == "" {
			return err
		}
	}
	subject, body := tmpl.Render(n.variables(vars))
	return n.SendRaw(to, subject, body)
}

// SendRaw delivers a subject/body that did not come from a PART 17
// template. It exists for the two spec'd messages whose content is not a
// template: the security-report maintainer notification (PART 11
// "Submission Flow" step 4, whose body is an encrypted blob) and the
// visitor contact form (PART 16), whose body is the visitor's own text.
func (n *Notifier) SendRaw(to []string, subject, body string) error {
	if !n.Enabled() {
		return ErrDisabled
	}
	n.mu.RLock()
	email := n.opts.Email
	appName := n.opts.AppName
	host := n.opts.FQDN
	n.mu.RUnlock()

	msg := Message{
		FromName:  fromName(email.From.Name, appName),
		FromEmail: fromAddress(email.From.Email, host),
		ReplyTo:   email.ReplyTo,
		To:        to,
		Subject:   subject,
		Body:      body,
	}
	if err := sendMail(email.SMTP, msg); err != nil {
		n.logf(applog.LevelError, "notify: send %q: %v", subject, err)
		return err
	}
	return nil
}

// Preview renders a template with the sample data of AI.md PART 17
// "Sample Data for Preview", overlaid with any supplied values. It never
// touches SMTP, so it works with email disabled.
func (n *Notifier) Preview(event string, vars map[string]string) (subject, body string, err error) {
	tmpl, err := n.Store().Load(event)
	if err != nil && tmpl.Name == "" {
		return "", "", err
	}
	sample := n.variables(SampleVariables(event))
	for k, v := range vars {
		sample[k] = v
	}
	subject, body = tmpl.Render(sample)
	return subject, body, nil
}

// SendTest delivers the `test` template with sample data to one address,
// per AI.md PART 17 "Send Test Email": real SMTP, sample data, and a
// `[TEST]` subject prefix.
func (n *Notifier) SendTest(to string) (subject string, err error) {
	if !n.Enabled() {
		return "", ErrDisabled
	}
	tmpl, err := n.Store().Load(EventTest)
	if err != nil && tmpl.Name == "" {
		return "", err
	}
	subject, body := tmpl.Render(n.variables(SampleVariables(EventTest)))
	subject = "[TEST] " + subject
	return subject, n.SendRaw([]string{to}, subject, body)
}

// variables merges the AI.md PART 17 global variables with the caller's
// event-specific values. Caller values win, so an event that carries its
// own `{fqdn}` (the certificate's domain, in the SSL templates) overrides
// the server's own.
func (n *Notifier) variables(vars map[string]string) map[string]string {
	n.mu.RLock()
	o := n.opts
	n.mu.RUnlock()

	now := time.Now()
	out := map[string]string{
		"app_name":              o.AppName,
		"app_url":               o.AppURL,
		"fqdn":                  o.FQDN,
		"onion_address":         o.OnionAddress,
		"onion_url":             overlayURL(o.OnionAddress),
		"i2p_address":           o.I2PAddress,
		"i2p_url":               overlayURL(o.I2PAddress),
		"notification_reply_to": o.Email.ReplyTo,
		"timestamp":             now.Format(time.RFC1123),
		"year":                  strconv.Itoa(now.Year()),
		"version":               o.Version,
		"mode":                  o.Mode,
	}
	for k, v := range vars {
		out[k] = v
	}
	return out
}

// overlayURL builds the URL form of an overlay address. AI.md PART 12 and
// PART 31 are absolute that `.onion` and `.b32.i2p` are ALWAYS http:// —
// never https, never upgraded.
func overlayURL(addr string) string {
	if addr == "" {
		return ""
	}
	return "http://" + addr
}

// fromName resolves the From display name, per AI.md PART 17 "Default
// Sender": the configured name, else the app name.
func fromName(configured, appName string) string {
	if configured != "" {
		return configured
	}
	return appName
}

// fromAddress resolves the From address, per AI.md PART 17 "Default
// Sender": the configured address, else `no-reply@{fqdn}`, else
// `no-reply@localhost`.
func fromAddress(configured, host string) string {
	if configured != "" {
		return configured
	}
	if host == "" {
		return "no-reply@localhost"
	}
	return "no-reply@" + host
}

// SampleVariables returns the AI.md PART 17 "Sample Data for Preview"
// values for event. Globals are supplied by the live config, so only the
// event-specific placeholders need sample content here.
func SampleVariables(event string) map[string]string {
	vars := map[string]string{"ip": "192.168.1.100"}
	for _, name := range eventVariables[event] {
		if _, ok := vars[name]; ok {
			continue
		}
		vars[name] = sampleValues[name]
	}
	return vars
}

// sampleValues is the placeholder content the preview and the test email
// use for event-specific variables.
var sampleValues = map[string]string{
	"event":            "Rate limit exceeded",
	"details":          "42 requests in 60 seconds from a single address",
	"filename":         "backup-full-20260101-020000.tar.gz",
	"size":             "12.4 MB",
	"error":            "connection refused",
	"expires_in":       "7",
	"expiry_date":      "2026-01-08",
	"valid_until":      "2026-04-08",
	"next_retry":       "in 6 hours",
	"task_name":        "backup_daily",
	"next_run":         "tomorrow at 02:00",
	"current_version":  "1.0.0",
	"new_version":      "1.1.0",
	"previous_version": "1.0.0",
	"channel":          "stable",
	"version":          "1.0.0",
	"mode":             "production",
}

// logf writes one line to the configured log, if any.
func (n *Notifier) logf(level applog.Level, format string, args ...any) {
	if n == nil || n.opts.Log == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	_ = n.opts.Log.WriteLine(level, applog.FormatText(time.Now(), level.String(), msg))
}
