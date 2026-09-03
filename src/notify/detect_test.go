package notify

import (
	"errors"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

func TestDetectCandidates(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		wantFirst   Candidate
		wantHosts   []string
		wantAbsent  []string
		wantMinimum int
	}{
		{
			name:        "loopback is always tried first, on port 25",
			host:        "example.com",
			wantFirst:   Candidate{Host: "127.0.0.1", Port: 25, Source: "Loopback (same machine)"},
			wantHosts:   []string{"127.0.0.1", "172.17.0.1", "example.com", "mail.example.com", "smtp.example.com"},
			wantMinimum: 5 * len(detectPorts),
		},
		{
			name:        "no fqdn means no fqdn-derived candidates",
			host:        "",
			wantFirst:   Candidate{Host: "127.0.0.1", Port: 25, Source: "Loopback (same machine)"},
			wantAbsent:  []string{"mail.", "smtp."},
			wantMinimum: 2 * len(detectPorts),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectCandidates(tc.host)
			if len(got) < tc.wantMinimum {
				t.Fatalf("got %d candidates, want at least %d", len(got), tc.wantMinimum)
			}
			if got[0] != tc.wantFirst {
				t.Errorf("first candidate = %+v, want %+v", got[0], tc.wantFirst)
			}

			hosts := map[string]int{}
			for _, c := range got {
				hosts[c.Host]++
			}
			for _, want := range tc.wantHosts {
				if hosts[want] != len(detectPorts) {
					t.Errorf("host %q appeared %d times, want %d (one per port)", want, hosts[want], len(detectPorts))
				}
			}
			for _, bad := range tc.wantAbsent {
				for h := range hosts {
					if len(h) >= len(bad) && h[:len(bad)] == bad {
						t.Errorf("candidate %q must not be present when there is no FQDN", h)
					}
				}
			}
		})
	}
}

func TestDetectCandidatesDeduplicates(t *testing.T) {
	// A machine whose FQDN resolves to the loopback address must not probe
	// 127.0.0.1 twice.
	got := DetectCandidates("127.0.0.1")
	seen := map[string]bool{}
	for _, c := range got {
		key := c.Host + ":" + string(rune(c.Port))
		if seen[key] {
			t.Fatalf("duplicate candidate %+v", c)
		}
		seen[key] = true
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name     string
		accept   func(config.SMTP) bool
		wantOK   bool
		wantHost string
		wantPort int
	}{
		{
			name:     "first working candidate wins",
			accept:   func(config.SMTP) bool { return true },
			wantOK:   true,
			wantHost: "127.0.0.1",
			wantPort: 25,
		},
		{
			name:     "falls through to a later candidate",
			accept:   func(c config.SMTP) bool { return c.Host == "172.17.0.1" && c.Port == 587 },
			wantOK:   true,
			wantHost: "172.17.0.1",
			wantPort: 587,
		},
		{
			name:   "nothing reachable is not an error",
			accept: func(config.SMTP) bool { return false },
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prev := probe
			probe = func(cfg config.SMTP) error {
				if tc.accept(cfg) {
					return nil
				}
				return errors.New("refused")
			}
			defer func() { probe = prev }()

			got, ok := Detect("example.com", config.SMTP{})
			if ok != tc.wantOK {
				t.Fatalf("Detect() ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got.Host != tc.wantHost || got.Port != tc.wantPort {
				t.Errorf("Detect() = %s:%d, want %s:%d", got.Host, got.Port, tc.wantHost, tc.wantPort)
			}
			if got.Source == "" {
				t.Error("Detect() returned no priority label")
			}
		})
	}
}

func TestDetectCarriesCredentialsAndDefaultsTLS(t *testing.T) {
	var seen config.SMTP
	prev := probe
	probe = func(cfg config.SMTP) error {
		seen = cfg
		return nil
	}
	defer func() { probe = prev }()

	if _, ok := Detect("example.com", config.SMTP{Username: "u", Password: "p"}); !ok {
		t.Fatal("Detect() failed with an accepting probe")
	}
	if seen.Username != "u" || seen.Password != "p" {
		t.Errorf("probe saw %q/%q, want the configured credentials", seen.Username, seen.Password)
	}
	if seen.TLS != config.SMTPTLSAuto {
		t.Errorf("probe TLS = %q, want %q", seen.TLS, config.SMTPTLSAuto)
	}
}

func TestNotifierStartupAutoDetects(t *testing.T) {
	prev := probe
	probe = func(cfg config.SMTP) error {
		if cfg.Host == "127.0.0.1" && cfg.Port == 25 {
			return nil
		}
		return errors.New("refused")
	}
	defer func() { probe = prev }()

	n := New(Options{Email: config.EmailNotifications{}})
	res := n.Startup("example.com")

	if !res.Enabled || !res.Detected {
		t.Fatalf("Startup() = %+v, want an enabled detection", res)
	}
	if res.Host != "127.0.0.1" || res.Port != 25 {
		t.Errorf("Startup() detected %s:%d, want 127.0.0.1:25", res.Host, res.Port)
	}
	if !n.Enabled() {
		t.Error("Enabled() = false after a successful detection")
	}
	if got := n.SMTPAddress(); got != "127.0.0.1:25" {
		t.Errorf("SMTPAddress() = %q, want 127.0.0.1:25", got)
	}
}

func TestNotifierStartupNoSMTPStaysDisabled(t *testing.T) {
	prev := probe
	probe = func(config.SMTP) error { return errors.New("refused") }
	defer func() { probe = prev }()

	n := New(Options{Email: config.EmailNotifications{Enabled: true}})
	res := n.Startup("example.com")

	// AI.md PART 17: "If all fail → email features disabled (not an error)".
	if res.Enabled || res.Err != nil {
		t.Fatalf("Startup() = %+v, want a quiet disable", res)
	}
	if n.Enabled() {
		t.Error("Enabled() = true after a failed detection")
	}
}

func TestNotifierStartupConnectionTest(t *testing.T) {
	tests := []struct {
		name        string
		probeErr    error
		wantEnabled bool
	}{
		{name: "configured host that answers enables email", wantEnabled: true},
		{name: "configured host that fails disables email and reports why", probeErr: errors.New("connection refused")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prev := probe
			probe = func(config.SMTP) error { return tc.probeErr }
			defer func() { probe = prev }()

			n := New(Options{Email: config.EmailNotifications{
				SMTP: config.SMTP{Host: "mail.example.com", Port: 587},
			}})
			res := n.Startup("example.com")

			if res.Enabled != tc.wantEnabled {
				t.Fatalf("Startup() = %+v, want Enabled=%v", res, tc.wantEnabled)
			}
			if res.Detected {
				t.Error("Detected = true for a configured host")
			}
			if res.Host != "mail.example.com" || res.Port != 587 {
				t.Errorf("Startup() reported %s:%d, want the configured server", res.Host, res.Port)
			}
			if (res.Err != nil) != (tc.probeErr != nil) {
				t.Errorf("Err = %v, want %v", res.Err, tc.probeErr)
			}
			if n.Enabled() != tc.wantEnabled {
				t.Errorf("Enabled() = %v, want %v", n.Enabled(), tc.wantEnabled)
			}
		})
	}
}
