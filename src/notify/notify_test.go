package notify

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

// sentRecorder captures the messages a test's sends produce instead of
// dialing a real SMTP server.
type sentRecorder struct {
	mu  sync.Mutex
	all []Message
	err error
}

func (s *sentRecorder) install(t *testing.T) {
	t.Helper()
	prev := sendMail
	sendMail = func(_ config.SMTP, msg Message) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.all = append(s.all, msg)
		return s.err
	}
	t.Cleanup(func() { sendMail = prev })
}

func (s *sentRecorder) messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.all...)
}

// readyNotifier returns a Notifier that believes SMTP works, without ever
// touching the network.
func readyNotifier(t *testing.T, recipients ...string) *Notifier {
	t.Helper()
	n := New(Options{
		Email: config.EmailNotifications{
			Enabled: true,
			SMTP:    config.SMTP{Host: "127.0.0.1", Port: 25},
			ReplyTo: "ops@example.com",
		},
		AppName:    "shortner",
		AppURL:     "https://example.com",
		FQDN:       "example.com",
		Recipients: recipients,
	})
	n.ready = true
	return n
}

func TestNilNotifierIsInert(t *testing.T) {
	var n *Notifier

	if n.Enabled() {
		t.Error("nil Notifier reports Enabled")
	}
	if got := n.SMTPAddress(); got != "" {
		t.Errorf("SMTPAddress() = %q, want empty", got)
	}
	if n.Store() == nil {
		t.Error("Store() on a nil Notifier must return a usable zero Store")
	}
	if err := n.Send(EventTest, nil); !errors.Is(err, ErrDisabled) {
		t.Errorf("Send() = %v, want ErrDisabled", err)
	}
	if err := n.SendRaw([]string{"a@example.com"}, "s", "b"); !errors.Is(err, ErrDisabled) {
		t.Errorf("SendRaw() = %v, want ErrDisabled", err)
	}
	if _, err := n.SendTest("a@example.com"); !errors.Is(err, ErrDisabled) {
		t.Errorf("SendTest() = %v, want ErrDisabled", err)
	}
	if got := n.Startup("example.com"); got.Enabled {
		t.Errorf("Startup() = %+v, want disabled", got)
	}
}

func TestSendGates(t *testing.T) {
	tests := []struct {
		name       string
		ready      bool
		enabled    bool
		host       string
		recipients []string
		events     config.EmailEvents
		event      string
		wantSent   bool
	}{
		{
			name: "no SMTP host means nothing is ever sent",
			// AI.md PART 17 "SMTP Requirement": no queue, no retry.
			ready: true, enabled: true, host: "",
			recipients: []string{"ops@example.com"}, event: EventTest,
		},
		{
			name:  "disabled email sends nothing",
			ready: true, enabled: false, host: "127.0.0.1",
			recipients: []string{"ops@example.com"}, event: EventTest,
		},
		{
			name:  "no recipients sends nothing",
			ready: true, enabled: true, host: "127.0.0.1",
			recipients: nil, event: EventTest,
		},
		{
			name:  "a disabled event sends nothing",
			ready: true, enabled: true, host: "127.0.0.1",
			recipients: []string{"ops@example.com"},
			events:     config.EmailEvents{SecurityAlert: false},
			event:      EventSecurityAlert,
		},
		{
			name:  "an enabled event sends",
			ready: true, enabled: true, host: "127.0.0.1",
			recipients: []string{"ops@example.com"},
			events:     config.EmailEvents{SecurityAlert: true},
			event:      EventSecurityAlert,
			wantSent:   true,
		},
		{
			name:  "the test event is never gated by the switch",
			ready: true, enabled: true, host: "127.0.0.1",
			recipients: []string{"ops@example.com"},
			event:      EventTest,
			wantSent:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &sentRecorder{}
			rec.install(t)

			n := New(Options{
				Email: config.EmailNotifications{
					Enabled: tc.enabled,
					SMTP:    config.SMTP{Host: tc.host, Port: 25},
					Events:  tc.events,
				},
				AppName:    "shortner",
				FQDN:       "example.com",
				Recipients: tc.recipients,
			})
			n.ready = tc.ready

			err := n.Send(tc.event, map[string]string{"event": "x", "details": "y"})
			got := len(rec.messages()) == 1
			if got != tc.wantSent {
				t.Fatalf("sent = %v (err %v), want %v", got, err, tc.wantSent)
			}
			if !tc.wantSent && !errors.Is(err, ErrDisabled) {
				t.Errorf("err = %v, want ErrDisabled", err)
			}
		})
	}
}

func TestSendTestPrefixesSubject(t *testing.T) {
	rec := &sentRecorder{}
	rec.install(t)

	n := readyNotifier(t, "ops@example.com")
	subject, err := n.SendTest("someone@example.com")
	if err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if !strings.HasPrefix(subject, "[TEST] ") {
		t.Errorf("subject = %q, want a [TEST] prefix", subject)
	}
	msgs := rec.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	if msgs[0].To[0] != "someone@example.com" {
		t.Errorf("To = %v, want the explicit recipient", msgs[0].To)
	}
	if msgs[0].Subject != subject {
		t.Errorf("delivered subject = %q, want %q", msgs[0].Subject, subject)
	}
}

func TestSendRawUsesConfiguredSender(t *testing.T) {
	tests := []struct {
		name      string
		fromName  string
		fromEmail string
		appName   string
		fqdn      string
		wantName  string
		wantEmail string
	}{
		{
			name:    "falls back to the app name and no-reply@fqdn",
			appName: "shortner", fqdn: "example.com",
			wantName: "shortner", wantEmail: "no-reply@example.com",
		},
		{
			name:     "configured values win",
			fromName: "Ops", fromEmail: "ops@example.net",
			appName: "shortner", fqdn: "example.com",
			wantName: "Ops", wantEmail: "ops@example.net",
		},
		{
			name:     "no fqdn falls back to localhost",
			appName:  "shortner",
			wantName: "shortner", wantEmail: "no-reply@localhost",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &sentRecorder{}
			rec.install(t)

			n := New(Options{
				Email: config.EmailNotifications{
					Enabled: true,
					SMTP:    config.SMTP{Host: "127.0.0.1", Port: 25},
					From:    config.EmailFrom{Name: tc.fromName, Email: tc.fromEmail},
				},
				AppName: tc.appName,
				FQDN:    tc.fqdn,
			})
			n.ready = true

			if err := n.SendRaw([]string{"a@example.com"}, "s", "b"); err != nil {
				t.Fatalf("SendRaw: %v", err)
			}
			msgs := rec.messages()
			if len(msgs) != 1 {
				t.Fatalf("sent %d messages, want 1", len(msgs))
			}
			if msgs[0].FromName != tc.wantName {
				t.Errorf("FromName = %q, want %q", msgs[0].FromName, tc.wantName)
			}
			if msgs[0].FromEmail != tc.wantEmail {
				t.Errorf("FromEmail = %q, want %q", msgs[0].FromEmail, tc.wantEmail)
			}
		})
	}
}

func TestSendRawPropagatesFailure(t *testing.T) {
	rec := &sentRecorder{err: errors.New("connection refused")}
	rec.install(t)

	n := readyNotifier(t, "ops@example.com")
	if err := n.SendRaw([]string{"a@example.com"}, "s", "b"); err == nil {
		t.Fatal("SendRaw() = nil, want the transport error")
	}
}

func TestPreviewWorksWithEmailDisabled(t *testing.T) {
	// Preview never touches SMTP (AI.md PART 17 "Template Preview"), so it
	// must work on a server with no mail server at all.
	n := New(Options{AppName: "shortner", FQDN: "example.com"})

	for _, event := range EventNames() {
		t.Run(event, func(t *testing.T) {
			subject, body, err := n.Preview(event, nil)
			if err != nil {
				t.Fatalf("Preview(%s): %v", event, err)
			}
			if subject == "" || body == "" {
				t.Fatalf("Preview(%s) = %q / %q, want both non-empty", event, subject, body)
			}
			if strings.Contains(subject+body, "{") {
				t.Errorf("Preview(%s) left an unresolved placeholder:\n%s\n%s", event, subject, body)
			}
		})
	}
}

func TestVariables(t *testing.T) {
	n := New(Options{
		AppName:      "shortner",
		AppURL:       "https://example.com",
		FQDN:         "example.com",
		OnionAddress: "abc.onion",
		Version:      "1.2.3",
		Mode:         "production",
		Email:        config.EmailNotifications{ReplyTo: "ops@example.com"},
	})

	got := n.variables(map[string]string{"fqdn": "cert.example.net"})

	tests := []struct{ key, want string }{
		{"app_name", "shortner"},
		{"app_url", "https://example.com"},
		// Caller values win over the server's own.
		{"fqdn", "cert.example.net"},
		{"onion_address", "abc.onion"},
		// AI.md PART 12/31: overlay addresses are ALWAYS http://.
		{"onion_url", "http://abc.onion"},
		{"i2p_address", ""},
		{"i2p_url", ""},
		{"notification_reply_to", "ops@example.com"},
		{"version", "1.2.3"},
		{"mode", "production"},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			if got[tc.key] != tc.want {
				t.Errorf("%s = %q, want %q", tc.key, got[tc.key], tc.want)
			}
		})
	}
	if got["year"] == "" || got["timestamp"] == "" {
		t.Error("year/timestamp must always be populated")
	}
}

func TestSampleVariables(t *testing.T) {
	tests := []struct {
		event string
		keys  []string
	}{
		{EventSecurityAlert, []string{"event", "ip", "details"}},
		{EventBackupFailed, []string{"filename", "size", "error"}},
		{EventSchedulerError, []string{"task_name", "error", "next_run"}},
		{EventTest, []string{"ip"}},
	}

	for _, tc := range tests {
		t.Run(tc.event, func(t *testing.T) {
			got := SampleVariables(tc.event)
			for _, k := range tc.keys {
				if got[k] == "" {
					t.Errorf("SampleVariables(%s)[%q] is empty", tc.event, k)
				}
			}
		})
	}
}

func TestOverlayURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"abc.onion", "http://abc.onion"},
		{"abc.b32.i2p", "http://abc.b32.i2p"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := overlayURL(tc.in); got != tc.want {
				t.Errorf("overlayURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
