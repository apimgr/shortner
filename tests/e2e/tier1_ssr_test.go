//go:build e2e

package e2e

import (
	"html"
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

// internalLinkPattern extracts navigable links for the deterministic route crawl.
var internalLinkPattern = regexp.MustCompile(`(?i)href="([^"]+)"`)

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

	for _, path := range []string{
		"/", "/list", "/server/about", "/server/privacy", "/server/help", "/server/terms",
		"/server/help/mcp", "/server/contact", "/server/ccpa", "/server/security", "/server/security/policy",
		"/server/security/thanks", "/server/dpo", "/server/healthz", "/server/preferences",
		"/server/preferences/tokens", "/server/preferences/sync", "/server/docs/swagger", "/server/docs/graphql",
	} {
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
		if !strings.Contains(body, "<title>") || !regexp.MustCompile(`<html[^>]+lang="[a-z]{2}`).MatchString(body) ||
			!strings.Contains(body, `charset="utf-8"`) || !strings.Contains(body, `name="viewport"`) {
			t.Errorf("GET %s is missing title, language, UTF-8 charset, or viewport metadata", path)
		}
	}
	swaggerResp, swaggerBody := get(t, client, "/server/docs/swagger")
	if swaggerResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /server/docs/swagger = %d, want 200", swaggerResp.StatusCode)
	}
	for _, operation := range []string{"GET /api/{api_version}/links", "POST /api/{api_version}/links", "GET /metrics"} {
		if !strings.Contains(swaggerBody, operation) {
			t.Errorf("server-rendered Swagger inventory omitted %q", operation)
		}
	}
	graphqlResp, graphqlBody := get(t, client, "/server/docs/graphql")
	if graphqlResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /server/docs/graphql = %d, want 200", graphqlResp.StatusCode)
	}
	for _, field := range []string{"linkStats", "getAPIHealth", `name="variables"`} {
		if !strings.Contains(graphqlBody, field) {
			t.Errorf("server-rendered GraphQL docs omitted %q", field)
		}
	}
	serverResp, _ := get(t, client, "/server")
	if serverResp.StatusCode != http.StatusMovedPermanently || serverResp.Header.Get("Location") != "/server/about" {
		t.Errorf("GET /server = %d Location %q, want 301 /server/about", serverResp.StatusCode, serverResp.Header.Get("Location"))
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

func TestDebug500PageUsesThemedRenderer(t *testing.T) {
	resp, body := getWithOperator(t, "/debug/error500")
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /debug/error500 = %d %q, want themed HTML 500", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "class=\"page-title\"") || strings.Contains(body, "goroutine ") {
		t.Fatalf("500 body is not the normal themed error page")
	}
}

// TestTier1FullInternalLinkCrawl follows every same-origin href reachable
// from the homepage and rejects dead routes, server errors, and broken redirects.
func TestTier1FullInternalLinkCrawl(t *testing.T) {
	client := newClient(t)
	base, err := url.Parse(httpBaseURL)
	if err != nil {
		t.Fatalf("parse test base URL: %v", err)
	}
	queue := []string{"/"}
	seen := make(map[string]struct{})
	for len(queue) > 0 {
		if len(seen) >= 500 {
			t.Fatal("internal link crawl exceeded 500 unique routes")
		}
		path := queue[0]
		queue = queue[1:]
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		resp, body := get(t, client, path)
		if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
			location := resp.Header.Get("Location")
			if location == "" {
				t.Errorf("internal route %s returned redirect %d without Location", path, resp.StatusCode)
				continue
			}
			target, parseErr := base.Parse(location)
			if parseErr != nil || target.Scheme != "http" && target.Scheme != "https" || target.Host == "" {
				t.Errorf("internal route %s redirected to an invalid URL: %q", path, location)
				continue
			}
			if target.Host != base.Host {
				continue
			}
			queue = append(queue, target.RequestURI())
			continue
		}
		if resp.StatusCode == http.StatusGone {
			// Expired resources remain valid routes with the specified 410 contract.
			continue
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("internal route %s returned %d", path, resp.StatusCode)
			continue
		}
		for _, match := range internalLinkPattern.FindAllStringSubmatch(body, -1) {
			resolved, parseErr := base.Parse(html.UnescapeString(match[1]))
			if parseErr != nil || resolved.Host != base.Host || (resolved.Scheme != "http" && resolved.Scheme != "https") {
				continue
			}
			resolved.Fragment = ""
			requestPath := resolved.RequestURI()
			if requestPath == "" {
				requestPath = "/"
			}
			if _, ok := seen[requestPath]; !ok {
				queue = append(queue, requestPath)
			}
		}
	}
	if len(seen) < 10 {
		t.Errorf("internal link crawl visited only %d routes", len(seen))
	}
}
