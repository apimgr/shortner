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
	if _, ok := verifyContactFormToken("installation-secret", token, now, 3, 60); !ok {
		t.Fatal("valid token was rejected")
	}
	if _, ok := verifyContactFormToken("wrong-secret", token, now, 3, 60); ok {
		t.Fatal("token signed with another secret was accepted")
	}
	if _, ok := verifyContactFormToken("installation-secret", token, now.Add(-2*time.Second), 3, 60); ok {
		t.Fatal("token younger than minimum fill time was accepted")
	}
	if _, ok := verifyContactFormToken("installation-secret", token, now.Add(2*time.Minute), 3, 60); ok {
		t.Fatal("expired token was accepted")
	}
}

func TestContactFormTokenIsSingleUseAndFitsFieldLimit(t *testing.T) {
	fd := &frontendDeps{cfg: config.Default("unused"), installSecret: "single-use-contact-token-test"}
	token := issueContactFormToken(fd.installSecret, time.Now().Add(-4*time.Second))
	if len(token) > 100 {
		t.Fatalf("form token length = %d, exceeds field limit", len(token))
	}
	if reason := fd.contactTokenValidationReason(token, time.Now()); reason != "" {
		t.Fatalf("first token use rejected: %s", reason)
	}
	if reason := fd.contactTokenValidationReason(token, time.Now()); reason != "replay" {
		t.Fatalf("reused token reason = %q, want replay", reason)
	}
}

func TestContactReviewTokenBindsFieldsAndAge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	values := url.Values{"email": {"ada@example.com"}, "subject": {"Report"}, "message": {"Details"}}
	token := issueContactReviewToken("review-secret", values, now, "")
	if len(token) > 300 {
		t.Fatalf("review token length = %d, exceeds field limit", len(token))
	}
	if _, ok := verifyContactReviewToken("review-secret", token, values, now.Add(3*time.Second), 2, 900); !ok {
		t.Fatal("valid review token was rejected")
	}
	changed := cloneURLValues(values)
	changed.Set("message", "changed")
	if _, ok := verifyContactReviewToken("review-secret", token, changed, now.Add(3*time.Second), 2, 900); ok {
		t.Fatal("review token accepted changed fields")
	}
	if _, ok := verifyContactReviewToken("review-secret", token, values, now.Add(time.Hour), 2, 900); ok {
		t.Fatal("expired review token was accepted")
	}
}

func TestContactOriginRequiresSameSiteContext(t *testing.T) {
	fd := &frontendDeps{resolver: NewProxyResolver(nil)}
	request := httptest.NewRequest("POST", "http://example.test/server/contact", nil)
	if fd.contactOriginValid(request) {
		t.Fatal("request without Origin, Referer, or Sec-Fetch-Site was accepted")
	}
	request.Header.Set("Origin", "https://elsewhere.test")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	if fd.contactOriginValid(request) {
		t.Fatal("request with mismatched Origin was accepted")
	}
	request.Header.Set("Origin", "http://example.test")
	if !fd.contactOriginValid(request) {
		t.Fatal("same-origin request was rejected")
	}
	request.Header.Del("Origin")
	if !fd.contactOriginValid(request) {
		t.Fatal("same-origin Sec-Fetch-Site request was rejected")
	}
}

func TestContactShapeValidation(t *testing.T) {
	valid := url.Values{
		"name": {"Ada"}, "email": {"ada@example.com"}, "subject": {"Hello"},
		"message": {"Message"}, "form_token": {"token"}, "captcha_id": {"id"},
		"type": {"abuse"}, "reported_url": {"https://example.test/short"}, "reason": {"terms_violation"},
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
	request := httptest.NewRequest("POST", "http://example.test/server/contact", nil)
	values := url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "subject": {"Question"}, "message": {"Two https:// links https://example.test"}}
	if fd.contactContentReason(request, values) != "" {
		t.Fatal("message within max_links was blocked")
	}
	values.Set("message", "https://one.test https://two.test https://three.test https://four.test https://five.test https://six.test https://seven.test https://eight.test https://nine.test https://ten.test https://eleven.test")
	if fd.contactContentReason(request, values) == "" {
		t.Fatal("message above max_links was accepted")
	}
	for _, message := range []string{"<a href=\"https://example.com\">link</a>", "[url=https://example.com]link[/url]", "&lt;script&gt;"} {
		values.Set("message", message)
		if got := fd.contactContentReason(request, values); got != "markup" {
			t.Errorf("contactContentReason(%q) = %q, want markup", message, got)
		}
	}
	values.Set("message", "Visit external.example")
	if got := fd.contactContentReason(request, values); got != "content" {
		t.Errorf("external host reason = %q, want content", got)
	}
	settings := fd.contactAntispam()
	settings.BlockedEmailDomains = []string{"blocked.test"}
	settings.BlockedWords = []string{"forbidden phrase"}
	fd.setContactAntispam(settings)
	values.Set("email", "ada@blocked.test")
	values.Set("message", "A normal report")
	if got := fd.contactContentReason(request, values); got != "content" {
		t.Errorf("blocked email domain reason = %q, want content", got)
	}
	values.Set("email", "ada@example.test")
	values.Set("message", "This contains a forbidden phrase")
	if got := fd.contactContentReason(request, values); got != "content" {
		t.Errorf("blocked word reason = %q, want content", got)
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
