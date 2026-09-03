package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/paths"
)

// emailPaths builds a throwaway Paths rooted in a temp dir so the email
// subcommands read and write only test-owned files.
func emailPaths(t *testing.T) paths.Paths {
	t.Helper()
	dir := t.TempDir()
	return paths.Paths{
		Config:     dir,
		ConfigFile: filepath.Join(dir, "server.yml"),
		Data:       dir,
		DB:         dir,
		Logs:       dir,
	}
}

func TestRunEmailDispatch(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		arg      string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{
			name:     "no command prints help",
			wantCode: 0,
			wantOut:  "Email and notification management:",
		},
		{
			name:     "--help prints help",
			command:  "--help",
			wantCode: 0,
			wantOut:  "reset <template>",
		},
		{
			name:     "unknown command is a usage error",
			command:  "nope",
			wantCode: 2,
			wantErr:  `unknown email command "nope"`,
		},
		{
			name:     "preview without a name is a usage error",
			command:  "preview",
			wantCode: 2,
			wantErr:  "email preview needs a template name",
		},
		{
			name:     "reset without a name is a usage error",
			command:  "reset",
			wantCode: 2,
			wantErr:  "email reset needs a template name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := emailPaths(t)
			out, errOut, code := captureOutput(t, func() int {
				return runEmail("shortner", p, tc.command, tc.arg)
			})
			if code != tc.wantCode {
				t.Fatalf("code = %d, want %d (out %q err %q)", code, tc.wantCode, out, errOut)
			}
			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", out, tc.wantOut)
			}
			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}
		})
	}
}

func TestRunEmailTestWithoutRecipient(t *testing.T) {
	// With no argument and no reply_to configured there is nobody to send
	// to, so the command must fail before it ever touches SMTP.
	p := emailPaths(t)
	_, errOut, code := captureOutput(t, func() int { return runEmail("shortner", p, "test", "") })
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "no recipient") {
		t.Errorf("stderr = %q, want it to mention the missing recipient", errOut)
	}
}

func TestRunEmailList(t *testing.T) {
	p := emailPaths(t)

	out, _, code := captureOutput(t, func() int { return runEmail("shortner", p, "list", "") })
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	for _, name := range notify.EventNames() {
		if !strings.Contains(out, name) {
			t.Errorf("list output is missing template %q:\n%s", name, out)
		}
	}
	if strings.Contains(out, "custom:") {
		t.Errorf("a fresh config dir must report every template as default:\n%s", out)
	}

	// Once an override exists, the same listing must say so.
	if _, err := notify.NewStore(p.Config).Save(notify.EventTest, "Subject: mine\n---\nbody\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, _, code = captureOutput(t, func() int { return runEmail("shortner", p, "list", "") })
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out, "custom:") {
		t.Errorf("list output does not report the override:\n%s", out)
	}
}

func TestRunEmailPreviewRendersSampleData(t *testing.T) {
	p := emailPaths(t)

	for _, name := range notify.EventNames() {
		t.Run(name, func(t *testing.T) {
			out, errOut, code := captureOutput(t, func() int { return runEmail("shortner", p, "preview", name) })
			if code != 0 {
				t.Fatalf("code = %d (err %q), want 0", code, errOut)
			}
			if !strings.HasPrefix(out, "Subject: ") {
				t.Errorf("preview output does not start with a subject line:\n%s", out)
			}
			// AI.md PART 17 "Template Preview" renders sample data, so no
			// placeholder may survive into the preview.
			if strings.Contains(out, "{") {
				t.Errorf("preview left an unresolved placeholder:\n%s", out)
			}
		})
	}
}

func TestRunEmailPreviewUnknownTemplate(t *testing.T) {
	p := emailPaths(t)
	_, errOut, code := captureOutput(t, func() int { return runEmail("shortner", p, "preview", "not_a_template") })
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if errOut == "" {
		t.Error("preview of an unknown template printed no error")
	}
}

func TestRunEmailValidate(t *testing.T) {
	tests := []struct {
		name     string
		arg      string
		override string
		wantCode int
		wantErr  string
	}{
		{
			name:     "every embedded default validates",
			wantCode: 0,
		},
		{
			name:     "a single named template validates",
			arg:      notify.EventTest,
			wantCode: 0,
		},
		{
			name:     "a broken override is reported",
			arg:      notify.EventTest,
			override: "no separator here",
			wantCode: 1,
			wantErr:  "error:",
		},
		{
			name:     "an unknown variable in an override is reported",
			arg:      notify.EventTest,
			override: "Subject: s\n---\n{task_name}\n",
			wantCode: 1,
			wantErr:  "Unknown variable",
		},
		{
			name:     "an unknown template name is reported",
			arg:      "not_a_template",
			wantCode: 1,
			wantErr:  "not_a_template",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := emailPaths(t)
			if tc.override != "" {
				writeOverride(t, p, notify.EventTest, tc.override)
			}
			out, errOut, code := captureOutput(t, func() int { return runEmail("shortner", p, "validate", tc.arg) })
			if code != tc.wantCode {
				t.Fatalf("code = %d, want %d (out %q err %q)", code, tc.wantCode, out, errOut)
			}
			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}
		})
	}
}

func TestRunEmailReset(t *testing.T) {
	p := emailPaths(t)
	store := notify.NewStore(p.Config)
	if _, err := store.Save(notify.EventTest, "Subject: mine\n---\nbody\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, errOut, code := captureOutput(t, func() int { return runEmail("shortner", p, "reset", notify.EventTest) })
	if code != 0 {
		t.Fatalf("code = %d (err %q), want 0", code, errOut)
	}
	if !strings.Contains(out, "embedded default") {
		t.Errorf("stdout = %q, want the reset confirmation", out)
	}
	if store.IsCustom(notify.EventTest) {
		t.Error("the override survived the reset")
	}

	// AI.md PART 17: resetting a template that was never customized is a
	// no-op, not a failure.
	if _, _, code := captureOutput(t, func() int { return runEmail("shortner", p, "reset", notify.EventTest) }); code != 0 {
		t.Errorf("second reset code = %d, want 0", code)
	}
}

func TestRunEmailResetRejectsUnsafeName(t *testing.T) {
	p := emailPaths(t)
	_, errOut, code := captureOutput(t, func() int { return runEmail("shortner", p, "reset", "../escape") })
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if errOut == "" {
		t.Error("a path-traversal template name printed no error")
	}
}

func TestEmailCLIContextAppliesSMTPEnv(t *testing.T) {
	// AI.md PART 17 "Environment Variable Priority" applies to the CLI, not
	// just the server.
	t.Setenv("SMTP_HOST", "env.example.com")
	t.Setenv("SMTP_PORT", "2525")

	p := emailPaths(t)
	cfg, n, err := emailCLIContext(p)
	if err != nil {
		t.Fatalf("emailCLIContext: %v", err)
	}
	if got := cfg.Server.Notifications.Email.SMTP.Host; got != "env.example.com" {
		t.Errorf("host = %q, want the environment override", got)
	}
	if got := cfg.Server.Notifications.Email.SMTP.Port; got != 2525 {
		t.Errorf("port = %d, want 2525", got)
	}
	if n == nil {
		t.Fatal("emailCLIContext returned no notifier")
	}
}

// writeOverride drops a raw custom template into the config dir, bypassing
// Store.Save's validation the way a hand-editing operator would.
func writeOverride(t *testing.T, p paths.Paths, name, raw string) {
	t.Helper()
	dir := filepath.Join(p.Config, "template", "email")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".tmpl"), []byte(raw), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestAuditEmailTestWritesAnEntry(t *testing.T) {
	p := emailPaths(t)
	auditEmailTest(p, "ops@example.com", "[TEST] hello", applog.ResultSuccess, nil)

	raw, err := os.ReadFile(filepath.Join(p.Logs, "audit.log"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	data := string(raw)
	for _, want := range []string{"email.test_sent", "ops@example.com", "[TEST] hello"} {
		if !strings.Contains(data, want) {
			t.Errorf("audit log missing %q:\n%s", want, data)
		}
	}
}

func TestOperatorRecipients(t *testing.T) {
	tests := []struct {
		name    string
		admin   string
		general string
		want    []string
	}{
		{name: "no contacts means no recipients"},
		{name: "admin wins", admin: "admin@example.com", general: "info@example.com", want: []string{"admin@example.com"}},
		{name: "general is the fallback", general: "info@example.com", want: []string{"info@example.com"}},
		{name: "whitespace-only is not an address", admin: "   ", general: "info@example.com", want: []string{"info@example.com"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default("")
			cfg.Server.Contact.Admin.Email = tc.admin
			cfg.Server.Contact.General.Email = tc.general

			got := operatorRecipients(cfg)
			if len(got) != len(tc.want) {
				t.Fatalf("operatorRecipients() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("operatorRecipients() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestAppName(t *testing.T) {
	cfg := config.Default("")
	if got := appName(cfg); got != internalName {
		t.Errorf("appName() = %q, want the compiled-in project name %q", got, internalName)
	}
	cfg.Server.Branding.SiteName = "  My Links  "
	if got := appName(cfg); got != "My Links" {
		t.Errorf("appName() = %q, want the trimmed site name", got)
	}
}

func TestAppURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		tls     bool
		fqdn    string
		want    string
	}{
		{name: "configured base URL wins", baseURL: "https://links.example.com", fqdn: "host.example.com", want: "https://links.example.com"},
		{name: "no base URL falls back to http", fqdn: "host.example.com", want: "http://host.example.com"},
		{name: "TLS makes the fallback https", tls: true, fqdn: "host.example.com", want: "https://host.example.com"},
		{name: "a non-URL base is ignored", baseURL: "links.example.com", fqdn: "host.example.com", want: "http://host.example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default("")
			cfg.Server.BaseURL = tc.baseURL
			cfg.Server.TLS.Enabled = tc.tls
			if got := appURL(cfg, tc.fqdn); got != tc.want {
				t.Errorf("appURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
