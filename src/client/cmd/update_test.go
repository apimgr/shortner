package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/client/api"
	clientconfig "github.com/apimgr/shortner/src/client/config"
	"github.com/apimgr/shortner/src/client/paths"
	"github.com/apimgr/shortner/src/common/version"
)

// TestVersionParts covers v-prefix stripping, pre-release/build-metadata
// cutting, and the malformed-input failure case.
func TestVersionParts(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  [3]int
		ok    bool
	}{
		{"plain", "1.2.3", [3]int{1, 2, 3}, true},
		{"v prefix", "v1.2.3", [3]int{1, 2, 3}, true},
		{"pre-release suffix", "1.2.3-beta.1", [3]int{1, 2, 3}, true},
		{"build metadata suffix", "1.2.3+build5", [3]int{1, 2, 3}, true},
		{"empty", "", [3]int{}, false},
		{"only v", "v", [3]int{}, false},
		{"non-numeric", "a.b.c", [3]int{}, false},
		{"missing patch defaults to zero", "1.2", [3]int{1, 2, 0}, true},
		{"devel", "devel", [3]int{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := versionParts(tc.value)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCompareVersions covers ordering, equality, the identical-string
// shortcut, and the "unparseable treated as equal" rule that keeps a devel
// build from ever triggering an update.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"equal strings", "1.2.3", "1.2.3", 0},
		{"a less by patch", "1.2.3", "1.2.4", -1},
		{"a greater by patch", "1.2.4", "1.2.3", 1},
		{"a less by minor", "1.2.9", "1.3.0", -1},
		{"a less by major", "1.9.9", "2.0.0", -1},
		{"v prefix mixed", "v1.2.3", "1.2.3", 0},
		{"unparseable a", "devel", "1.2.3", 0},
		{"unparseable b", "1.2.3", "devel", 0},
		{"both unparseable", "devel", "unknown", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compareVersions(tc.a, tc.b); got != tc.want {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestVerifyChecksum covers a matching checksum, a mismatched checksum, and
// the empty-expected-checksum refusal.
func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary")
	content := []byte("this is the downloaded binary content")
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])

	t.Run("matching checksum", func(t *testing.T) {
		if err := verifyChecksum(path, expected); err != nil {
			t.Fatalf("verifyChecksum: %v", err)
		}
	})

	t.Run("matching checksum case-insensitive", func(t *testing.T) {
		upper := ""
		for _, r := range expected {
			upper += string(r)
		}
		if err := verifyChecksum(path, upper); err != nil {
			t.Fatalf("verifyChecksum: %v", err)
		}
	})

	t.Run("mismatched checksum", func(t *testing.T) {
		err := verifyChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000")
		if err == nil {
			t.Fatal("want error for mismatched checksum")
		}
	})

	t.Run("empty expected refuses to install", func(t *testing.T) {
		err := verifyChecksum(path, "")
		if err == nil {
			t.Fatal("want error for empty expected checksum")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		err := verifyChecksum(filepath.Join(dir, "missing"), expected)
		if err == nil {
			t.Fatal("want error for missing file")
		}
	})
}

// TestAutodiscoverCacheRoundTrip covers write-then-read, a server mismatch,
// TTL expiry, and the negative (Missing: true) cache entry.
func TestAutodiscoverCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "autodiscover.json")

	t.Run("missing file is invalid", func(t *testing.T) {
		_, ok := readAutodiscoverCache(path, "https://example.com", time.Hour)
		if ok {
			t.Fatal("want ok = false for a missing cache file")
		}
	})

	t.Run("round trip hit", func(t *testing.T) {
		cached := cachedAutodiscover{
			FetchedAt: time.Now(),
			Server:    "https://example.com",
			Document:  api.Autodiscover{CLIMinVersion: "1.0.0"},
		}
		writeAutodiscoverCache(path, cached)
		got, ok := readAutodiscoverCache(path, "https://example.com", time.Hour)
		if !ok {
			t.Fatal("want ok = true")
		}
		if got.Document.CLIMinVersion != "1.0.0" {
			t.Fatalf("Document.CLIMinVersion = %q", got.Document.CLIMinVersion)
		}
	})

	t.Run("server mismatch invalidates", func(t *testing.T) {
		cached := cachedAutodiscover{
			FetchedAt: time.Now(),
			Server:    "https://example.com",
			Document:  api.Autodiscover{},
		}
		writeAutodiscoverCache(path, cached)
		_, ok := readAutodiscoverCache(path, "https://other.example.com", time.Hour)
		if ok {
			t.Fatal("want ok = false for a server mismatch")
		}
	})

	t.Run("ttl expiry invalidates", func(t *testing.T) {
		cached := cachedAutodiscover{
			FetchedAt: time.Now().Add(-2 * time.Hour),
			Server:    "https://example.com",
			Document:  api.Autodiscover{},
		}
		writeAutodiscoverCache(path, cached)
		_, ok := readAutodiscoverCache(path, "https://example.com", time.Hour)
		if ok {
			t.Fatal("want ok = false for an expired entry")
		}
	})

	t.Run("negative cache entry round trips", func(t *testing.T) {
		cached := cachedAutodiscover{
			FetchedAt: time.Now(),
			Server:    "https://example.com",
			Missing:   true,
		}
		writeAutodiscoverCache(path, cached)
		got, ok := readAutodiscoverCache(path, "https://example.com", time.Hour)
		if !ok {
			t.Fatal("want ok = true")
		}
		if !got.Missing {
			t.Fatal("want Missing = true")
		}
	})

	t.Run("corrupt json is invalid", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, ok := readAutodiscoverCache(path, "https://example.com", time.Hour)
		if ok {
			t.Fatal("want ok = false for corrupt JSON")
		}
	})

	t.Run("write creates missing parent directory", func(t *testing.T) {
		nested := filepath.Join(dir, "nested", "sub", "autodiscover.json")
		writeAutodiscoverCache(nested, cachedAutodiscover{Server: "s", FetchedAt: time.Now()})
		if _, err := os.Stat(nested); err != nil {
			t.Fatalf("expected cache file to exist: %v", err)
		}
	})
}

// newUpdateTestRunner builds a runner suitable for exercising fetchAutodiscover
// and updateGate against an httptest server, with cache isolated to a temp
// directory so nothing touches the real user cache.
func newUpdateTestRunner(t *testing.T, serverURL string, cacheEnabled bool) *runner {
	t.Helper()
	cfg := clientconfig.Default()
	cfg.Server.Primary = serverURL
	cfg.Cache.Enabled = cacheEnabled
	cfg.Cache.TTL = "5m"

	r := &runner{
		binaryName: "shortner-cli",
		cfg:        cfg,
		paths:      paths.Paths{CacheDir: t.TempDir()},
		client: api.New(api.Options{
			BaseURL:    serverURL,
			APIVersion: "v1",
			UserAgent:  "shortner-cli/test",
		}),
		printer: NewPrinter(newDiscardWriter(), newDiscardWriter(), "table", false, false),
	}
	return r
}

// newDiscardWriter returns a writer that discards everything written to it,
// used to keep printer output out of test logs.
func newDiscardWriter() *discardWriter { return &discardWriter{} }

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// TestFetchAutodiscoverCachesResultAndNegativeResult exercises both the
// success path (positive cache written) and the not-found path (negative
// cache written), then confirms a second call is served from cache without
// hitting the network again.
func TestFetchAutodiscoverCachesResultAndNegativeResult(t *testing.T) {
	t.Run("success is cached", func(t *testing.T) {
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hits++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(api.Autodiscover{CLIMinVersion: "1.0.0"})
		}))
		defer srv.Close()

		r := newUpdateTestRunner(t, srv.URL, true)
		doc, err := r.fetchAutodiscover(context.Background(), false)
		if err != nil {
			t.Fatalf("fetchAutodiscover: %v", err)
		}
		if doc.CLIMinVersion != "1.0.0" {
			t.Fatalf("CLIMinVersion = %q", doc.CLIMinVersion)
		}

		if _, err := r.fetchAutodiscover(context.Background(), false); err != nil {
			t.Fatalf("second fetchAutodiscover: %v", err)
		}
		if hits != 1 {
			t.Fatalf("hits = %d, want 1 (second call should be served from cache)", hits)
		}
	})

	t.Run("not found is negatively cached", func(t *testing.T) {
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hits++
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		r := newUpdateTestRunner(t, srv.URL, true)
		if _, err := r.fetchAutodiscover(context.Background(), false); err == nil {
			t.Fatal("want error for a 404 autodiscover response")
		}
		if _, err := r.fetchAutodiscover(context.Background(), false); err == nil {
			t.Fatal("want error on the cached negative result too")
		}
		if hits != 1 {
			t.Fatalf("hits = %d, want 1 (negative result should be cached)", hits)
		}
	})

	t.Run("force bypasses cache", func(t *testing.T) {
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hits++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(api.Autodiscover{})
		}))
		defer srv.Close()

		r := newUpdateTestRunner(t, srv.URL, true)
		if _, err := r.fetchAutodiscover(context.Background(), false); err != nil {
			t.Fatalf("fetchAutodiscover: %v", err)
		}
		if _, err := r.fetchAutodiscover(context.Background(), true); err != nil {
			t.Fatalf("forced fetchAutodiscover: %v", err)
		}
		if hits != 2 {
			t.Fatalf("hits = %d, want 2 (force should bypass cache)", hits)
		}
	})

	t.Run("disabled cache always hits the network", func(t *testing.T) {
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			hits++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(api.Autodiscover{})
		}))
		defer srv.Close()

		r := newUpdateTestRunner(t, srv.URL, false)
		if _, err := r.fetchAutodiscover(context.Background(), false); err != nil {
			t.Fatalf("fetchAutodiscover: %v", err)
		}
		if _, err := r.fetchAutodiscover(context.Background(), false); err != nil {
			t.Fatalf("fetchAutodiscover: %v", err)
		}
		if hits != 2 {
			t.Fatalf("hits = %d, want 2 (cache disabled)", hits)
		}
	})
}

// TestUpdateGate covers the never-check short-circuit, below-cli_min_version
// (returns false), and the up-to-date / newer-available (returns true) paths.
// The compiled version.Version is "devel" by default, which compareVersions
// treats as always-equal (an unparseable version never triggers an update),
// so these subtests pin it to a real semver for the duration of the test.
func TestUpdateGate(t *testing.T) {
	old := version.Version
	version.Version = "1.0.0"
	t.Cleanup(func() { version.Version = old })

	t.Run("never check interval short-circuits", func(t *testing.T) {
		r := newUpdateTestRunner(t, "https://unreachable.invalid.example", true)
		r.cfg.Update.CheckInterval = "never"
		if !r.updateGate(context.Background()) {
			t.Fatal("want true when CheckInterval is never, regardless of reachability")
		}
	})

	t.Run("fetch error never blocks the command", func(t *testing.T) {
		r := newUpdateTestRunner(t, "https://unreachable.invalid.example", false)
		if !r.updateGate(context.Background()) {
			t.Fatal("want true when autodiscover cannot be fetched")
		}
	})

	t.Run("below cli_min_version returns false", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(api.Autodiscover{CLIMinVersion: "999.0.0"})
		}))
		defer srv.Close()

		r := newUpdateTestRunner(t, srv.URL, false)
		if r.updateGate(context.Background()) {
			t.Fatal("want false when the client is below cli_min_version")
		}
	})

	t.Run("up to date returns true", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(api.Autodiscover{CLIMinVersion: "0.0.1"})
		}))
		defer srv.Close()

		r := newUpdateTestRunner(t, srv.URL, false)
		if !r.updateGate(context.Background()) {
			t.Fatal("want true when the client meets cli_min_version")
		}
	})

	t.Run("newer version available warns and returns true when auto is off", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(api.Autodiscover{
				CLIVersions: map[string]api.CLIVersion{
					platformKey(): {Version: "999.0.0", SHA256: "deadbeef"},
				},
			})
		}))
		defer srv.Close()

		r := newUpdateTestRunner(t, srv.URL, false)
		r.cfg.Update.Auto = false
		if !r.updateGate(context.Background()) {
			t.Fatal("want true even with a newer version available and auto off")
		}
	})
}
