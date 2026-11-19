package httpserver

import (
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
	if fd.contactDuplicateKey(" ADA@example.com ", " Hello ", "one  two") != fd.contactDuplicateKey("ada@example.com", "hello", "one two") {
		t.Fatal("duplicate hashing did not normalize case and whitespace")
	}
}
