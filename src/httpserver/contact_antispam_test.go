package httpserver

import (
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
)

func TestContactFormTokenAgeAndSignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	token := issueContactFormToken("installation-secret", now.Add(-4*time.Second))
	if !verifyContactFormToken("installation-secret", token, now, 3, 60) {
		t.Fatal("valid token was rejected")
	}
	if verifyContactFormToken("wrong-secret", token, now, 3, 60) {
		t.Fatal("token signed with another secret was accepted")
	}
	if verifyContactFormToken("installation-secret", token, now.Add(-2*time.Second), 3, 60) {
		t.Fatal("token younger than minimum fill time was accepted")
	}
	if verifyContactFormToken("installation-secret", token, now.Add(2*time.Minute), 3, 60) {
		t.Fatal("expired token was accepted")
	}
}

func TestContactShapeValidation(t *testing.T) {
	valid := url.Values{
		"name": {"Ada"}, "email": {"ada@example.com"}, "subject": {"Hello"},
		"message": {"Message"}, "form_token": {"token"}, "captcha_id": {"id"},
		"captcha_answer": {"12"}, "website": {""},
	}
	if reason, ok := contactShapeValid(valid, false, false); !ok {
		t.Fatalf("valid browser form rejected: %s", reason)
	}
	api := cloneURLValues(valid)
	delete(api, "website")
	if reason, ok := contactShapeValid(api, false, true); !ok {
		t.Fatalf("valid API form rejected: %s", reason)
	}
	for name, mutate := range map[string]func(url.Values){
		"unknown field":            func(v url.Values) { v.Set("unexpected", "x") },
		"control in subject":       func(v url.Values) { v.Set("subject", "hello\r\nBcc: x@example.com") },
		"multiple email addresses": func(v url.Values) { v.Set("email", "a@example.com,b@example.com") },
		"missing simple proof":     func(v url.Values) { delete(v, "captcha_id"); delete(v, "captcha_answer") },
	} {
		t.Run(name, func(t *testing.T) {
			values := cloneURLValues(valid)
			mutate(values)
			if reason, ok := contactShapeValid(values, false, false); ok || reason != "shape" {
				t.Fatalf("contactShapeValid() = (%q,%v), want (shape,false)", reason, ok)
			}
		})
	}
}

func cloneURLValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, entries := range values {
		clone[key] = append([]string(nil), entries...)
	}
	return clone
}

func TestContactContentRules(t *testing.T) {
	fd := &frontendDeps{cfg: config.Default("unused"), installSecret: "content-test"}
	if fd.contactContentBlocked("Ada", "ada@example.com", "Question", "Two https:// links https://example.com") {
		t.Fatal("message within max_links was blocked")
	}
	if !fd.contactContentBlocked("Ada", "ada@example.com", "Question", "https://one.test https://two.test https://three.test") {
		t.Fatal("message above max_links was accepted")
	}
	for _, message := range []string{"<a href=\"https://example.com\">link</a>", "[url=https://example.com]link[/url]", "&lt;script&gt;"} {
		if got := fd.contactContentReason("Ada", "ada@example.com", "Question", message); got != "markup" {
			t.Errorf("contactContentReason(%q) = %q, want markup", message, got)
		}
	}
	if got := fd.contactContentReason("Ada", "ada@example.com", "Question", "Visit one.test two.example three.shop"); got != "content" {
		t.Errorf("bare-domain link count reason = %q, want content", got)
	}
	settings := fd.cfg.Server.Contact.Antispam
	settings.BlockedLinkDomains = []string{"example.shop"}
	fd.setContactAntispam(settings)
	if got := fd.contactContentReason("Ada", "ada@example.com", "Question", "Visit a.example.shop"); got != "content" {
		t.Errorf("blocked suffix domain reason = %q, want content", got)
	}
	if fd.contactDuplicateKey(" ADA@example.com ", " Hello ", "one  two") != fd.contactDuplicateKey("ada@example.com", "hello", "one two") {
		t.Fatal("duplicate hashing did not normalize case and whitespace")
	}
}

func TestContactStrikesBanAndOverlayExemption(t *testing.T) {
	fd := &frontendDeps{cfg: config.Default("unused"), installSecret: "strikes-test"}
	fd.cfg.Server.Contact.Antispam.Window = "1h"
	fd.cfg.Server.Contact.Antispam.Strikes = 3
	fd.cfg.Server.Contact.Antispam.BanHours = 24
	request := httptest.NewRequest("POST", "http://example.test/server/contact", nil)
	request.RemoteAddr = "192.0.2.18:4321"
	now := time.Now()
	for i := 0; i < 3; i++ {
		fd.contactSpamBlock(request, "captcha")
	}
	if reason, blocked := fd.contactRateReason(request, "ada@example.com", now.Add(4*time.Second)); !blocked || reason != "banned" {
		t.Fatalf("contactRateReason() = (%q,%v), want (banned,true)", reason, blocked)
	}

	overlayFD := &frontendDeps{cfg: config.Default("unused"), installSecret: "overlay-strikes-test", resolver: NewProxyResolver(nil)}
	overlayFD.cfg.Server.Contact.Antispam.Strikes = 1
	overlayFD.resolver.SetOverlayHost(OverlayTor, "exampleonion.onion")
	overlayRequest := httptest.NewRequest("POST", "http://exampleonion.onion/server/contact", nil)
	overlayFD.contactSpamBlock(overlayRequest, "captcha")
	if reason, blocked := overlayFD.contactRateReason(overlayRequest, "ada@example.com", now.Add(time.Second)); blocked || reason != "" {
		t.Fatalf("overlay contactRateReason() = (%q,%v), want no IP ban", reason, blocked)
	}
}

func TestContactPerIPLimitUsesConfiguredThreshold(t *testing.T) {
	fd := &frontendDeps{cfg: config.Default("unused"), installSecret: "ip-rate-test"}
	request := httptest.NewRequest("POST", "http://example.test/server/contact", nil)
	request.RemoteAddr = "192.0.2.19:4321"
	now := time.Now()
	for i := 0; i < fd.contactAntispam().PerIP; i++ {
		email := fmt.Sprintf("ada%d@example.com", i)
		if reason, blocked := fd.contactRecordAccepted(request, email, "subject", "message", now.Add(time.Duration(i)*time.Second), false); blocked {
			t.Fatalf("accepted submission %d blocked: %s", i+1, reason)
		}
	}
	if reason, blocked := fd.contactRateReason(request, "new@example.com", now.Add(time.Minute)); !blocked || reason != "rate_ip" {
		t.Fatalf("submission after configured limit = (%q,%v), want (rate_ip,true)", reason, blocked)
	}
}
