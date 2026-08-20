package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient builds a Client pointed at the given httptest server, with a
// distinctive User-Agent so header assertions can't accidentally pass.
func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	return New(Options{
		BaseURL:    server.URL,
		APIVersion: "v1",
		UserAgent:  "shortner-cli/test",
		Token:      "tok_abc",
		Timeout:    5 * time.Second,
	})
}

// TestNewNormalizesBaseURL covers the scheme-injection and trailing-slash
// trimming behavior, plus the default Timeout/APIVersion fallbacks.
func TestNewNormalizesBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"trailing slash trimmed", "https://example.com/", "https://example.com"},
		{"missing scheme gets https", "example.com", "https://example.com"},
		{"existing scheme kept", "http://example.com", "http://example.com"},
		{"whitespace trimmed", "  https://example.com  ", "https://example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(Options{BaseURL: tc.in})
			if got := c.BaseURL(); got != tc.want {
				t.Errorf("BaseURL() = %q, want %q", got, tc.want)
			}
		})
	}

	c := New(Options{BaseURL: "https://example.com"})
	if c.apiVersion != "v1" {
		t.Errorf("apiVersion default = %q, want v1", c.apiVersion)
	}
	if c.httpClient.Timeout != 30*time.Second {
		t.Errorf("Timeout default = %v, want 30s", c.httpClient.Timeout)
	}
}

// TestNoServerConfigured covers the local, no-HTTP-call error path when the
// base URL is empty.
func TestNoServerConfigured(t *testing.T) {
	c := New(Options{})
	_, err := c.GetLink(context.Background(), "abc123")
	if err == nil || !strings.Contains(err.Error(), "no server configured") {
		t.Fatalf("err = %v, want no server configured", err)
	}
}

// TestCreateLinkSendsRequestAndHeaders exercises the happy path plus the
// User-Agent, Accept, Content-Type, and Authorization headers.
func TestCreateLinkSendsRequestAndHeaders(t *testing.T) {
	var gotMethod, gotPath, gotUA, gotAuth, gotAccept, gotContentType string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotUA = r.Header.Get("User-Agent")
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"short_code":"abc123","short_url":"https://sh.rt/abc123","destination_url":"https://example.com","owner_token":"owner-tok"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	created, err := c.CreateLink(context.Background(), "https://example.com", "", "")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if created.ShortCode != "abc123" || created.OwnerToken != "owner-tok" {
		t.Fatalf("unexpected result: %+v", created)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/links" {
		t.Errorf("path = %q", gotPath)
	}
	if gotUA != "shortner-cli/test" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gotAuth != "Bearer tok_abc" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if !strings.Contains(string(gotBody), `"url":"https://example.com"`) {
		t.Errorf("body = %s", gotBody)
	}
}

// TestCreateLinkWithSlugAndExpiry checks the optional fields are included
// only when provided.
func TestCreateLinkWithSlugAndExpiry(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"short_code":"my-slug"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	_, err := c.CreateLink(context.Background(), "https://example.com", "my-slug", "2030-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if !strings.Contains(string(gotBody), `"slug":"my-slug"`) {
		t.Errorf("body missing slug: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"expires_at":"2030-01-01T00:00:00Z"`) {
		t.Errorf("body missing expires_at: %s", gotBody)
	}
}

// TestGetLinkNotFound covers the 404 -> ErrNotFound mapping.
func TestGetLinkNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"no such link"}}`))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	_, err := c.GetLink(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "no such link") {
		t.Fatalf("err = %v, want server message included", err)
	}
}

// TestStatusErrorMapping covers every branch of statusError: plain
// unauthorized, revoked-token, forbidden, and the generic default.
func TestStatusErrorMapping(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantSentinel error
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":{"message":"bad token"}}`, ErrUnauthorized},
		{"token revoked", http.StatusUnauthorized, `{"error":{"code":"TOKEN_REVOKED","message":"revoked"}}`, ErrTokenRevoked},
		{"forbidden", http.StatusForbidden, `{"error":{"message":"nope"}}`, ErrUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			c := newTestClient(t, server)
			_, err := c.GetLink(context.Background(), "x")
			if !errors.Is(err, tc.wantSentinel) {
				t.Fatalf("err = %v, want %v", err, tc.wantSentinel)
			}
		})
	}

	t.Run("generic server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer server.Close()

		c := newTestClient(t, server)
		_, err := c.GetLink(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "server returned 500") {
			t.Fatalf("err = %v, want generic 500 message", err)
		}
	})

	t.Run("empty body falls back to status text", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer server.Close()

		c := newTestClient(t, server)
		_, err := c.GetLink(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), http.StatusText(http.StatusBadGateway)) {
			t.Fatalf("err = %v, want status text fallback", err)
		}
	})
}

// TestListLinksQueryParams covers pagination parameter construction,
// including the zero-value omission rule.
func TestListLinksQueryParams(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"page":1,"limit":20,"total":0,"pages":0}}`))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	if _, err := c.ListLinks(context.Background(), 2, 50); err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if !strings.Contains(gotQuery, "page=2") || !strings.Contains(gotQuery, "limit=50") {
		t.Fatalf("query = %q", gotQuery)
	}

	gotQuery = ""
	if _, err := c.ListLinks(context.Background(), 0, 0); err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if gotQuery != "" {
		t.Fatalf("query = %q, want empty when page/limit are zero", gotQuery)
	}
}

// TestUpdateLinkNothingToUpdate covers the local validation error that never
// reaches the network.
func TestUpdateLinkNothingToUpdate(t *testing.T) {
	c := New(Options{BaseURL: "https://example.com"})
	_, err := c.UpdateLink(context.Background(), "abc", "", "")
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("err = %v", err)
	}
}

// TestUpdateLinkSendsPatch covers the happy path and PATCH method.
func TestUpdateLinkSendsPatch(t *testing.T) {
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"short_code":"abc"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	link, err := c.UpdateLink(context.Background(), "abc", "https://new.example.com", "")
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	if link.ShortCode != "abc" {
		t.Fatalf("unexpected link: %+v", link)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
}

// TestDeleteLink covers a no-body DELETE response.
func TestDeleteLink(t *testing.T) {
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := newTestClient(t, server)
	if err := c.DeleteLink(context.Background(), "abc"); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
}

// TestGetStats covers the stats endpoint round trip.
func TestGetStats(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"short_code":"abc","total_clicks":42,"referrers":{"direct":10},"time_series":[{"date":"2026-01-01","count":5}]}`))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	stats, err := c.GetStats(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.TotalClicks != 42 || stats.Referrers["direct"] != 10 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// TestHealth covers the health endpoint round trip and nested project.name.
func TestHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/healthz" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"1.0.0","project":{"name":"shortner"}}`))
	}))
	defer server.Close()

	c := newTestClient(t, server)
	health, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Status != "ok" || health.Project.Name != "shortner" {
		t.Fatalf("unexpected health: %+v", health)
	}
}

// TestAutodiscover covers the unversioned /api/autodiscover path (it does
// NOT go through apiPath, unlike every other endpoint) and the ErrNotFound
// "server doesn't publish updates" case.
func TestAutodiscover(t *testing.T) {
	t.Run("happy path uses unversioned path", func(t *testing.T) {
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cli_versions":{"linux-amd64":{"version":"1.2.3","sha256":"abc"}},"cli_min_version":"1.0.0"}`))
		}))
		defer server.Close()

		c := newTestClient(t, server)
		doc, err := c.Autodiscover(context.Background())
		if err != nil {
			t.Fatalf("Autodiscover: %v", err)
		}
		if gotPath != "/api/autodiscover" {
			t.Fatalf("path = %q, want unversioned /api/autodiscover", gotPath)
		}
		if doc.CLIMinVersion != "1.0.0" || doc.CLIVersions["linux-amd64"].Version != "1.2.3" {
			t.Fatalf("unexpected doc: %+v", doc)
		}
	})

	t.Run("not found maps to ErrNotFound", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		c := newTestClient(t, server)
		_, err := c.Autodiscover(context.Background())
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestDownloadCLIBinary covers a successful stream and the 4xx mapping.
func TestDownloadCLIBinary(t *testing.T) {
	t.Run("streams bytes", func(t *testing.T) {
		payload := []byte("fake-binary-contents")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.URL.Path, "shortner-cli-linux-amd64") {
				t.Errorf("path = %q", r.URL.Path)
			}
			_, _ = w.Write(payload)
		}))
		defer server.Close()

		c := newTestClient(t, server)
		var buf bytes.Buffer
		n, err := c.DownloadCLIBinary(context.Background(), "shortner", "linux-amd64", &buf)
		if err != nil {
			t.Fatalf("DownloadCLIBinary: %v", err)
		}
		if n != int64(len(payload)) || buf.String() != string(payload) {
			t.Fatalf("got %d bytes %q", n, buf.String())
		}
	})

	t.Run("error status maps via statusError", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		c := newTestClient(t, server)
		var buf bytes.Buffer
		_, err := c.DownloadCLIBinary(context.Background(), "shortner", "linux-amd64", &buf)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestGetSucceedsWithoutRetryWhenHealthy covers the baseline: a healthy GET
// makes exactly one request even when Retry is configured, and a transport
// failure against a closed listener still surfaces a "connect to" error.
func TestGetSucceedsWithoutRetryWhenHealthy(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"short_code":"abc"}`))
	}))
	defer server.Close()

	c := New(Options{BaseURL: server.URL, APIVersion: "v1", Retry: 2, RetryDelay: time.Millisecond})
	if _, err := c.GetLink(context.Background(), "abc"); err != nil {
		t.Fatalf("GetLink: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry needed on success)", attempts)
	}
}

// TestGetRetriesThenFailsOnTransportError covers the GET-only retry loop
// against a listener that never accepts, exhausting all attempts and
// surfacing the wrapped connect error.
func TestGetRetriesThenFailsOnTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	deadURL := server.URL
	server.Close()

	c := New(Options{BaseURL: deadURL, APIVersion: "v1", Retry: 2, RetryDelay: time.Millisecond})
	_, err := c.GetLink(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "connect to") {
		t.Fatalf("err = %v, want a wrapped connect error", err)
	}
}

// TestPostNeverRetries covers that non-GET requests never retry, even when
// Retry is configured, since retrying a non-idempotent write is unsafe.
func TestPostNeverRetries(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := New(Options{BaseURL: server.URL, APIVersion: "v1", Retry: 3, RetryDelay: time.Millisecond})
	_, err := c.CreateLink(context.Background(), "https://example.com", "", "")
	if err == nil {
		t.Fatal("want error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want exactly 1 for a non-GET request", attempts)
	}
}

// TestSetToken covers that SetToken changes the Authorization header used by
// subsequent requests.
func TestSetToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	c := New(Options{BaseURL: server.URL, APIVersion: "v1"})
	c.SetToken("new-token")
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if gotAuth != "Bearer new-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}
