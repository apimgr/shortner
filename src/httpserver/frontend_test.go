package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/pgp"
	"github.com/apimgr/shortner/src/security"
)

func testFrontendDeps(t *testing.T) (*frontendDeps, *linkDeps) {
	t.Helper()
	ld, _ := testLinkDeps(t)
	cfg := config.Default(":memory:")
	store, err := db.Open(filepath.Join(t.TempDir(), "contact.sqlite"), db.DefaultPool(), nil)
	if err != nil {
		t.Fatalf("open contact test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fd := &frontendDeps{cfg: cfg, version: "test", buildDate: "2026-01-01", ld: ld, resolver: NewProxyResolver(nil), store: store, installSecret: "test-install-secret"}
	fd.routeMatch = func(path string) (string, map[string]string, bool) {
		if path == "/server/about" {
			return "/server/about", nil, true
		}
		return "", nil, false
	}
	fd.setContactAntispam(cfg.Server.Contact.Antispam)
	return fd, ld
}

func TestStoreSecurityReportUsesProjectPGPKeyWhenConfigured(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.configDir = t.TempDir()
	key, err := pgp.Generate("Shortner", "security@example.test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := pgp.NewStore(fd.configDir).Write(key, fd.installSecret); err != nil {
		t.Fatal(err)
	}
	form := securityReportForm{Summary: "private report body", Severity: "high", Component: "API"}
	id, err := fd.storeSecurityReport(context.Background(), form)
	if err != nil {
		t.Fatalf("storeSecurityReport() error = %v", err)
	}
	report, err := db.GetSecurityReport(context.Background(), fd.ld.sqlDB, id)
	if err != nil {
		t.Fatal(err)
	}
	if !pgp.IsArmoredMessage(report.Sealed) {
		t.Fatal("stored report is not PGP-encrypted")
	}
	plain, err := pgp.DecryptArmored(key, report.Sealed)
	if err != nil {
		t.Fatalf("DecryptArmored() error = %v", err)
	}
	var got securityReportForm
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary != form.Summary {
		t.Errorf("decrypted summary = %q, want %q", got.Summary, form.Summary)
	}
}

func TestStoreSecurityReportUsesAESWhenNoProjectPGPKey(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.configDir = t.TempDir()
	keyBytes := make([]byte, 32)
	fd.cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(keyBytes)
	form := securityReportForm{Summary: "private report body", Severity: "high", Component: "API"}
	id, err := fd.storeSecurityReport(context.Background(), form)
	if err != nil {
		t.Fatalf("storeSecurityReport() error = %v", err)
	}
	report, err := db.GetSecurityReport(context.Background(), fd.ld.sqlDB, id)
	if err != nil {
		t.Fatal(err)
	}
	if pgp.IsArmoredMessage(report.Sealed) {
		t.Fatal("stored fallback report unexpectedly uses PGP")
	}
	plain, err := security.Open(keyBytes, report.Sealed)
	if err != nil {
		t.Fatalf("security.Open() error = %v", err)
	}
	var got securityReportForm
	if err := json.Unmarshal(plain, &got); err != nil {
		t.Fatal(err)
	}
	if got.Summary != form.Summary {
		t.Errorf("decrypted summary = %q, want %q", got.Summary, form.Summary)
	}
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
	if method == http.MethodPost {
		scheme := req.URL.Scheme
		if scheme == "" {
			scheme = "http"
		}
		req.Header.Set("Origin", scheme+"://"+req.Host)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	rctx := chi.NewRouteContext()
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func TestHomeHandler_GETRendersForm(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.cfg.Server.Branding.Title = "Shortner Test"
	fd.cfg.Server.Branding.Description = "A configured description"
	fd.cfg.Server.SEO.Keywords = []string{"short links", "privacy"}
	fd.cfg.Server.SEO.Verification.Google = "abc_123"
	rec := httptest.NewRecorder()
	fd.homeHandler(rec, htmlRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "create-link-form") {
		t.Errorf("expected the create-link form in body, got: %s", rec.Body.String())
	}
	for _, want := range []string{"Shortner Test", `name="robots" content="index,follow"`, `name="keywords" content="short links, privacy"`, `google-site-verification" content="abc_123"`, `property="og:site_name" content="Shortner Test"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("page metadata missing %q", want)
		}
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
	form := contactTestForm(t, fd, "jane@example.com")
	form.Set("name", "Jane")
	form.Set("subject", "Hello")
	form.Set("message", "A message")
	rec := httptest.NewRecorder()
	req := htmlRequest(http.MethodPost, "/server/contact", form)
	if !fd.contactOriginValid(req) {
		proto, host, port := fd.resolver.GetURLVars(req)
		t.Fatalf("test origin rejected: origin=%q fetch=%q resolved=%s %s %s request_host=%q", req.Header.Get("Origin"), req.Header.Get("Sec-Fetch-Site"), proto, host, port, req.Host)
	}
	fd.contactHandler(rec, req)
	if rec.Code != http.StatusOK {
		body := rec.Body.String()
		start := strings.Index(body, `class="form-error"`)
		if start >= 0 {
			end := min(len(body), start+220)
			t.Fatalf("status = %d, form error: %s", rec.Code, body[start:end])
		}
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "contact-review") {
		t.Errorf("expected review step, got: %s", rec.Body.String())
	}
}

func TestContactHandler_POSTBadCaptcha(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := contactTestForm(t, fd, "jane@example.com")
	form.Set("name", "Jane")
	form.Set("subject", "Hello")
	form.Set("message", "A message")
	form.Set("captcha_answer", "9")
	rec := httptest.NewRecorder()
	fd.contactHandler(rec, htmlRequest(http.MethodPost, "/server/contact", form))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

func contactTestForm(t *testing.T, fd *frontendDeps, email string) url.Values {
	t.Helper()
	now := time.Now()
	id := "test-captcha-id"
	if err := fd.store.IssueCaptchaChallenge(context.Background(), id, captchaAnswerHash(id, "7"), now, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("issue test challenge: %v", err)
	}
	return url.Values{
		"name": {"Jane"}, "email": {email},
		"subject": {"Hello"}, "message": {"A message"}, "captcha_id": {id},
		"type": {"abuse"}, "reported_url": {"https://example.com/server/about"}, "reason": {"terms_violation"},
		"captcha_answer": {"7"}, "website": {""},
		"form_token": {issueContactFormToken(fd.installSecret, now.Add(-4*time.Second))},
	}
}

func TestContactHandler_Disabled(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.cfg.Server.Pages.Contact.Enabled = false
	rec := httptest.NewRecorder()
	fd.contactHandler(rec, htmlRequest(http.MethodGet, "/server/contact", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
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

func TestListHTMLHandler_RendersHTMLForBrowsers(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	for i := 0; i < 2; i++ {
		if _, err := db.CreateLinkAutoCode(context.Background(), ld.sqlDB, "https://example.com", nil); err != nil {
			t.Fatalf("CreateLinkAutoCode: %v", err)
		}
	}
	req := htmlRequest(http.MethodGet, "/list", nil)
	rec := httptest.NewRecorder()
	fd.listHTMLHandler(ld)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "All Links") {
		t.Errorf("expected HTML list page, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "example.com") {
		t.Errorf("expected list page to include created link, got: %s", rec.Body.String())
	}
}

func TestListHTMLHandler_FallsBackToJSONForAPIClients(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	req := htmlRequest(http.MethodGet, "/list", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	fd.listHTMLHandler(ld)(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
