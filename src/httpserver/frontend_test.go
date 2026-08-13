package httpserver

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
)

func testFrontendDeps(t *testing.T) (*frontendDeps, *linkDeps) {
	t.Helper()
	ld, _ := testLinkDeps(t)
	cfg := config.Default(":memory:")
	fd := &frontendDeps{cfg: cfg, version: "test", buildDate: "2026-01-01", ld: ld}
	return fd, ld
}

func htmlRequest(method, target string, form url.Values) *http.Request {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rctx := chi.NewRouteContext()
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func TestHomeHandler_GETRendersForm(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.homeHandler(rec, htmlRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "create-link-form") {
		t.Errorf("expected the create-link form in body, got: %s", rec.Body.String())
	}
}

func TestHomeHandler_POSTCreatesLink(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{"url": {"https://example.com/page"}}
	rec := httptest.NewRecorder()
	fd.homeHandler(rec, htmlRequest(http.MethodPost, "/", form))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Success") || !strings.Contains(body, "api-token") {
		t.Errorf("expected success state with owner token, got: %s", body)
	}
}

func TestHomeHandler_POSTInvalidURL(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{"url": {"not-a-url"}}
	rec := httptest.NewRecorder()
	fd.homeHandler(rec, htmlRequest(http.MethodPost, "/", form))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "valid http") {
		t.Errorf("expected validation error message, got: %s", rec.Body.String())
	}
}

func TestHomeHandler_POSTReservedSlug(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{"url": {"https://example.com"}, "slug": {"api"}}
	rec := httptest.NewRecorder()
	fd.homeHandler(rec, htmlRequest(http.MethodPost, "/", form))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestHomeHandler_POSTCustomSlugTaken(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	if _, err := db.CreateLinkCustomSlug(context.Background(), ld.sqlDB, "myslug", "https://example.com", nil); err != nil {
		t.Fatalf("seed CreateLinkCustomSlug: %v", err)
	}
	form := url.Values{"url": {"https://example.com/other"}, "slug": {"myslug"}}
	rec := httptest.NewRecorder()
	fd.homeHandler(rec, htmlRequest(http.MethodPost, "/", form))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestAboutHandler(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.aboutHandler(rec, htmlRequest(http.MethodGet, "/server/about", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "About") {
		t.Errorf("expected About heading, got: %s", rec.Body.String())
	}
}

func TestHelpHandler(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.helpHandler(rec, htmlRequest(http.MethodGet, "/server/help", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Getting Started") {
		t.Errorf("expected Getting Started section, got: %s", rec.Body.String())
	}
}

func TestTermsHandler(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.termsHandler(rec, htmlRequest(http.MethodGet, "/server/terms", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Acceptance of Terms") {
		t.Errorf("expected default terms content, got: %s", rec.Body.String())
	}
}

func TestPrivacyHandler(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.privacyHandler(rec, htmlRequest(http.MethodGet, "/server/privacy", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Cookie Policy") {
		t.Errorf("expected Cookie Policy section, got: %s", rec.Body.String())
	}
}

func TestContactHandler_GET(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.contactHandler(rec, htmlRequest(http.MethodGet, "/server/contact", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "contact-form") {
		t.Errorf("expected contact form, got: %s", rec.Body.String())
	}
}

func TestContactHandler_POSTSuccess(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{
		"name": {"Jane"}, "email": {"jane@example.com"},
		"subject": {"Hello"}, "message": {"A message"}, "captcha": {"7"},
	}
	rec := httptest.NewRecorder()
	fd.contactHandler(rec, htmlRequest(http.MethodPost, "/server/contact", form))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Thank you") {
		t.Errorf("expected success message, got: %s", rec.Body.String())
	}
}

func TestContactHandler_POSTBadCaptcha(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{
		"name": {"Jane"}, "email": {"jane@example.com"},
		"subject": {"Hello"}, "message": {"A message"}, "captcha": {"9"},
	}
	rec := httptest.NewRecorder()
	fd.contactHandler(rec, htmlRequest(http.MethodPost, "/server/contact", form))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func TestContactHandler_Disabled(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.cfg.Pages.Contact.Enabled = false
	rec := httptest.NewRecorder()
	fd.contactHandler(rec, htmlRequest(http.MethodGet, "/server/contact", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "disabled") {
		t.Errorf("expected disabled message, got: %s", rec.Body.String())
	}
}

func TestConsentHandler_Accept(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{"decision": {"accept"}}
	rec := httptest.NewRecorder()
	fd.consentHandler(rec, htmlRequest(http.MethodPost, "/server/consent", form))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == consentCookieName {
			found = true
			decoded, err := base64.RawURLEncoding.DecodeString(c.Value)
			if err != nil {
				t.Fatalf("cookie value not valid base64: %v", err)
			}
			if !strings.Contains(string(decoded), `"essential":true`) {
				t.Errorf("expected essential:true in decoded cookie, got %q", decoded)
			}
		}
	}
	if !found {
		t.Fatal("expected cookie_consent cookie to be set")
	}
}

func TestConsentHandler_RejectsOpenRedirect(t *testing.T) {
	tests := []struct {
		name       string
		returnTo   string
		wantHeader string
	}{
		{"protocol-relative", "//evil.example.com", "/"},
		{"backslash-disguised", "/\\evil.example.com", "/"},
		{"scheme-bearing", "https://evil.example.com/", "/"},
		{"safe local path", "/server/help", "/server/help"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd, _ := testFrontendDeps(t)
			form := url.Values{"decision": {"accept"}, "return_to": {tt.returnTo}}
			rec := httptest.NewRecorder()
			fd.consentHandler(rec, htmlRequest(http.MethodPost, "/server/consent", form))
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != tt.wantHeader {
				t.Errorf("Location = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}

func TestIsSafeLocalRedirect(t *testing.T) {
	tests := []struct {
		dest string
		want bool
	}{
		{"", false},
		{"/", true},
		{"/server/help", true},
		{"//evil.example.com", false},
		{"/\\evil.example.com", false},
		{"https://evil.example.com", false},
		{"/foo?bar=1", true},
	}
	for _, tt := range tests {
		if got := isSafeLocalRedirect(tt.dest); got != tt.want {
			t.Errorf("isSafeLocalRedirect(%q) = %v, want %v", tt.dest, got, tt.want)
		}
	}
}

func TestCCPAHandler_NotFoundWhenNotSold(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.ccpaHandler(rec, htmlRequest(http.MethodGet, "/server/ccpa", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCCPAHandler_RendersWhenSold(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.cfg.Server.Privacy.Data.Sold = true
	rec := httptest.NewRecorder()
	fd.ccpaHandler(rec, htmlRequest(http.MethodGet, "/server/ccpa", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Do Not Sell") {
		t.Errorf("expected CCPA heading, got: %s", rec.Body.String())
	}
}

func TestHealthzHTMLHandler_RendersHTMLForBrowsers(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	hd := &healthDeps{sqlDB: ld.sqlDB, dataDir: t.TempDir(), startTime: time.Now(), stats: NewStats(), version: "test"}
	rec := httptest.NewRecorder()
	fd.healthzHTMLHandler(hd)(rec, htmlRequest(http.MethodGet, "/server/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Server Health") {
		t.Errorf("expected HTML health page, got: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHealthzHTMLHandler_FallsBackToJSONForAPIClients(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	hd := &healthDeps{sqlDB: ld.sqlDB, dataDir: t.TempDir(), startTime: time.Now(), stats: NewStats(), version: "test"}
	req := htmlRequest(http.MethodGet, "/server/healthz", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	fd.healthzHTMLHandler(hd)(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestStatsHTMLHandler_RendersHTMLForBrowsers(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	link, err := db.CreateLinkAutoCode(context.Background(), ld.sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode: %v", err)
	}
	req := htmlRequest(http.MethodGet, "/"+link.ShortCode+"/stats", nil)
	rctx := chi.RouteContext(req.Context())
	rctx.URLParams.Add("slug", link.ShortCode)
	rec := httptest.NewRecorder()
	fd.statsHTMLHandler(ld)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Click Statistics") {
		t.Errorf("expected HTML stats page, got: %s", rec.Body.String())
	}
}

func TestStatsHTMLHandler_FallsBackToJSONForAPIClients(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	link, err := db.CreateLinkAutoCode(context.Background(), ld.sqlDB, "https://example.com", nil)
	if err != nil {
		t.Fatalf("CreateLinkAutoCode: %v", err)
	}
	req := htmlRequest(http.MethodGet, "/"+link.ShortCode+"/stats", nil)
	req.Header.Set("Accept", "application/json")
	rctx := chi.RouteContext(req.Context())
	rctx.URLParams.Add("slug", link.ShortCode)
	rec := httptest.NewRecorder()
	fd.statsHTMLHandler(ld)(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
