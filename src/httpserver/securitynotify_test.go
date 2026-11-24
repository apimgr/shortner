package httpserver

import (
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/notify"
)

type recordedSecurityEmail struct {
	to          []string
	subject     string
	body        string
	attachments []notify.Attachment
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

func (s *recordingSecuritySender) SendRawWithAttachments(to []string, subject, body string, attachments []notify.Attachment) error {
	s.emails = append(s.emails, recordedSecurityEmail{
		to: append([]string(nil), to...), subject: subject, body: body, attachments: attachments,
	})
	return s.err
}

func TestNotifySecurityReportSendsOneMaintainerNoticeAndMinimalAcknowledgment(t *testing.T) {
	cfg := config.Default("")
	cfg.Server.Contact.Security.Email = "security@example.test"
	cfg.Server.Contact.General.Email = "contact@example.test"
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	fd := &frontendDeps{
		cfg:       cfg,
		configDir: t.TempDir(),
		resolver:  NewProxyResolver(nil),
	}
	sender := &recordingSecuritySender{enabled: true}
	request := httptest.NewRequest("POST", "https://example.test/api/v1/server/contact", nil)
	form := securityReportForm{
		Name: "Researcher Name", Email: "researcher@example.test", Summary: "Private vulnerability summary",
		Steps: "Private reproduction steps", Impact: "Private impact", DisclosureDays: 90,
	}

	statusToken := "f1a4d882"
	acknowledged, notified := fd.notifySecurityReportWith(sender, request, "sec_0123456789abcdef", statusToken, form)
	if !acknowledged || !notified {
		t.Fatal("acknowledgment was not accepted by the mail transport")
	}

	if len(sender.emails) != 2 {
		t.Fatalf("sent %d emails, want one maintainer notification and one acknowledgment", len(sender.emails))
	}
	if !reflect.DeepEqual(sender.emails[0].to, []string{"security@example.test"}) {
		t.Errorf("maintainer recipients = %v", sender.emails[0].to)
	}
	maintainer := sender.emails[0]
	if len(maintainer.attachments) != 1 {
		t.Fatalf("maintainer email has %d attachments, want one", len(maintainer.attachments))
	}
	if len(maintainer.attachments[0].Data) == 0 || !strings.Contains(maintainer.body, "WARNING: no PGP keypair") {
		t.Errorf("maintainer email lacks warning or encrypted attachment: %+v", maintainer)
	}
	for _, private := range []string{form.Name, form.Email, form.Summary, form.Steps, form.Impact} {
		if strings.Contains(maintainer.body, private) || strings.Contains(string(maintainer.attachments[0].Data), private) {
			t.Errorf("maintainer email contains plaintext report content %q", private)
		}
	}
	ack := sender.emails[1]
	if !reflect.DeepEqual(ack.to, []string{"researcher@example.test"}) {
		t.Errorf("acknowledgment recipients = %v", ack.to)
	}
	if !strings.Contains(ack.body, "Tracking ID: sec_0123456789abcdef\n") ||
		!strings.Contains(ack.body, "Status URL: https://example.test/server/security/report/sec_0123456789abcdef?token=f1a4d882\n") {
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

func TestNotifySecurityReportAcknowledgmentFailureIsNotReportedAsSent(t *testing.T) {
	cfg := config.Default("")
	cfg.Server.Contact.Security.Email = "security@example.test"
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	fd := &frontendDeps{cfg: cfg, configDir: t.TempDir(), resolver: NewProxyResolver(nil)}
	sender := &recordingSecuritySender{enabled: true, err: errors.New("smtp unavailable")}
	acknowledged, notified := fd.notifySecurityReportWith(sender, httptest.NewRequest("POST", "https://example.test/", nil), "sec_0123456789abcdef", "token", securityReportForm{Email: "researcher@example.test"})
	if acknowledged || notified {
		t.Fatal("failed acknowledgment was reported as sent")
	}
}

func TestNotifySecurityReportSkipsAcknowledgmentWithoutResearcherEmail(t *testing.T) {
	cfg := config.Default("")
	cfg.Server.Contact.Security.Email = "security@example.test"
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	fd := &frontendDeps{cfg: cfg, configDir: t.TempDir()}
	sender := &recordingSecuritySender{enabled: true}
	fd.notifySecurityReportWith(sender, httptest.NewRequest("POST", "https://example.test/", nil), "sec_0123456789abcdef", "token", securityReportForm{})
	if len(sender.emails) != 1 {
		t.Fatalf("sent %d emails, want only the maintainer notification", len(sender.emails))
	}
}

func TestNotifySecurityReportDoesNotSendWhenEmailIsDisabled(t *testing.T) {
	fd := &frontendDeps{cfg: config.Default(""), configDir: t.TempDir()}
	sender := &recordingSecuritySender{}
	fd.notifySecurityReportWith(sender, httptest.NewRequest("POST", "https://example.test/", nil), "sec_0123456789abcdef", "token", securityReportForm{
		Email: "researcher@example.test",
	})
	if len(sender.emails) != 0 {
		t.Fatalf("sent %d emails while notifications are disabled", len(sender.emails))
	}
}
