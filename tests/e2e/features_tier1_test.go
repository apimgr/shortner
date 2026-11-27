//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestTier1CustomSlugListingAndAnalytics exercises the project's public
// create, resolve, list, and analytics pages through their server-rendered
// routes, with no JavaScript or browser runtime.
func TestTier1CustomSlugListingAndAnalytics(t *testing.T) {
	client := newClient(t)
	_, home := get(t, client, "/")
	csrf := csrfPattern.FindStringSubmatch(home)
	if csrf == nil {
		t.Fatal("home form has no CSRF token")
	}
	csrfCookiePresent := false
	csrfCookieMatches := false
	baseURL, err := url.Parse(httpBaseURL)
	if err != nil {
		t.Fatalf("parse test base URL: %v", err)
	}
	for _, cookie := range client.Jar.Cookies(baseURL) {
		if cookie.Name == "csrf_token" {
			csrfCookiePresent = true
			csrfCookieMatches = cookie.Value == csrf[1]
		}
	}
	if !csrfCookiePresent || !csrfCookieMatches {
		t.Fatalf("CSRF setup mismatch: cookie_present=%t form_matches_cookie=%t", csrfCookiePresent, csrfCookieMatches)
	}

	const slug = "tier1feature"
	response := postForm(t, client, "/", url.Values{
		"csrf_token": {csrf[1]},
		"url":        {"https://example.com/tier1-feature"},
		"slug":       {slug},
	})
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read create response: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST / custom slug = %d, want 201: %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "/"+slug) || !strings.Contains(string(body), "Success!") {
		t.Fatalf("custom slug success page does not show the created URL: %s", body)
	}

	redirect, _ := get(t, client, "/"+slug)
	if redirect.StatusCode != http.StatusFound || redirect.Header.Get("Location") != "https://example.com/tier1-feature" {
		t.Fatalf("GET /%s = %d Location %q", slug, redirect.StatusCode, redirect.Header.Get("Location"))
	}

	list, listBody := get(t, client, "/list")
	if list.StatusCode != http.StatusOK || !strings.Contains(listBody, slug) || !strings.Contains(listBody, "https://example.com/tier1-feature") {
		t.Fatalf("public list omitted created resource: status=%d body=%s", list.StatusCode, listBody)
	}

	statsPage, statsHTML := get(t, client, "/"+slug+"/stats")
	if statsPage.StatusCode != http.StatusOK || !strings.Contains(statsHTML, slug) || !strings.Contains(statsHTML, "https://example.com/tier1-feature") || !strings.Contains(statsHTML, `class="info-value">1</span>`) {
		t.Fatalf("stats page omitted the recorded redirect: status=%d body=%s", statsPage.StatusCode, statsHTML)
	}

	req, err := http.NewRequest(http.MethodGet, httpBaseURL+"/api/v1/links/"+slug+"/stats", nil)
	if err != nil {
		t.Fatalf("build stats API request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET stats API: %v", err)
	}
	defer resp.Body.Close()
	var result struct {
		Data struct {
			ShortCode   string `json:"short_code"`
			TotalClicks int    `json:"total_clicks"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&result) != nil {
		t.Fatalf("GET stats API returned status %d or invalid JSON", resp.StatusCode)
	}
	if result.Data.ShortCode != slug || result.Data.TotalClicks != 1 {
		t.Errorf("stats API returned %+v, want code %q and one click", result.Data, slug)
	}
}

// TestTier1ExpiredAndUnknownResources verifies the public error contract for
// resource resolution: expired links are gone, while unknown slugs are absent.
func TestTier1ExpiredAndUnknownResources(t *testing.T) {
	client := newClient(t)
	past := "2000-01-01T00:00:00Z"
	requestBody := fmt.Sprintf(`{"url":"https://example.com/expired","slug":"tier1expired","expires_at":%q}`, past)
	req, err := http.NewRequest(http.MethodPost, httpBaseURL+"/api/v1/links", strings.NewReader(requestBody))
	if err != nil {
		t.Fatalf("build expired-link request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create expired link: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create expired link = %d, want 201", resp.StatusCode)
	}

	expired, _ := get(t, client, "/tier1expired")
	if expired.StatusCode != http.StatusGone {
		t.Errorf("expired link status = %d, want 410 Gone", expired.StatusCode)
	}
	unknown, body := get(t, client, "/tier1notfound")
	if unknown.StatusCode != http.StatusNotFound || !strings.Contains(body, "<html") || strings.Contains(body, "goroutine ") {
		t.Errorf("unknown resource response = %d, expected themed 404 without stack trace", unknown.StatusCode)
	}
}

// TestTier1OwnerManagementCoversAPICRUDAndWebTokenForms exercises the
// anonymous owner's bearer-token CRUD rights and the server-rendered token
// restore, create, and rename forms.
func TestTier1OwnerManagementCoversAPICRUDAndWebTokenForms(t *testing.T) {
	client := newClient(t)
	createReq, err := http.NewRequest(http.MethodPost, httpBaseURL+"/api/v1/links", strings.NewReader(`{"url":"https://example.com/owner-original","slug":"tier1owner"}`))
	if err != nil {
		t.Fatalf("build owner link request: %v", err)
	}
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Accept", "application/json")
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create owner link: %v", err)
	}
	var created struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(createResp.Body).Decode(&created)
	_ = createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated || decodeErr != nil || created.Data.OwnerToken == "" {
		t.Fatalf("owner link creation failed: status=%d json_error=%v", createResp.StatusCode, decodeErr)
	}

	unauthorized := apiRequest(t, client, http.MethodPatch, "/api/v1/links/tier1owner", `{"url":"https://example.com/unauthorized"}`, "")
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusNotFound {
		t.Errorf("owner-only PATCH without token = %d, want concealed 404", unauthorized.StatusCode)
	}
	patched := apiRequest(t, client, http.MethodPatch, "/api/v1/links/tier1owner", `{"url":"https://example.com/owner-updated"}`, created.Data.OwnerToken)
	var updated struct {
		Data struct {
			Link struct {
				Destination string `json:"destination_url"`
			} `json:"link"`
		} `json:"data"`
	}
	patchErr := json.NewDecoder(patched.Body).Decode(&updated)
	_ = patched.Body.Close()
	if patched.StatusCode != http.StatusOK || patchErr != nil || updated.Data.Link.Destination != "https://example.com/owner-updated" {
		t.Errorf("owner PATCH status=%d decode=%v updated=%+v", patched.StatusCode, patchErr, updated.Data.Link)
	}
	expiredPatch := apiRequest(t, client, http.MethodPatch, "/api/v1/links/tier1owner", `{"expires_at":"2000-01-01T00:00:00Z"}`, created.Data.OwnerToken)
	_ = expiredPatch.Body.Close()
	if expiredPatch.StatusCode != http.StatusOK {
		t.Errorf("owner expiration PATCH = %d, want 200", expiredPatch.StatusCode)
	}
	expiredResource, _ := get(t, client, "/tier1owner")
	if expiredResource.StatusCode != http.StatusGone {
		t.Errorf("owner link after expiration update = %d, want 410", expiredResource.StatusCode)
	}
	clearExpiry := apiRequest(t, client, http.MethodPatch, "/api/v1/links/tier1owner", `{"expires_at":""}`, created.Data.OwnerToken)
	_ = clearExpiry.Body.Close()
	if clearExpiry.StatusCode != http.StatusOK {
		t.Errorf("owner expiration clear PATCH = %d, want 200", clearExpiry.StatusCode)
	}
	activeResource, _ := get(t, client, "/tier1owner")
	if activeResource.StatusCode != http.StatusFound {
		t.Errorf("owner link after clearing expiration = %d, want 302", activeResource.StatusCode)
	}

	pageResp, pageBody := get(t, client, "/server/preferences/tokens")
	if pageResp.StatusCode != http.StatusOK || !strings.Contains(pageBody, `action="/server/preferences/token/restore"`) {
		t.Fatalf("token page did not render restore form: status=%d", pageResp.StatusCode)
	}
	csrf := csrfPattern.FindStringSubmatch(pageBody)
	if csrf == nil {
		t.Fatal("token restore form has no CSRF token")
	}
	restore := postForm(t, client, "/server/preferences/token/restore", url.Values{"csrf_token": {csrf[1]}, "token": {created.Data.OwnerToken}})
	if restore.StatusCode != http.StatusSeeOther {
		_ = restore.Body.Close()
		t.Fatalf("POST token restore = %d, want 303", restore.StatusCode)
	}
	_ = restore.Body.Close()

	pageReq, err := http.NewRequest(http.MethodGet, httpBaseURL+"/server/preferences/tokens", nil)
	if err != nil {
		t.Fatalf("build restored token page request: %v", err)
	}
	pageReq.Header.Set("Accept", "text/html")
	pageReq.AddCookie(&http.Cookie{Name: "shortner_owner_token_zgnygola", Value: created.Data.OwnerToken})
	page, err := client.Do(pageReq)
	if err != nil {
		t.Fatalf("get restored token page: %v", err)
	}
	ownerHTML, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil || page.StatusCode != http.StatusOK || !strings.Contains(string(ownerHTML), `action="/server/preferences/tokens"`) {
		t.Fatalf("restored token page missing linked-token creation form: status=%d read_error=%v", page.StatusCode, err)
	}
	csrf = csrfPattern.FindStringSubmatch(string(ownerHTML))
	if csrf == nil {
		t.Fatal("linked-token creation form has no CSRF token")
	}
	createLinked := postFormWithCookie(t, client, "/server/preferences/tokens", url.Values{
		"csrf_token": {csrf[1]}, "name": {"browser integration"}, "expiry": {"never"},
	}, &http.Cookie{Name: "shortner_owner_token_zgnygola", Value: created.Data.OwnerToken})
	_ = createLinked.Body.Close()
	if createLinked.StatusCode != http.StatusSeeOther {
		t.Fatalf("create linked token form = %d, want 303", createLinked.StatusCode)
	}

	pageReq, err = http.NewRequest(http.MethodGet, httpBaseURL+"/server/preferences/tokens", nil)
	if err != nil {
		t.Fatalf("build linked token page request: %v", err)
	}
	pageReq.Header.Set("Accept", "text/html")
	pageReq.AddCookie(&http.Cookie{Name: "shortner_owner_token_zgnygola", Value: created.Data.OwnerToken})
	page, err = client.Do(pageReq)
	if err != nil {
		t.Fatalf("get linked token page: %v", err)
	}
	linkedHTML, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil || page.StatusCode != http.StatusOK || !strings.Contains(string(linkedHTML), "browser integration") {
		t.Fatalf("created linked token not shown: status=%d read_error=%v", page.StatusCode, err)
	}
	idMatch := regexp.MustCompile(`id="token-([0-9]+)"`).FindStringSubmatch(string(linkedHTML))
	csrf = csrfPattern.FindStringSubmatch(string(linkedHTML))
	if idMatch == nil || csrf == nil {
		t.Fatal("linked token card has no id or CSRF token")
	}
	updateLinked := postFormWithCookie(t, client, "/server/preferences/tokens/"+idMatch[1]+"/update", url.Values{
		"csrf_token": {csrf[1]}, "name": {"browser renamed"}, "expiry": {"never"},
	}, &http.Cookie{Name: "shortner_owner_token_zgnygola", Value: created.Data.OwnerToken})
	_ = updateLinked.Body.Close()
	if updateLinked.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename linked token form = %d, want 303", updateLinked.StatusCode)
	}
	pageReq, err = http.NewRequest(http.MethodGet, httpBaseURL+"/server/preferences/tokens", nil)
	if err != nil {
		t.Fatalf("build updated linked-token page request: %v", err)
	}
	pageReq.Header.Set("Accept", "text/html")
	pageReq.AddCookie(&http.Cookie{Name: "shortner_owner_token_zgnygola", Value: created.Data.OwnerToken})
	page, err = client.Do(pageReq)
	if err != nil {
		t.Fatalf("get updated linked-token page: %v", err)
	}
	updatedHTML, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if err != nil || page.StatusCode != http.StatusOK || !strings.Contains(string(updatedHTML), "browser renamed") {
		t.Fatalf("renamed linked token not shown: status=%d read_error=%v", page.StatusCode, err)
	}

	deleted := apiRequest(t, client, http.MethodDelete, "/api/v1/links/tier1owner", "", created.Data.OwnerToken)
	_ = deleted.Body.Close()
	if deleted.StatusCode != http.StatusOK {
		t.Errorf("owner DELETE = %d, want 200", deleted.StatusCode)
	}
	missing, _ := get(t, client, "/tier1owner")
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("deleted link lookup = %d, want 404", missing.StatusCode)
	}
}

func apiRequest(t *testing.T, client *http.Client, method, path, body, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, httpBaseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build API %s %s request: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("API %s %s: %v", method, path, err)
	}
	return resp
}

func postFormWithCookie(t *testing.T, client *http.Client, path string, values url.Values, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, httpBaseURL+path, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")
	req.AddCookie(cookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// TestTier1SlugValidationErrors verifies that malformed, reserved, and
// duplicate vanity slugs fail as complete server-rendered form responses.
func TestTier1SlugValidationErrors(t *testing.T) {
	client := newClient(t)
	create := func(slug string) *http.Response {
		t.Helper()
		_, html := get(t, client, "/")
		csrf := csrfPattern.FindStringSubmatch(html)
		if csrf == nil {
			t.Fatal("home form has no CSRF token")
		}
		return postForm(t, client, "/", url.Values{
			"csrf_token": {csrf[1]},
			"url":        {"https://example.com/validation"},
			"slug":       {slug},
		})
	}

	for _, tc := range []struct {
		name       string
		slug       string
		wantStatus int
		wantText   string
	}{
		{name: "malformed", slug: "bad_slug", wantStatus: http.StatusBadRequest, wantText: "slug"},
		{name: "reserved", slug: "server", wantStatus: http.StatusConflict, wantText: "reserved"},
		{name: "duplicate", slug: "tier1dupe", wantStatus: http.StatusConflict, wantText: "already"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "duplicate" {
				created := create(tc.slug)
				_ = created.Body.Close()
				if created.StatusCode != http.StatusCreated {
					t.Fatalf("seed slug returned %d, want 201", created.StatusCode)
				}
			}
			resp := create(tc.slug)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read validation response: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("POST / = %d, want %d: %s", resp.StatusCode, tc.wantStatus, body)
			}
			if !strings.Contains(strings.ToLower(string(body)), tc.wantText) || !strings.Contains(string(body), "<form") {
				t.Errorf("validation response did not render the form error %q: %s", tc.wantText, body)
			}
		})
	}
}

func postForm(t *testing.T, client *http.Client, path string, values url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, httpBaseURL+path, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}
