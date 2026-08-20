package config

import (
	"testing"
)

func TestApplySMTPEnv(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantApplied []string
		check       func(*testing.T, EmailNotifications)
	}{
		{
			name:        "no environment leaves the config untouched",
			env:         nil,
			wantApplied: nil,
			check: func(t *testing.T, e EmailNotifications) {
				if e.SMTP.Host != "file.example.com" || e.SMTP.Port != 587 {
					t.Errorf("SMTP = %s:%d, want the config-file values", e.SMTP.Host, e.SMTP.Port)
				}
			},
		},
		{
			name: "every variable overrides its field",
			env: map[string]string{
				"SMTP_HOST":       "env.example.com",
				"SMTP_PORT":       "2525",
				"SMTP_USERNAME":   "user",
				"SMTP_PASSWORD":   "pass",
				"SMTP_TLS":        SMTPTLSStartTLS,
				"SMTP_FROM_NAME":  "Ops",
				"SMTP_FROM_EMAIL": "ops@example.net",
			},
			wantApplied: []string{
				"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD",
				"SMTP_TLS", "SMTP_FROM_NAME", "SMTP_FROM_EMAIL",
			},
			check: func(t *testing.T, e EmailNotifications) {
				if e.SMTP.Host != "env.example.com" || e.SMTP.Port != 2525 {
					t.Errorf("SMTP = %s:%d, want env.example.com:2525", e.SMTP.Host, e.SMTP.Port)
				}
				if e.SMTP.Username != "user" || e.SMTP.Password != "pass" {
					t.Errorf("credentials = %q/%q, want user/pass", e.SMTP.Username, e.SMTP.Password)
				}
				if e.SMTP.TLS != SMTPTLSStartTLS {
					t.Errorf("TLS = %q, want %q", e.SMTP.TLS, SMTPTLSStartTLS)
				}
				if e.From.Name != "Ops" || e.From.Email != "ops@example.net" {
					t.Errorf("From = %q <%q>, want Ops <ops@example.net>", e.From.Name, e.From.Email)
				}
			},
		},
		{
			name:        "an empty variable is treated as unset",
			env:         map[string]string{"SMTP_HOST": ""},
			wantApplied: nil,
			check: func(t *testing.T, e EmailNotifications) {
				if e.SMTP.Host != "file.example.com" {
					t.Errorf("Host = %q, want the config-file value", e.SMTP.Host)
				}
			},
		},
		{
			name:        "a non-numeric port is ignored",
			env:         map[string]string{"SMTP_PORT": "not-a-port"},
			wantApplied: nil,
			check: func(t *testing.T, e EmailNotifications) {
				if e.SMTP.Port != 587 {
					t.Errorf("Port = %d, want the config-file value 587", e.SMTP.Port)
				}
			},
		},
		{
			name:        "an out-of-range port is ignored",
			env:         map[string]string{"SMTP_PORT": "70000"},
			wantApplied: nil,
			check: func(t *testing.T, e EmailNotifications) {
				if e.SMTP.Port != 587 {
					t.Errorf("Port = %d, want the config-file value 587", e.SMTP.Port)
				}
			},
		},
		{
			name:        "a padded port still parses",
			env:         map[string]string{"SMTP_PORT": " 465 "},
			wantApplied: []string{"SMTP_PORT"},
			check: func(t *testing.T, e EmailNotifications) {
				if e.SMTP.Port != 465 {
					t.Errorf("Port = %d, want 465", e.SMTP.Port)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			cfg := &Config{}
			cfg.Server.Notifications = DefaultNotifications()
			cfg.Server.Notifications.Email.SMTP.Host = "file.example.com"

			applied := ApplySMTPEnv(cfg)
			if len(applied) != len(tc.wantApplied) {
				t.Fatalf("applied = %v, want %v", applied, tc.wantApplied)
			}
			for i := range applied {
				if applied[i] != tc.wantApplied[i] {
					t.Fatalf("applied = %v, want %v", applied, tc.wantApplied)
				}
			}
			tc.check(t, cfg.Server.Notifications.Email)
		})
	}
}

func TestValidateNotifications(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Notifications)
		wantWarn  bool
		wantCheck func(*testing.T, Notifications)
	}{
		{
			name:   "defaults validate cleanly",
			mutate: func(*Notifications) {},
		},
		{
			name:     "an unknown webui position falls back",
			mutate:   func(n *Notifications) { n.WebUI.Position = "middle" },
			wantWarn: true,
			wantCheck: func(t *testing.T, n Notifications) {
				if n.WebUI.Position != "top-right" {
					t.Errorf("position = %q, want the default", n.WebUI.Position)
				}
			},
		},
		{
			name:     "a negative toast duration falls back",
			mutate:   func(n *Notifications) { n.WebUI.Duration = -1 },
			wantWarn: true,
			wantCheck: func(t *testing.T, n Notifications) {
				if n.WebUI.Duration != 5 {
					t.Errorf("duration = %d, want the default 5", n.WebUI.Duration)
				}
			},
		},
		{
			name:     "an unknown TLS mode falls back to auto",
			mutate:   func(n *Notifications) { n.Email.SMTP.TLS = "ssl" },
			wantWarn: true,
			wantCheck: func(t *testing.T, n Notifications) {
				if n.Email.SMTP.TLS != SMTPTLSAuto {
					t.Errorf("tls = %q, want %q", n.Email.SMTP.TLS, SMTPTLSAuto)
				}
			},
		},
		{
			name:     "an impossible port falls back",
			mutate:   func(n *Notifications) { n.Email.SMTP.Port = 0 },
			wantWarn: true,
			wantCheck: func(t *testing.T, n Notifications) {
				if n.Email.SMTP.Port != 587 {
					t.Errorf("port = %d, want the default 587", n.Email.SMTP.Port)
				}
			},
		},
		{
			name:     "a From address that is not an address is dropped",
			mutate:   func(n *Notifications) { n.Email.From.Email = "not-an-address" },
			wantWarn: true,
			wantCheck: func(t *testing.T, n Notifications) {
				if n.Email.From.Email != "" {
					t.Errorf("from.email = %q, want it dropped", n.Email.From.Email)
				}
			},
		},
		{
			name:     "a Reply-To that is not an address is dropped",
			mutate:   func(n *Notifications) { n.Email.ReplyTo = "nope" },
			wantWarn: true,
			wantCheck: func(t *testing.T, n Notifications) {
				if n.Email.ReplyTo != "" {
					t.Errorf("reply_to = %q, want it dropped", n.Email.ReplyTo)
				}
			},
		},
		{
			name: "valid addresses survive",
			mutate: func(n *Notifications) {
				n.Email.From.Email = "ops@example.com"
				n.Email.ReplyTo = "reply@example.com"
			},
			wantCheck: func(t *testing.T, n Notifications) {
				if n.Email.From.Email != "ops@example.com" || n.Email.ReplyTo != "reply@example.com" {
					t.Errorf("addresses = %q/%q, want both preserved", n.Email.From.Email, n.Email.ReplyTo)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Server.Notifications = DefaultNotifications()
			tc.mutate(&cfg.Server.Notifications)

			defaults := &Config{}
			defaults.Server.Notifications = DefaultNotifications()

			warnings := validateNotifications(cfg, defaults)
			if got := len(warnings) > 0; got != tc.wantWarn {
				t.Fatalf("warnings = %v, want any = %v", warnings, tc.wantWarn)
			}
			if tc.wantCheck != nil {
				tc.wantCheck(t, cfg.Server.Notifications)
			}
		})
	}
}
