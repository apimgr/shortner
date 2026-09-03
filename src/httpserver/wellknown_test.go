package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

// testWellKnownDeps builds a wellKnownDeps rooted at a temp data/config
// dir, so file-backed entries can be exercised hermetically.
func testWellKnownDeps(t *testing.T) *wellKnownDeps {
	t.Helper()
	cfg := config.Default(":memory:")
	return &wellKnownDeps{
		cfg:           cfg,
		resolver:      NewProxyResolver(nil),
		dataDir:       t.TempDir(),
		configDir:     t.TempDir(),
		installSecret: "test-install-secret",
	}
}

// TestWellKnownHandlerMethodNotAllowed proves a non-GET/HEAD request gets
// 405 with the required Allow header, per the namespace contract.
func TestWellKnownHandlerMethodNotAllowed(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/.well-known/security.txt", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

// TestWellKnownHandlerBareDirectoryIs404 proves the bare /.well-known/
// path never serves a directory index.
func TestWellKnownHandlerBareDirectoryIs404(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for the bare directory", rec.Code)
	}
}

// TestWellKnownHandlerNestedPathIs404 proves a nested path under a flat
// entry is treated as a different, non-existent resource.
func TestWellKnownHandlerNestedPathIs404(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt/extra", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a nested path", rec.Code)
	}
}

// TestWellKnownHandlerUnknownEntryIs404 proves an entry outside the
// allowlist 404s and never redirects.
func TestWellKnownHandlerUnknownEntryIs404(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/change-password", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unlisted entry", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("expected no Location header (no redirect), got %q", loc)
	}
}

// TestWellKnownHandlerAllowlistedButDisabledIs404 proves an entry that is
// allowlisted but not enabled for this deployment still 404s.
func TestWellKnownHandlerAllowlistedButDisabledIs404(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a disabled optional entry", rec.Code)
	}
}

// TestWellKnownHandlerSecurityTxt proves security.txt renders with the
// expected content type and body markers.
func TestWellKnownHandlerSecurityTxt(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.Contains(rec.Body.String(), "Contact:") {
		t.Errorf("expected a Contact: line, got: %s", rec.Body.String())
	}
	// The scheme comes from the request, which is plain HTTP here, so the
	// canonical URL is matched by path rather than by a hardcoded scheme.
	if !strings.Contains(rec.Body.String(), "Canonical: http://example.com/.well-known/security.txt") {
		t.Errorf("expected a Canonical line, got: %s", rec.Body.String())
	}
}

// TestWellKnownHandlerHEADHasNoBody proves a HEAD request writes the
// status and headers but no body.
func TestWellKnownHandlerHEADHasNoBody(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodHead, "/.well-known/security.txt", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected an empty body for HEAD, got %q", rec.Body.String())
	}
}

// TestWellKnownHandlerPGPKeyMissingIs404 proves /.well-known/pgp-key.asc
// 404s when no keypair has been generated.
func TestWellKnownHandlerPGPKeyMissingIs404(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/pgp-key.asc", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no PGP key exists", rec.Code)
	}
}

// TestWellKnownHandlerPGPKeyServedWhenPresent proves the key is served
// once the generated file exists on disk.
func TestWellKnownHandlerPGPKeyServedWhenPresent(t *testing.T) {
	wd := testWellKnownDeps(t)
	keyPath := filepath.Join(wd.configDir, "security", "pgp.pub.asc")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/pgp-key.asc", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pgp-keys" {
		t.Errorf("Content-Type = %q, want application/pgp-keys", got)
	}
	if !strings.Contains(rec.Body.String(), "BEGIN PGP PUBLIC KEY") {
		t.Errorf("expected the key body, got: %s", rec.Body.String())
	}
}

// TestWellKnownHandlerFileBackedEntry proves an enabled, file-backed entry
// (webfinger) is served from {data_dir}/web/.well-known/.
func TestWellKnownHandlerFileBackedEntry(t *testing.T) {
	wd := testWellKnownDeps(t)
	wd.cfg.Web.WellKnown.Webfinger.Enabled = true

	dir := filepath.Join(wd.dataDir, "web", ".well-known")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "webfinger"), []byte(`{"subject":"acct:test@example.com"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/jrd+json" {
		t.Errorf("Content-Type = %q, want application/jrd+json", got)
	}
	if !strings.Contains(rec.Body.String(), "acct:test@example.com") {
		t.Errorf("expected the file body, got: %s", rec.Body.String())
	}
}

// TestWellKnownHandlerFileBackedEntryMissingFileIs404 proves an enabled
// entry with no file on disk still 404s cleanly.
func TestWellKnownHandlerFileBackedEntryMissingFileIs404(t *testing.T) {
	wd := testWellKnownDeps(t)
	wd.cfg.Web.WellKnown.Webfinger.Enabled = true

	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger", nil)
	rec := httptest.NewRecorder()
	wd.wellKnownHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when the backing file is missing", rec.Code)
	}
}

// TestRobotsHandler proves the generated body reflects the configured
// allow/deny lists and includes a Sitemap line.
func TestRobotsHandler(t *testing.T) {
	wd := testWellKnownDeps(t)
	wd.cfg.Web.Robots.Allow = []string{"/", "/api"}
	wd.cfg.Web.Robots.Deny = []string{"/admin"}

	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	wd.robotsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Allow: /api") || !strings.Contains(body, "Disallow: /admin") {
		t.Errorf("expected allow/deny lines, got: %s", body)
	}
	if !strings.Contains(body, "Sitemap: http://example.com/sitemap.xml") {
		t.Errorf("expected a Sitemap line, got: %s", body)
	}
}

// TestRobotsHandlerMethodNotAllowed proves /robots.txt enforces the same
// GET/HEAD-only rule as the rest of the well-known namespace.
func TestRobotsHandlerMethodNotAllowed(t *testing.T) {
	wd := testWellKnownDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/robots.txt", nil)
	rec := httptest.NewRecorder()
	wd.robotsHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// TestLlmsHandlerDisabledIs404 proves /llms.txt 404s when the feature is
// off, and TestLlmsHandlerEnabled proves the body once it is on.
func TestLlmsHandlerDisabledIs404(t *testing.T) {
	wd := testWellKnownDeps(t)
	wd.cfg.Web.LLMs.Enabled = false

	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	rec := httptest.NewRecorder()
	wd.llmsHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when llms.txt is disabled", rec.Code)
	}
}

func TestLlmsHandlerEnabled(t *testing.T) {
	wd := testWellKnownDeps(t)
	wd.cfg.Web.LLMs.Enabled = true
	wd.cfg.Web.LLMs.IncludeEndpoints = true
	wd.cfg.Server.Compliance.GDPR = true
	wd.cfg.Server.GeoIP.Enabled = true

	req := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	wd.llmsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"## API", "## Endpoints", "GET /server/healthz", "## Capabilities", "GeoIP", "Compliance mode: gdpr"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected llms.txt to contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "/server/metrics") {
		t.Error("metrics endpoints must never be advertised in llms.txt")
	}
}

// TestLlmsHandlerMethodNotAllowed covers the shared method gate.
func TestLlmsHandlerMethodNotAllowed(t *testing.T) {
	wd := testWellKnownDeps(t)
	wd.cfg.Web.LLMs.Enabled = true
	req := httptest.NewRequest(http.MethodPut, "/llms.txt", nil)
	rec := httptest.NewRecorder()
	wd.llmsHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
