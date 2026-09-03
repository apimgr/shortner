// The AI.md PART 17 (Email & Notifications) startup wiring: build the
// notifier, run the startup SMTP check, persist an auto-detected server,
// and report the resulting state on the console.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/common/version"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/mode"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/paths"
)

// newNotifier builds the process-wide notify.Notifier from live config.
// It is always non-nil; email stays disabled until Startup succeeds.
func newNotifier(cfg *config.Config, p paths.Paths, fqdnHost string) *notify.Notifier {
	return notify.New(notify.Options{
		Email:      cfg.Server.Notifications.Email,
		Store:      notify.NewStore(p.Config),
		AppName:    appName(cfg),
		AppURL:     appURL(cfg, fqdnHost),
		FQDN:       fqdnHost,
		Recipients: operatorRecipients(cfg),
		Version:    version.String(),
		Mode:       mode.GetCurrentAppMode().String(),
	})
}

// appName is the `{app_name}` global template variable: the operator's
// configured site name (AI.md PART 16 "Branding"), falling back to the
// project name compiled into the binary.
func appName(cfg *config.Config) string {
	if n := strings.TrimSpace(cfg.Server.Branding.SiteName); n != "" {
		return n
	}
	return internalName
}

// appURL is the `{app_url}` global template variable: the operator's
// configured base URL when set, otherwise the FQDN with the scheme TLS
// implies.
func appURL(cfg *config.Config, fqdnHost string) string {
	if u := strings.TrimSpace(cfg.Server.BaseURL); strings.HasPrefix(u, "http") {
		return u
	}
	scheme := "http"
	if cfg.Server.TLS.Enabled {
		scheme = "https"
	}
	return scheme + "://" + fqdnHost
}

// operatorRecipients resolves who receives operator event email. AI.md
// PART 17 "Operator Notifications" routes everything to the operator, and
// PART 12's contact roles are where operator addresses live: the admin
// role first, falling back to the general role.
func operatorRecipients(cfg *config.Config) []string {
	for _, addr := range []string{cfg.Server.Contact.Admin.Email, cfg.Server.Contact.General.Email} {
		if addr = strings.TrimSpace(addr); addr != "" {
			return []string{addr}
		}
	}
	return nil
}

// startupNotifications runs the AI.md PART 17 startup SMTP check and
// reports the outcome.
//
// Per PART 17's "Display Note", the detected `{host}:{port}` IS shown on
// the console and in the logs even when it is a loopback address — that
// note is an explicit exemption from the usual never-display-localhost
// rule, because an operator has to be able to see which mail server was
// picked. When nothing works, email is simply off: nothing is queued, and
// no "would have sent" line is ever written.
func startupNotifications(binaryName string, n *notify.Notifier, cfg *config.Config, p paths.Paths, fqdnHost string, log *applog.Logger) {
	res := n.Startup(fqdnHost)

	switch {
	case res.Detected:
		line := fmt.Sprintf("email: SMTP detected at %s:%d (%s)", res.Host, res.Port, res.Source)
		fmt.Println(binaryName + ": " + line)
		writeLine(log, applog.LevelInfo, line)
		// Persist so the next start runs the cheap connection test against a
		// known server instead of re-probing every candidate.
		cfg.Server.Notifications.Email.SMTP.Host = res.Host
		cfg.Server.Notifications.Email.SMTP.Port = res.Port
		cfg.Server.Notifications.Email.Enabled = true
		if err := config.Save(p.ConfigFile, cfg); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": warning: "+err.Error())
		}
	case res.Err != nil:
		line := fmt.Sprintf("email: SMTP %s:%d is unreachable (%v) — email notifications disabled", res.Host, res.Port, res.Err)
		fmt.Fprintln(os.Stderr, binaryName+": warning: "+line)
		writeLine(log, applog.LevelWarn, line)
	case res.Enabled:
		line := fmt.Sprintf("email: SMTP %s:%d verified", res.Host, res.Port)
		writeLine(log, applog.LevelInfo, line)
	default:
		writeLine(log, applog.LevelInfo, "email: no SMTP server found — email notifications disabled")
	}

	if n.Enabled() {
		_ = n.Send(notify.EventStartup, nil)
	}
}

// writeLine appends one formatted line to log, if a log is open.
func writeLine(log *applog.Logger, level applog.Level, msg string) {
	if log == nil {
		return
	}
	_ = log.WriteLine(level, msg)
}
