package httpserver

import (
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

type recordedSecurityEmail struct {
	to      []string
	subject string
	body    string
}

type recordingSecuritySender struct {
	enabled bool
	err     error
	emails  []recordedSecurityEmail
}

func (s *recordingSecuritySender) Enabled() bool { return s.enabled }

func (s *recordingSecuritySender) SendRaw(to []string, subject, body string) error {
	s.emails = append(s.emails, recordedSecurityEmail{
		to: append([]string(nil), to...), subject: subject, body: body,
	})
	return s.err
}

func TestNotifySecurityReportSendsOneMaintainerNoticeAndMinimalAcknowledgment(t *testing.T) {
	cfg := config.Default("")
	cfg.Server.Contact.Security.Email = "security@example.test"
	cfg.Server.Contact.General.Email = "contact@example.test"
	fd := &frontendDeps{
		cfg:       cfg,
		configDir: t.TempDir(),
		resolver:  NewProxyResolver(nil),
	}
	sender := &recordingSecuritySender{enabled: true, err: errors.New("smtp unavailable")}
	request := httptest.NewRequest("POST", "https://example.test/api/v1/server/contact", nil)
	form := securityReportForm{
		Name: "Researcher Name", Email: "researcher@example.test", Summary: "Private vulnerability summary",
		Steps: "Private reproduction steps", Impact: "Private impact", DisclosureDays: 90,
	}

	fd.notifySecurityReportWith(sender, request, "sec_0123456789abcdef", form)

	if len(sender.emails) != 2 {
		t.Fatalf("sent %d emails, want one maintainer notification and one acknowledgment", len(sender.emails))
	}
	if !reflect.DeepEqual(sender.emails[0].to, []string{"security@example.test"}) {
		t.Errorf("maintainer recipients = %v", sender.emails[0].to)
	}
	ack := sender.emails[1]
	if !reflect.DeepEqual(ack.to, []string{"researcher@example.test"}) {
		t.Errorf("acknowledgment recipients = %v", ack.to)
	}
	if !strings.Contains(ack.body, "Tracking ID: sec_0123456789abcdef\n") ||
		!strings.Contains(ack.body, "Status URL: https://example.test/server/security/report/sec_0123456789abcdef\n") {
		t.Errorf("acknowledgment lacks tracking ID or status URL: %q", ack.body)
	}
	for _, private := range []string{form.Name, form.Email, form.Summary, form.Steps, form.Impact} {
		if strings.Contains(ack.body, private) {
			t.Errorf("acknowledgment contains submitted report content %q", private)
		}
	}
	if strings.Count(ack.body, "\n") != 2 {
		t.Errorf("acknowledgment has extra content: %q", ack.body)
	}
}

func TestNotifySecurityReportSkipsAcknowledgmentWithoutResearcherEmail(t *testing.T) {
	cfg := config.Default("")
	cfg.Server.Contact.Security.Email = "security@example.test"
	fd := &frontendDeps{cfg: cfg, configDir: t.TempDir()}
	sender := &recordingSecuritySender{enabled: true}
	fd.notifySecurityReportWith(sender, httptest.NewRequest("POST", "https://example.test/", nil), "sec_0123456789abcdef", securityReportForm{})
	if len(sender.emails) != 1 {
		t.Fatalf("sent %d emails, want only the maintainer notification", len(sender.emails))
	}
}

func TestNotifySecurityReportDoesNotSendWhenEmailIsDisabled(t *testing.T) {
	fd := &frontendDeps{cfg: config.Default(""), configDir: t.TempDir()}
	sender := &recordingSecuritySender{}
	fd.notifySecurityReportWith(sender, httptest.NewRequest("POST", "https://example.test/", nil), "sec_0123456789abcdef", securityReportForm{
		Email: "researcher@example.test",
	})
	if len(sender.emails) != 0 {
		t.Fatalf("sent %d emails while notifications are disabled", len(sender.emails))
	}
}
