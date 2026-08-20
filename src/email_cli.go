// The `email` subcommand: AI.md PART 17 "Email Template Configuration"
// ("Use `{project_name} email test` to send a test email and verify
// configuration") plus the template management the same PART defines for
// the editor — list, preview with sample data, validate, and reset-to-
// default. The notification machinery itself lives in src/notify.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/fqdn"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/paths"
)

// emailHelp is the `email --help` text.
const emailHelp = `Email and notification management:

test [address]                        - Send a test email with sample data
                                        Defaults to server.notifications.email.reply_to
                                        Subject is prefixed [TEST]

list                                  - List every template and whether it
                                        is customized or using the default

preview <template>                    - Render a template with sample data

validate [template]                   - Validate custom templates
                                        No argument validates all of them

reset <template>                      - Delete the custom override and
                                        restore the embedded default

Examples:
  %[1]s email test ops@example.com
  %[1]s email preview backup_failed
  %[1]s email reset security_alert
`

// runEmail dispatches `email [COMMAND] [ARG]` and returns the process exit
// code: 0 on success, 1 on failure, 2 on a usage error.
func runEmail(binaryName string, p paths.Paths, command, arg string) int {
	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(emailHelp, binaryName)
		return 0
	case "test":
		return runEmailTest(binaryName, p, arg)
	case "list":
		return runEmailList(binaryName, p)
	case "preview":
		return runEmailPreview(binaryName, p, arg)
	case "validate":
		return runEmailValidate(binaryName, p, arg)
	case "reset":
		return runEmailReset(binaryName, p, arg)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown email command %q (run '%s email --help')\n", binaryName, command, binaryName)
		return 2
	}
}

// emailCLIContext loads the config and builds the notifier the email
// subcommands share.
func emailCLIContext(p paths.Paths) (*config.Config, *notify.Notifier, error) {
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return nil, nil, err
	}
	// AI.md PART 17 "Environment Variable Priority" applies to the CLI too:
	// an operator testing SMTP_HOST must be testing the server they set.
	config.ApplySMTPEnv(cfg)
	return cfg, newNotifier(cfg, p, fqdn.GetFQDN(internalName)), nil
}

// runEmailTest implements AI.md PART 17 "Send Test Email": it requires
// working SMTP, sends sample data with a `[TEST]` subject prefix, and
// writes an audit entry.
func runEmailTest(binaryName string, p paths.Paths, arg string) int {
	cfg, n, err := emailCLIContext(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}

	to := strings.TrimSpace(arg)
	if to == "" {
		to = strings.TrimSpace(cfg.Server.Notifications.Email.ReplyTo)
	}
	if to == "" {
		fmt.Fprintf(os.Stderr, "%s: no recipient (pass an address or set server.notifications.email.reply_to)\n", binaryName)
		return 2
	}

	// The startup probe is what turns "a host is configured" into "a host
	// answers"; without it a test send would fail on a stale config.
	n.Startup(fqdn.GetFQDN(internalName))
	if !n.Enabled() {
		fmt.Fprintf(os.Stderr, "%s: no working SMTP server — nothing was sent and nothing was queued\n", binaryName)
		return 1
	}

	subject, err := n.SendTest(to)
	result := applog.ResultSuccess
	if err != nil {
		result = applog.ResultFailure
	}
	auditEmailTest(p, to, subject, result, err)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("Sent to %s via %s\n", to, n.SMTPAddress())
	fmt.Printf("Subject: %s\n", subject)
	return 0
}

// auditEmailTest records the test send. A test email leaves the server and
// reaches a human, so AI.md PART 17 requires it in the audit log; the
// recipient is operator-supplied, never visitor PII.
func auditEmailTest(p paths.Paths, to, subject string, result applog.Result, sendErr error) {
	audit, err := applog.NewAuditLogger(filepath.Join(p.Logs, "audit.log"))
	if err != nil {
		return
	}
	defer audit.Close()

	details := map[string]any{"recipient": to, "subject": subject}
	if sendErr != nil {
		details["error"] = sendErr.Error()
	}
	_ = audit.Write(applog.Entry{
		Time:     time.Now().UTC(),
		Event:    "email.test_sent",
		Category: "config",
		Severity: applog.SeverityInfo,
		Details:  details,
		Result:   result,
	})
}

// runEmailList prints every template with its source, per AI.md PART 17
// "Template Storage" (custom wins, otherwise the embedded default).
func runEmailList(binaryName string, p paths.Paths) int {
	_, n, err := emailCLIContext(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	store := n.Store()
	for _, name := range notify.EventNames() {
		source := "default"
		if store.IsCustom(name) {
			source = "custom: " + store.CustomPath(name)
		}
		fmt.Printf("  %-22s %s\n", name, source)
	}
	return 0
}

// runEmailPreview renders a template with the sample data AI.md PART 17
// "Sample Data for Preview" defines.
func runEmailPreview(binaryName string, p paths.Paths, name string) int {
	if name == "" {
		fmt.Fprintf(os.Stderr, "%s: email preview needs a template name (run '%s email list')\n", binaryName, binaryName)
		return 2
	}
	_, n, err := emailCLIContext(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	subject, body, err := n.Preview(name, notify.SampleVariables(name))
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("Subject: %s\n\n%s\n", subject, body)
	return 0
}

// runEmailValidate runs AI.md PART 17 "Template Validation" over one or
// every template, reporting errors and non-blocking warnings.
func runEmailValidate(binaryName string, p paths.Paths, name string) int {
	_, n, err := emailCLIContext(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	names := notify.EventNames()
	if name != "" {
		names = []string{name}
	}

	failed := false
	for _, tmpl := range names {
		raw, _, err := n.Store().Raw(tmpl)
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			failed = true
			continue
		}
		v := notify.ValidateRaw(tmpl, raw)
		for _, msg := range v.Errors {
			fmt.Fprintf(os.Stderr, "  %-22s error: %s\n", tmpl, msg)
			failed = true
		}
		for _, msg := range v.Warnings {
			fmt.Printf("  %-22s warning: %s\n", tmpl, msg)
		}
		if v.OK() && len(v.Warnings) == 0 {
			fmt.Printf("  %-22s ok\n", tmpl)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// knownTemplate reports whether name is one of the AI.md PART 17 event
// templates. Reset takes a filesystem path from operator input, so the
// name is matched against the known set rather than merely sanitized.
func knownTemplate(name string) bool {
	for _, known := range notify.EventNames() {
		if known == name {
			return true
		}
	}
	return false
}

// runEmailReset deletes a custom override, per AI.md PART 17 "Reset to
// default → delete custom file".
func runEmailReset(binaryName string, p paths.Paths, name string) int {
	if name == "" {
		fmt.Fprintf(os.Stderr, "%s: email reset needs a template name (run '%s email list')\n", binaryName, binaryName)
		return 2
	}
	if !knownTemplate(name) {
		fmt.Fprintf(os.Stderr, "%s: unknown template %q (run '%s email list')\n", binaryName, name, binaryName)
		return 1
	}
	_, n, err := emailCLIContext(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if err := n.Store().Reset(name); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("%s now uses the embedded default\n", name)
	return 0
}
