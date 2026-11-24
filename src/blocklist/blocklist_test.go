package blocklist

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
)

func TestParseIPFeedSkipsUnsafeRanges(t *testing.T) {
	got, skipped, err := parseFeed("# comment\n203.0.113.4\n198.51.100.0/24 note\n10.0.0.0/8\ninvalid\n", "ip")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || skipped != 2 {
		t.Fatalf("entries=%d skipped=%d, want 2 and 2", len(got), skipped)
	}
}

func TestParseIPFeedSkipsConfiguredTrustedProxyNetworks(t *testing.T) {
	got, skipped, err := parseFeed("198.51.100.0/24\n203.0.113.0/24\n", "ip", "198.51.100.12")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || skipped != 1 {
		t.Fatalf("entries=%d skipped=%d, want 1 and 1", len(got), skipped)
	}
}

func TestDomainNormalizationAndWildcardMatching(t *testing.T) {
	feed, skipped, err := parseFeed("example.com\n*.bad.example\n*.com\n192.0.2.1\nok.example/path\n", "domain")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 3 {
		t.Fatalf("skipped = %d, want 3", skipped)
	}
	m := &Manager{sources: []Source{{ID: "domains", Type: "domain"}}, data: snapshot{entries: map[string][]entry{"domains": feed}}}
	if got := m.MatchDomain("www.bad.example"); got != "domains" {
		t.Fatalf("subdomain match = %q", got)
	}
	if got := m.MatchDomain("bad.example"); got != "" {
		t.Fatalf("wildcard matched apex: %q", got)
	}
	if got := m.MatchDomain("example.com."); got != "domains" {
		t.Fatalf("normalized exact match = %q", got)
	}
}

func TestIPMatchingAndFreshnessStatus(t *testing.T) {
	_, network, _ := net.ParseCIDR("198.51.100.0/24")
	now := time.Now().UTC().Add(-maxAge - time.Minute)
	m := &Manager{sources: []Source{{ID: "feed", Type: "ip"}}, data: snapshot{
		entries: map[string][]entry{"feed": {{network: network}}},
		stats:   map[string]Stats{"feed": {Loaded: 1, Updated: now, Status: "ok"}},
	}}
	if got := m.MatchIP("198.51.100.25"); got != "feed" {
		t.Fatalf("IP match = %q", got)
	}
	if got := m.MatchIP("203.0.113.1"); got != "" {
		t.Fatalf("unexpected IP match = %q", got)
	}
	if got := m.Status()["feed"].Status; got != "degraded" {
		t.Fatalf("status = %q, want degraded", got)
	}
}

func TestOpenRetainsBuiltinWhenExtraSourcesDisabled(t *testing.T) {
	falseValue := false
	m, err := Open(t.TempDir(), config.Blocklists{Enabled: true, ExtraSources: []config.BlocklistSource{{ID: "off", Type: "ip", URL: "https://example.test/feed", Enabled: &falseValue}}})
	if err != nil {
		t.Fatal(err)
	}
	sources := m.Sources()
	if len(sources) != 1 || sources[0].ID != "firehol_level1" {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestUpdateAndMatchRequest(t *testing.T) {
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte("blocked.example\n"))
		}
	}))
	defer server.Close()
	m, err := Open(t.TempDir(), config.Blocklists{})
	if err != nil {
		t.Fatal(err)
	}
	m.sources = []Source{{ID: "domains", Type: "domain", URL: server.URL, Enabled: true}}
	if err := m.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	bodyText := "email=user%40blocked.example&url=https%3A%2F%2Fok.example"
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(bodyText))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got, err := m.MatchRequest(req); err != nil || got != "domains" {
		t.Fatalf("MatchRequest() = %q, %v", got, err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil || string(body) != bodyText {
		t.Fatalf("request body was not restored: %q, %v", body, err)
	}
	status = http.StatusInternalServerError
	if err := m.Update(context.Background()); err == nil {
		t.Fatal("Update() succeeded after feed server error")
	}
	if got := m.MatchDomain("blocked.example"); got != "domains" {
		t.Fatalf("last known good feed lost after failure: %q", got)
	}
}
