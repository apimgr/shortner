package geoip

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

func allDBs() config.GeoIPDatabases {
	return config.GeoIPDatabases{ASN: true, Country: true, City: true}
}

func TestOpenMissingFilesFailsOpen(t *testing.T) {
	dir := t.TempDir()
	m := Open(dir, true, allDBs())
	defer m.Close()

	ip := net.ParseIP("203.0.113.1")
	result := m.Lookup(ip)
	if result != (Result{}) {
		t.Errorf("Lookup() with no databases on disk = %+v, want zero Result", result)
	}
}

func TestOpenDisabledNeverLooksUp(t *testing.T) {
	dir := t.TempDir()
	m := Open(dir, false, allDBs())
	defer m.Close()

	if got := m.Lookup(net.ParseIP("8.8.8.8")); got != (Result{}) {
		t.Errorf("disabled Manager.Lookup() = %+v, want zero Result", got)
	}
}

func TestLookupSkipsPrivateAndLoopback(t *testing.T) {
	dir := t.TempDir()
	m := Open(dir, true, allDBs())
	defer m.Close()

	for _, addr := range []string{"127.0.0.1", "10.0.0.5", "192.168.1.1", "172.16.0.1", "::1", "fc00::1"} {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("net.ParseIP(%q) = nil", addr)
		}
		if got := m.Lookup(ip); got != (Result{}) {
			t.Errorf("Lookup(%s) = %+v, want zero Result (private/loopback must never be looked up)", addr, got)
		}
	}
}

func TestLookupNilIP(t *testing.T) {
	dir := t.TempDir()
	m := Open(dir, true, allDBs())
	defer m.Close()

	if got := m.Lookup(nil); got != (Result{}) {
		t.Errorf("Lookup(nil) = %+v, want zero Result", got)
	}
}

func TestReloadAndClose(t *testing.T) {
	dir := t.TempDir()
	m := Open(dir, true, allDBs())
	m.Reload()
	if err := m.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	// Reload on a disabled manager is a no-op, not a panic.
	disabled := Open(dir, false, allDBs())
	disabled.Reload()
	_ = disabled.Close()
}

func TestIsBlocked(t *testing.T) {
	tests := []struct {
		name    string
		country string
		deny    []string
		allow   []string
		want    bool
	}{
		{"both empty allows everything", "CN", nil, nil, false},
		{"deny list blocks match", "CN", []string{"CN", "RU"}, nil, true},
		{"deny list allows non-match", "US", []string{"CN", "RU"}, nil, false},
		{"allow list allows match", "US", nil, []string{"US", "CA"}, false},
		{"allow list blocks non-match", "CN", nil, []string{"US", "CA"}, true},
		{"allow wins when both set", "CN", []string{"US"}, []string{"CN"}, false},
		{"allow wins when both set and blocks", "RU", []string{"US"}, []string{"CN"}, true},
		{"case insensitive", "cn", []string{"CN"}, nil, true},
		{"empty country code never blocked", "", []string{"CN"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBlocked(tt.country, tt.deny, tt.allow); got != tt.want {
				t.Errorf("IsBlocked(%q, %v, %v) = %v, want %v", tt.country, tt.deny, tt.allow, got, tt.want)
			}
		})
	}
}

// TestDownload spins up a local HTTP server, points downloadURLs at it, and
// verifies Download writes valid-looking files and rejects bad ones. This
// mutates the package-level downloadURLs map, so it cannot run in parallel
// with other tests that rely on the real CDN URLs (none do).
func TestDownload(t *testing.T) {
	// A minimal valid MMDB has a specific binary trailer format; rather than
	// hand-construct one, verify Download's plumbing (HTTP fetch, atomic
	// rename, error propagation) using the sanity-check failure path, which
	// exercises every line except the final happy-path rename.
	mux := http.NewServeMux()
	mux.HandleFunc("/ok-but-not-mmdb", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not a real mmdb file"))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	origURLs := downloadURLs
	defer func() { downloadURLs = origURLs }()

	t.Run("invalid mmdb content is rejected and not installed", func(t *testing.T) {
		downloadURLs = map[string]string{asnFile: srv.URL + "/ok-but-not-mmdb"}
		dir := t.TempDir()
		err := Download(context.Background(), dir, config.GeoIPDatabases{ASN: true})
		if err == nil {
			t.Fatal("Download() error = nil, want an error for invalid MMDB content")
		}
		if _, statErr := os.Stat(filepath.Join(dir, asnFile)); statErr == nil {
			t.Error("invalid download was installed into dir; want it left absent")
		}
	})

	t.Run("404 propagates as an error", func(t *testing.T) {
		downloadURLs = map[string]string{countryFile: srv.URL + "/missing"}
		dir := t.TempDir()
		err := Download(context.Background(), dir, config.GeoIPDatabases{Country: true})
		if err == nil {
			t.Fatal("Download() error = nil, want an error for 404 response")
		}
	})

	t.Run("no databases enabled is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		if err := Download(context.Background(), dir, config.GeoIPDatabases{}); err != nil {
			t.Errorf("Download() with nothing enabled error = %v, want nil", err)
		}
	})

	t.Run("unknown filename has no URL", func(t *testing.T) {
		downloadURLs = map[string]string{}
		dir := t.TempDir()
		err := Download(context.Background(), dir, config.GeoIPDatabases{ASN: true})
		if err == nil {
			t.Fatal("Download() error = nil, want an error for a filename with no configured URL")
		}
	})
}
