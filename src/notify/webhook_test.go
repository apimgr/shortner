package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

func TestWebhookGenericPayloadIsSignedAndTimestamped(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sender := newWebhookSender(config.Contact{}, "shortner", "Shortner", "1.2.3", "https://shortner.example", nil)
	sender.client = server.Client()
	payload := webhookPayload{
		Role: "admin", Event: "backup.failed", Subject: "Backup failed", Body: "summary",
		Severity: "critical", Timestamp: time.Date(2026, 11, 24, 9, 30, 0, 0, time.UTC).Format(time.RFC3339),
		ProjectName: "shortner", ProjectVersion: "1.2.3", AppURL: "https://shortner.example", TrackingID: "trk_123",
	}
	message := webhookMessage{payload: payload, secret: "test-signing-secret", id: "019b0000-0000-7000-8000-000000000001"}
	if err := sender.sendOnce("generic", server.URL, message); err != nil {
		t.Fatal(err)
	}
	var decoded webhookPayload
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != payload {
		t.Fatalf("payload = %+v, want %+v", decoded, payload)
	}
	mac := hmac.New(sha256.New, []byte(message.secret))
	_, _ = mac.Write(gotBody)
	if got := gotHeaders.Get("X-Webhook-Signature"); got != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatalf("signature = %q", got)
	}
	if gotHeaders.Get("X-Webhook-Timestamp") != "1795512600" || gotHeaders.Get("X-Webhook-ID") != message.id || gotHeaders.Get("X-Webhook-Event") != payload.Event {
		t.Fatalf("webhook headers = timestamp:%q id:%q event:%q", gotHeaders.Get("X-Webhook-Timestamp"), gotHeaders.Get("X-Webhook-ID"), gotHeaders.Get("X-Webhook-Event"))
	}
}

func TestWebhookTransportPayloads(t *testing.T) {
	tests := []struct {
		transport, endpoint, wantType, wantBody string
	}{
		{"discord", "https://example.test/hook", "application/json", `"username":"Shortner"`},
		{"slack", "https://example.test/hook", "application/json", `"text":"subject`},
		{"mattermost", "https://example.test/hook", "application/json", `"text":"subject`},
		{"pushover", "https://example.test/1/messages.json?user=u&token=t", "application/x-www-form-urlencoded", "priority=1"},
		{"gotify", "https://example.test?token=t", "application/json", `"priority":8`},
	}
	payload := webhookPayload{Subject: "subject", Body: "body", AppName: "Shortner", ProjectName: "shortner", Severity: "critical"}
	for _, tt := range tests {
		t.Run(tt.transport, func(t *testing.T) {
			endpoint, err := url.Parse(tt.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			body, contentType, err := webhookRequest(tt.transport, endpoint, payload)
			if err != nil {
				t.Fatal(err)
			}
			if contentType != tt.wantType || !strings.Contains(string(body), tt.wantBody) {
				t.Fatalf("content type/body = %q %q", contentType, body)
			}
			if tt.transport == "gotify" && (endpoint.Path != "/message" || endpoint.Query().Get("token") != "t") {
				t.Fatalf("gotify URL = %s", endpoint)
			}
		})
	}
}

func TestWebhookRoleFallback(t *testing.T) {
	contact := config.Contact{
		Admin:   config.ContactRecipient{Webhooks: map[string]string{"discord": "https://admin.test/hook", "discord_secret": "admin-secret", "generic": "https://admin.test/generic", "generic_secret": "admin-generic"}},
		General: config.ContactRecipient{Webhooks: map[string]string{"discord": "https://general.test/hook", "discord_secret": "general-secret"}},
		Abuse:   config.ContactRecipient{Webhooks: map[string]string{"slack": "https://abuse.test/hook", "slack_secret": "abuse-secret"}},
	}
	sender := newWebhookSender(contact, "shortner", "Shortner", "1.0.0", "https://shortner.example", nil)
	urls, secrets := sender.resolve("abuse")
	if urls["slack"] != "https://abuse.test/hook" || urls["discord"] != "https://general.test/hook" || urls["generic"] != "https://admin.test/generic" {
		t.Fatalf("fallback URLs = %#v", urls)
	}
	if secrets["discord"] != "general-secret" || secrets["generic"] != "admin-generic" {
		t.Fatalf("fallback secrets = %#v", secrets)
	}
}

func TestWebhookDeliveryDoesNotFollowRedirects(t *testing.T) {
	destinationCalled := false
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	sender := newWebhookSender(config.Contact{}, "shortner", "Shortner", "1.0.0", "https://shortner.example", nil)
	sender.client = redirect.Client()
	err := sender.sendOnce("generic", redirect.URL, webhookMessage{
		payload: webhookPayload{Timestamp: time.Now().UTC().Format(time.RFC3339)},
		secret:  "signing-secret",
		id:      "019b0000-0000-7000-8000-000000000001",
	})
	if err == nil {
		t.Fatal("redirect response was accepted")
	}
	if destinationCalled {
		t.Fatal("webhook request followed a redirect to another host")
	}
}

func TestWebhookFailureLogsAreRateLimitedPerRoleAndTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.log")
	logger, err := applog.Open(path, applog.LevelDebug)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})
	sender := newWebhookSender(config.Contact{}, "shortner", "Shortner", "1.0.0", "https://shortner.example", logger)
	sender.logFailure("admin", "generic", "backup.failed")
	sender.logFailure("admin", "generic", "backup.failed")
	sender.logFailure("security", "generic", "security.report_received")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "notify.webhook_failed"); got != 2 {
		t.Fatalf("webhook failure log count = %d, want one per role/transport: %s", got, data)
	}
}

func TestNotifierDispatchesAdminWebhookWithoutSMTP(t *testing.T) {
	delivered := make(chan webhookPayload, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook: %v", err)
		}
		delivered <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	contact := config.Contact{Admin: config.ContactRecipient{Webhooks: map[string]string{
		"generic": server.URL, "generic_secret": "webhook-secret",
	}}}
	notifier := New(Options{
		Email: config.DefaultNotifications().Email, Contact: contact,
		AppName: "Shortner", AppURL: "https://shortner.example", FQDN: "shortner.example",
		ProjectName: "shortner", Version: "1.0.0",
	})
	notifier.webhooks.client = server.Client()
	if err := notifier.Send(EventBackupFailed, map[string]string{"filename": "backup.tar.gz", "error": "disk full"}); err != ErrDisabled {
		t.Fatalf("Send() = %v, want ErrDisabled when SMTP is unavailable", err)
	}
	select {
	case payload := <-delivered:
		if payload.Role != "admin" || payload.Event != "backup.failed" || payload.Severity != "critical" || !strings.Contains(payload.Body, "disk full") {
			t.Fatalf("webhook payload = %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("admin webhook was not delivered without SMTP")
	}
}
