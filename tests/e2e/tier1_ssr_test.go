//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// csrfPattern finds the hidden CSRF field rendered into every form.
var csrfPattern = regexp.MustCompile(`name="csrf_token"[^>]*value="([^"]*)"`)

// shortURLPattern finds the created short URL on the success card.
var shortURLPattern = regexp.MustCompile(`<a href="(https?://[^"]+/[A-Za-z0-9-]{3,20})"`)

// newClient returns an HTTP client with a cookie jar and redirects
// disabled, so redirect status codes can be asserted directly.
func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func get(t *testing.T, client *http.Client, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, httpBaseURL+path, nil)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return resp, string(body)
}

// TestTier1ServerSideRendering is PART 28's Tier 1: the pages must be
// complete and usable with no browser at all — plain net/http, no
// JavaScript engine involved.
func TestTier1ServerSideRendering(t *testing.T) {
	client := newClient(t)

	for _, path := range []string{"/", "/server", "/server/about", "/server/healthz", "/list"} {
		resp, body := get(t, client, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, "<!DOCTYPE") {
			t.Errorf("GET %s did not return a complete HTML document", path)
		}
		if strings.Contains(body, "{{") {
			t.Errorf("GET %s leaked an unrendered template action", path)
		}
	}

	_, body := get(t, client, "/")
	if !strings.Contains(body, `name="url"`) {
		t.Fatalf("home page has no shorten form")
	}

	match := csrfPattern.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("home page rendered no csrf_token field")
	}

	form := url.Values{}
	form.Set("csrf_token", match[1])
	form.Set("url", "https://example.com/tier1")
	req, err := http.NewRequest(http.MethodPost, httpBaseURL+"/", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("building POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")
	postResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /: %v", err)
	}
	defer postResp.Body.Close()
	raw, err := io.ReadAll(postResp.Body)
	if err != nil {
		t.Fatalf("reading POST body: %v", err)
	}
	postBody := string(raw)
	if postResp.StatusCode != http.StatusOK && postResp.StatusCode != http.StatusCreated {
		saveArtifacts(t, "tier1-create", postBody, nil)
		t.Fatalf("POST / = %d, want 200 or 201", postResp.StatusCode)
	}
	if !strings.Contains(postBody, "Success!") {
		saveArtifacts(t, "tier1-create", postBody, nil)
		t.Fatalf("POST / did not render the success card")
	}

	found := shortURLPattern.FindStringSubmatch(postBody)
	if found == nil {
		saveArtifacts(t, "tier1-create", postBody, nil)
		t.Fatalf("success card contained no short URL")
	}
	slug := found[1][strings.LastIndex(found[1], "/")+1:]

	redirectResp, _ := get(t, client, "/"+slug)
	if redirectResp.StatusCode != http.StatusFound {
		t.Fatalf("GET /%s = %d, want 302", slug, redirectResp.StatusCode)
	}
	if loc := redirectResp.Header.Get("Location"); loc != "https://example.com/tier1" {
		t.Errorf("Location = %q, want https://example.com/tier1", loc)
	}

	statsResp, statsBody := get(t, client, "/"+slug+"/stats")
	if statsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /%s/stats = %d, want 200", slug, statsResp.StatusCode)
	}
	if !strings.Contains(statsBody, "<!DOCTYPE") {
		t.Errorf("stats page is not a complete HTML document")
	}
}
