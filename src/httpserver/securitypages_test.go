package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

// testSecurityFrontendDeps builds a frontendDeps with the resolver,
// installSecret, audit logger, and configDir the PART 11 security pages
// depend on — testFrontendDeps in frontend_test.go leaves these zero,
// which would nil-panic here.
func testSecurityFrontendDeps(t *testing.T) *frontendDeps {
	t.Helper()
	ld, _ := testLinkDeps(t)
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	audit, err := applog.NewAuditLogger(auditPath)
	if err != nil {
		t.Fatalf("applog.NewAuditLogger() error = %v", err)
	}
	t.Cleanup(func() { audit.Close() })

	cfg := config.Default(":memory:")
	return &frontendDeps{
		cfg:           cfg,
		version:       "test",
		buildDate:     "2026-01-01",
		ld:            ld,
		resolver:      NewProxyResolver(nil),
		installSecret: "test-install-secret",
		audit:         audit,
		configDir:     t.TempDir(),
	}
}

// TestSecurityHandlerRendersConfiguredValues proves the page picks up
// live config (report URL, PGP key presence, thanks) rather than static
// content.
func TestSecurityHandlerRendersConfiguredValues(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	fd.cfg.Web.Security.ReportURL = "https://example.com/security/report"

	req := htmlRequest(http.MethodGet, "/server/security", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	fd.securityHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "https://example.com/security/report") {
		t.Errorf("expected the configured report URL in body, got: %s", rec.Body.String())
	}
}

// TestSecurityHandlerHasPGPKeyReflectsFileState proves HasPGPKey tracks
// the actual on-disk key rather than a fixed value.
func TestSecurityHandlerHasPGPKeyReflectsFileState(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	fd.cfg.Web.Security.PublishPGPKey = true

	if fd.hasPGPKey() {
		t.Fatal("expected hasPGPKey() = false before any key file exists")
	}

	keyPath := filepath.Join(fd.configDir, "security", "pgp.pub.asc")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !fd.hasPGPKey() {
		t.Error("expected hasPGPKey() = true once the key file exists")
	}
}

// TestSecurityPolicyHandlerDefaultPolicy proves the built-in policy text
// renders (split into paragraphs) when no operator policy is configured.
func TestSecurityPolicyHandlerDefaultPolicy(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	req := htmlRequest(http.MethodGet, "/server/security/policy", nil)
	rec := httptest.NewRecorder()
	fd.securityPolicyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "coordinated disclosure") {
		t.Errorf("expected the default disclosure policy text, got: %s", rec.Body.String())
	}
}

// TestSecurityPolicyHandlerOperatorPolicy proves an operator-supplied
// policy replaces the built-in default.
func TestSecurityPolicyHandlerOperatorPolicy(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	fd.cfg.Web.Security.Policy = "Custom policy paragraph one.\n\nCustom policy paragraph two."

	req := htmlRequest(http.MethodGet, "/server/security/policy", nil)
	rec := httptest.NewRecorder()
	fd.securityPolicyHandler(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Custom policy paragraph one.") || !strings.Contains(body, "Custom policy paragraph two.") {
		t.Errorf("expected both custom paragraphs, got: %s", body)
	}
	if strings.Contains(body, "coordinated disclosure") {
		t.Errorf("expected the default policy text to be fully replaced, got: %s", body)
	}
}

// TestSecurityThanksHandlerRendersEntries proves the acknowledgments page
// lists operator-curated entries.
func TestSecurityThanksHandlerRendersEntries(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	fd.cfg.Web.Security.Thanks = []config.SecurityThanks{
		{Name: "Jane Researcher", Year: 2025, Credit: "Found the CSP bypass"},
	}

	req := htmlRequest(http.MethodGet, "/server/security/thanks", nil)
	rec := httptest.NewRecorder()
	fd.securityThanksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Jane Researcher") {
		t.Errorf("expected the thanks entry to render, got: %s", rec.Body.String())
	}
}

// TestDpoHandlerNotFoundByDefault proves the DPO page 404s when no
// compliance standard requiring it is enabled.
func TestDpoHandlerNotFoundByDefault(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	req := htmlRequest(http.MethodGet, "/server/dpo", nil)
	rec := httptest.NewRecorder()
	fd.dpoHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no compliance standard requires a DPO", rec.Code)
	}
}

// TestDpoHandlerRendersWhenGDPREnabled proves GDPR enables the page and
// carries the configured DPO contact through.
func TestDpoHandlerRendersWhenGDPREnabled(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	fd.cfg.Server.Compliance.GDPR = true
	fd.cfg.Server.Contact.DPO.Name = "Data Officer"
	fd.cfg.Server.Contact.DPO.Email = "dpo@example.com"

	req := htmlRequest(http.MethodGet, "/server/dpo", nil)
	rec := httptest.NewRecorder()
	fd.dpoHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dpo@example.com") {
		t.Errorf("expected the configured DPO email, got: %s", rec.Body.String())
	}
}

// TestDpoHandlerRendersWhenLGPDEnabled proves LGPD alone (without GDPR)
// also unlocks the page, per RequiresDPOContact's OR condition.
func TestDpoHandlerRendersWhenLGPDEnabled(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	fd.cfg.Server.Compliance.LGPD = true

	req := htmlRequest(http.MethodGet, "/server/dpo", nil)
	rec := httptest.NewRecorder()
	fd.dpoHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when LGPD is enabled", rec.Code)
	}
}

// TestSecurityContactEmailFallsBackToFQDN proves the security@{fqdn}
// default applies when neither web.security.contact nor
// server.contact.security.email is configured.
func TestSecurityContactEmailFallsBackToFQDN(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	req := htmlRequest(http.MethodGet, "/server/security", nil)
	req.Host = "shortner.example"

	if got := fd.securityContactEmail(req); got != "security@shortner.example" {
		t.Errorf("securityContactEmail() = %q, want security@shortner.example", got)
	}

	fd.cfg.Web.Security.Contact = "explicit@example.com"
	if got := fd.securityContactEmail(req); got != "explicit@example.com" {
		t.Errorf("securityContactEmail() = %q, want the explicit web.security.contact override", got)
	}
}
