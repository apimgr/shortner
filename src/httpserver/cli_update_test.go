package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/config"
)

func TestCLIAutodiscoverListsAvailableBinariesAndMinimum(t *testing.T) {
	dir := t.TempDir()
	data := []byte("cli binary contents")
	if err := os.WriteFile(filepath.Join(dir, "shortner-cli-linux-amd64"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("")
	cfg.Server.Update.CLIMinVersion = "1.2.0"
	d := &cliUpdateDeps{cfg: cfg, version: "1.3.0", binaryDir: dir}
	rec := httptest.NewRecorder()
	d.autodiscoverHandler(rec, httptest.NewRequest(http.MethodGet, "/api/autodiscover", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		OK   bool                    `json:"ok"`
		Data cliAutodiscoverDocument `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatalf("response envelope not successful: %s", rec.Body.String())
	}
	doc := envelope.Data
	sum := sha256.Sum256(data)
	if doc.CLIMinVersion != "1.2.0" {
		t.Errorf("cli_min_version = %q", doc.CLIMinVersion)
	}
	if got := doc.CLIVersions["linux-amd64"]; got.Version != "1.3.0" || got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("linux-amd64 = %+v", got)
	}
	if len(doc.CLIVersions) != 1 {
		t.Errorf("listed %d binaries, want only the available binary", len(doc.CLIVersions))
	}
}

func TestCLIAutodiscoverPublishesOnlyExplicitlyExposedOverlayAddresses(t *testing.T) {
	cfg := config.Default("")
	tor := &fakeTorReporter{enabled: true, running: true, healthy: true, address: "hidden.onion"}
	i2p := &fakeI2PReporter{enabled: true, running: true, healthy: true, address: "hidden.b32.i2p"}
	d := &cliUpdateDeps{cfg: cfg, version: "1.0.0", resolver: NewProxyResolver(nil), tor: tor, i2p: i2p}
	read := func() cliAutodiscoverDocument {
		rec := httptest.NewRecorder()
		d.autodiscoverHandler(rec, httptest.NewRequest(http.MethodGet, "/api/autodiscover", nil))
		var envelope struct {
			OK   bool                    `json:"ok"`
			Data cliAutodiscoverDocument `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if !envelope.OK {
			t.Fatalf("response not successful: %s", rec.Body.String())
		}
		return envelope.Data
	}
	if doc := read(); doc.Onion != "" || doc.I2P != "" {
		t.Fatalf("unexposed addresses published: %+v", doc)
	}
	cfg.Server.Tor.Expose, cfg.Server.I2P.Expose = true, true
	if doc := read(); doc.Onion != tor.address || doc.I2P != i2p.address {
		t.Fatalf("exposed addresses = %q/%q", doc.Onion, doc.I2P)
	}
}

func TestCLIBinaryHandlerServesOnlyAvailablePlatformAssets(t *testing.T) {
	dir := t.TempDir()
	windowsBinary := []byte("windows cli")
	if err := os.WriteFile(filepath.Join(dir, "shortner-cli-windows-amd64.exe"), windowsBinary, 0o755); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Handle("/cli/binaries/{name}", http.HandlerFunc((&cliUpdateDeps{binaryDir: dir}).binaryHandler))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cli/binaries/shortner-cli-windows-amd64", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != string(windowsBinary) {
		t.Fatalf("status/body = %d/%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="shortner-cli-windows-amd64.exe"` {
		t.Errorf("Content-Disposition = %q", got)
	}

	for _, target := range []string{
		"/cli/binaries/shortner-cli-linux-amd64",
		"/cli/binaries/shortner-cli-linux-amd64.exe",
		"/cli/binaries/../../server.yml",
	} {
		missing := httptest.NewRecorder()
		router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, target, nil))
		if missing.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", target, missing.Code)
		}
	}
}

func TestCLIBinaryHandlerSupportsHeadAndRejectsMethods(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shortner-cli-linux-amd64"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Handle("/cli/binaries/{name}", http.HandlerFunc((&cliUpdateDeps{binaryDir: dir}).binaryHandler))

	head := httptest.NewRecorder()
	router.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/cli/binaries/shortner-cli-linux-amd64", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD status/body = %d/%d", head.Code, head.Body.Len())
	}
	post := httptest.NewRecorder()
	router.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/cli/binaries/shortner-cli-linux-amd64", nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST status/Allow = %d/%q", post.Code, post.Header().Get("Allow"))
	}
}
