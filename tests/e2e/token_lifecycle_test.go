//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestTier1TokenLifecycleAndPrimaryOnlyManagement covers token reveal,
// independent rotation, linked-token revocation, and primary revocation via
// the authenticated API mirror.
func TestTier1TokenLifecycleAndPrimaryOnlyManagement(t *testing.T) {
	client := newClient(t)
	created := apiRequest(t, client, http.MethodPost, "/api/v1/links", `{"url":"https://example.com/token-lifecycle","slug":"tier1lifecycle"}`, "")
	var linkResult struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(created.Body).Decode(&linkResult); err != nil || created.StatusCode != http.StatusCreated || linkResult.Data.OwnerToken == "" {
		_ = created.Body.Close()
		t.Fatalf("create owner resource: status=%d decode_error=%v", created.StatusCode, err)
	}
	_ = created.Body.Close()
	primary := linkResult.Data.OwnerToken

	primaryReveal := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary/reveal", "", primary)
	var revealed struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(primaryReveal.Body).Decode(&revealed)
	_ = primaryReveal.Body.Close()
	if primaryReveal.StatusCode != http.StatusOK || decodeErr != nil || revealed.Data.Token != primary {
		t.Fatalf("primary reveal failed: status=%d decode_error=%v", primaryReveal.StatusCode, decodeErr)
	}

	linkedCreate := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens", `{"name":"lifecycle integration","expiry":"never"}`, primary)
	var linkedResult struct {
		Data struct {
			Token string `json:"token"`
			Data  struct {
				ID int64 `json:"id"`
			} `json:"data"`
		} `json:"data"`
	}
	decodeErr = json.NewDecoder(linkedCreate.Body).Decode(&linkedResult)
	_ = linkedCreate.Body.Close()
	if linkedCreate.StatusCode != http.StatusOK || decodeErr != nil || linkedResult.Data.Token == "" || linkedResult.Data.Data.ID <= 0 {
		t.Fatalf("linked-token creation failed: status=%d decode_error=%v", linkedCreate.StatusCode, decodeErr)
	}
	linked := linkedResult.Data.Token
	linkedID := linkedResult.Data.Data.ID
	linkedPath := fmt.Sprintf("/api/v1/server/preferences/tokens/%d", linkedID)

	managementCalls := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"snapshot", http.MethodGet, "/api/v1/server/preferences/tokens", ""},
		{"create", http.MethodPost, "/api/v1/server/preferences/tokens", `{"name":"forbidden"}`},
		{"primary reveal", http.MethodPost, "/api/v1/server/preferences/tokens/primary/reveal", ""},
		{"primary rotate", http.MethodPost, "/api/v1/server/preferences/tokens/primary/rotate", ""},
		{"linked reveal", http.MethodPost, linkedPath + "/reveal", ""},
		{"linked rotate", http.MethodPost, linkedPath + "/rotate", ""},
		{"linked update", http.MethodPatch, linkedPath, `{"name":"forbidden","expiry":"never"}`},
		{"linked revoke", http.MethodDelete, linkedPath, ""},
		{"primary revoke", http.MethodDelete, "/api/v1/server/preferences/tokens/primary", fmt.Sprintf(`{"confirm":%q}`, primary[len(primary)-4:])},
	}
	for _, call := range managementCalls {
		resp := tokenAPIRequest(t, client, call.method, call.path, call.body, linked)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("linked token %s = %d, want 403", call.name, resp.StatusCode)
		}
	}
	issueWithLinked := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary", "", linked)
	_ = issueWithLinked.Body.Close()
	if issueWithLinked.StatusCode != http.StatusConflict {
		t.Errorf("issuing a primary with a valid linked token = %d, want 409", issueWithLinked.StatusCode)
	}

	primaryRotate := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary/rotate", "", primary)
	var primaryRotated struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeErr = json.NewDecoder(primaryRotate.Body).Decode(&primaryRotated)
	_ = primaryRotate.Body.Close()
	if primaryRotate.StatusCode != http.StatusOK || decodeErr != nil || primaryRotated.Data.Token == "" || primaryRotated.Data.Token == primary {
		t.Fatalf("primary rotation failed: status=%d decode_error=%v", primaryRotate.StatusCode, decodeErr)
	}
	newPrimary := primaryRotated.Data.Token
	oldPrimary := tokenAPIRequest(t, client, http.MethodGet, "/api/v1/server/preferences/tokens", "", primary)
	_ = oldPrimary.Body.Close()
	if oldPrimary.StatusCode != http.StatusUnauthorized {
		t.Errorf("old primary after rotation = %d, want 401", oldPrimary.StatusCode)
	}
	newPrimarySnapshot := tokenAPIRequest(t, client, http.MethodGet, "/api/v1/server/preferences/tokens", "", newPrimary)
	_ = newPrimarySnapshot.Body.Close()
	if newPrimarySnapshot.StatusCode != http.StatusOK {
		t.Errorf("new primary after rotation = %d, want 200", newPrimarySnapshot.StatusCode)
	}
	linkedStillValid := tokenAPIRequest(t, client, http.MethodPatch, "/api/v1/links/tier1lifecycle", `{"url":"https://example.com/after-primary-rotation"}`, linked)
	_ = linkedStillValid.Body.Close()
	if linkedStillValid.StatusCode != http.StatusOK {
		t.Errorf("linked token stopped working after primary rotation: %d", linkedStillValid.StatusCode)
	}

	linkedReveal := tokenAPIRequest(t, client, http.MethodPost, linkedPath+"/reveal", "", newPrimary)
	var revealedLinked struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeErr = json.NewDecoder(linkedReveal.Body).Decode(&revealedLinked)
	_ = linkedReveal.Body.Close()
	if linkedReveal.StatusCode != http.StatusOK || decodeErr != nil || revealedLinked.Data.Token != linked {
		t.Errorf("linked reveal failed: status=%d decode_error=%v", linkedReveal.StatusCode, decodeErr)
	}

	linkedRotate := tokenAPIRequest(t, client, http.MethodPost, linkedPath+"/rotate", "", newPrimary)
	var linkedRotated struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeErr = json.NewDecoder(linkedRotate.Body).Decode(&linkedRotated)
	_ = linkedRotate.Body.Close()
	if linkedRotate.StatusCode != http.StatusOK || decodeErr != nil || linkedRotated.Data.Token == "" || linkedRotated.Data.Token == linked {
		t.Fatalf("linked rotation failed: status=%d decode_error=%v", linkedRotate.StatusCode, decodeErr)
	}
	newLinked := linkedRotated.Data.Token
	oldLinked := tokenAPIRequest(t, client, http.MethodPatch, "/api/v1/links/tier1lifecycle", `{"url":"https://example.com/old-linked"}`, linked)
	_ = oldLinked.Body.Close()
	if oldLinked.StatusCode != http.StatusNotFound {
		t.Errorf("old linked token after rotation = %d, want concealed 404", oldLinked.StatusCode)
	}
	newLinkedWorks := tokenAPIRequest(t, client, http.MethodPatch, "/api/v1/links/tier1lifecycle", `{"url":"https://example.com/new-linked"}`, newLinked)
	_ = newLinkedWorks.Body.Close()
	if newLinkedWorks.StatusCode != http.StatusOK {
		t.Errorf("new linked token failed after rotation: %d", newLinkedWorks.StatusCode)
	}
	primaryUnaffected := tokenAPIRequest(t, client, http.MethodPatch, "/api/v1/links/tier1lifecycle", `{"url":"https://example.com/primary-still-works"}`, newPrimary)
	_ = primaryUnaffected.Body.Close()
	if primaryUnaffected.StatusCode != http.StatusOK {
		t.Errorf("linked rotation invalidated primary: %d", primaryUnaffected.StatusCode)
	}

	linkedRevoke := tokenAPIRequest(t, client, http.MethodDelete, linkedPath, "", newPrimary)
	_ = linkedRevoke.Body.Close()
	if linkedRevoke.StatusCode != http.StatusOK {
		t.Errorf("linked revoke = %d, want 200", linkedRevoke.StatusCode)
	}
	revokedLinked := tokenAPIRequest(t, client, http.MethodPatch, "/api/v1/links/tier1lifecycle", `{"url":"https://example.com/revoked-linked"}`, newLinked)
	_ = revokedLinked.Body.Close()
	if revokedLinked.StatusCode != http.StatusNotFound {
		t.Errorf("revoked linked token still authorized a resource update: %d", revokedLinked.StatusCode)
	}

	remainingCreate := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens", `{"name":"primary revoke check","expiry":"never"}`, newPrimary)
	var remainingToken struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeErr = json.NewDecoder(remainingCreate.Body).Decode(&remainingToken)
	_ = remainingCreate.Body.Close()
	if remainingCreate.StatusCode != http.StatusOK || decodeErr != nil || remainingToken.Data.Token == "" {
		t.Fatalf("create linked token for primary-revoke test: status=%d decode_error=%v", remainingCreate.StatusCode, decodeErr)
	}

	primaryRevoke := tokenAPIRequest(t, client, http.MethodDelete, "/api/v1/server/preferences/tokens/primary", fmt.Sprintf(`{"confirm":%q}`, newPrimary[len(newPrimary)-4:]), newPrimary)
	_ = primaryRevoke.Body.Close()
	if primaryRevoke.StatusCode != http.StatusOK {
		t.Fatalf("primary revoke = %d, want 200", primaryRevoke.StatusCode)
	}
	for label, token := range map[string]string{"primary": newPrimary, "linked": remainingToken.Data.Token} {
		denied := tokenAPIRequest(t, client, http.MethodGet, "/api/v1/server/preferences/tokens", "", token)
		_ = denied.Body.Close()
		if denied.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s token after primary revoke = %d, want 401", label, denied.StatusCode)
		}
	}
	resource, _ := get(t, client, "/tier1lifecycle")
	if resource.StatusCode != http.StatusNotFound {
		t.Errorf("primary revoke left owner resource visible: status=%d", resource.StatusCode)
	}
}

// TestTier1TokenIssuanceAndRestore checks the unauthenticated API issue path
// and confirms a valid existing token cannot issue another primary identity.
func TestTier1TokenIssuanceAndRestore(t *testing.T) {
	client := newClient(t)
	issued := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary", "", "")
	var result struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	err := json.NewDecoder(issued.Body).Decode(&result)
	_ = issued.Body.Close()
	if issued.StatusCode != http.StatusCreated || err != nil || result.Data.OwnerToken == "" {
		t.Fatalf("issue owner token: status=%d decode_error=%v", issued.StatusCode, err)
	}
	conflict := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary", "", result.Data.OwnerToken)
	_ = conflict.Body.Close()
	if conflict.StatusCode != http.StatusConflict {
		t.Errorf("valid primary token issued another owner: status=%d, want 409", conflict.StatusCode)
	}
}

// TestTier1WebTokenRevealIsOneShot confirms the token page reveals a primary
// token for one redirected page load and masks it again on the next load.
func TestTier1WebTokenRevealIsOneShot(t *testing.T) {
	client := newClient(t)
	issued := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary", "", "")
	var result struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	issueErr := json.NewDecoder(issued.Body).Decode(&result)
	_ = issued.Body.Close()
	if issued.StatusCode != http.StatusCreated || issueErr != nil || result.Data.OwnerToken == "" {
		t.Fatalf("issue token for web reveal: status=%d decode_error=%v", issued.StatusCode, issueErr)
	}

	ownerCookie := http.Cookie{Name: "shortner_owner_token_zgnygola", Value: result.Data.OwnerToken}
	page, pageHTML := getTokenPageWithCookies(t, client, ownerCookie)
	if page.StatusCode != http.StatusOK {
		t.Fatalf("initial token page status = %d", page.StatusCode)
	}
	csrf := csrfPattern.FindStringSubmatch(pageHTML)
	if csrf == nil || strings.Contains(pageHTML, result.Data.OwnerToken) {
		t.Fatal("initial token page lacks CSRF or unexpectedly exposes the primary token")
	}
	reveal := postFormWithCookie(t, client, "/server/preferences/token/reveal", url.Values{"csrf_token": {csrf[1]}}, &ownerCookie)
	_ = reveal.Body.Close()
	if reveal.StatusCode != http.StatusSeeOther {
		t.Fatalf("primary reveal form = %d, want 303", reveal.StatusCode)
	}
	var viewCookie *http.Cookie
	for _, cookie := range reveal.Cookies() {
		if cookie.Name == "shortner_token_view" {
			viewCookie = cookie
			break
		}
	}
	if viewCookie == nil {
		t.Fatal("primary reveal response omitted its one-use view cookie")
	}
	revealedPage, revealedHTML := getTokenPageWithCookies(t, client, ownerCookie, *viewCookie)
	if revealedPage.StatusCode != http.StatusOK || !strings.Contains(revealedHTML, result.Data.OwnerToken) {
		t.Errorf("one-use token view did not reveal the primary token (status %d)", revealedPage.StatusCode)
	}
	maskedPage, maskedHTML := getTokenPageWithCookies(t, client, ownerCookie)
	if maskedPage.StatusCode != http.StatusOK || strings.Contains(maskedHTML, result.Data.OwnerToken) {
		t.Errorf("primary token was not masked on the next page load (status %d)", maskedPage.StatusCode)
	}
}

func getTokenPageWithCookies(t *testing.T, client *http.Client, cookies ...http.Cookie) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, httpBaseURL+"/server/preferences/tokens", nil)
	if err != nil {
		t.Fatalf("build token page request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	for i := range cookies {
		req.AddCookie(&cookies[i])
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("load token page: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		t.Fatalf("read token page: %v", err)
	}
	_ = resp.Body.Close()
	return resp, string(body)
}

func tokenAPIRequest(t *testing.T, client *http.Client, method, path, body, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, httpBaseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build token API %s %s request: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("token API %s %s: %v", method, path, err)
	}
	return resp
}

// TestTier2NoJavaScriptOwnerTokenFormFlow confirms token management remains
// usable from its native forms with script execution disabled.
func TestTier2NoJavaScriptOwnerTokenFormFlow(t *testing.T) {
	client := newClient(t)
	issued := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary", "", "")
	var result struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	err := json.NewDecoder(issued.Body).Decode(&result)
	_ = issued.Body.Close()
	if issued.StatusCode != http.StatusCreated || err != nil || result.Data.OwnerToken == "" {
		t.Fatalf("issue owner token for no-JS flow: status=%d decode_error=%v", issued.StatusCode, err)
	}

	ctx, _ := browserContext(t)
	if err := setBrowserOwnerCookie(ctx, result.Data.OwnerToken); err != nil {
		t.Fatalf("inject owner cookie into browser context: %v", err)
	}
	var html string
	err = chromedp.Run(ctx,
		emulation.SetScriptExecutionDisabled(true),
		chromedp.Navigate(browserBaseURL+"/server/preferences/tokens"),
		chromedp.WaitVisible(`#create-linked`, chromedp.ByQuery),
	)
	if err == nil {
		err = chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery))
	}
	if err != nil {
		t.Fatalf("loading linked-token form without JavaScript: %v", err)
	}
	if !strings.Contains(html, `action="/server/preferences/tokens"`) || !strings.Contains(html, `name="csrf_token"`) {
		t.Fatalf("no-JavaScript linked-token page omitted its native POST form")
	}

	ownerCookie := http.Cookie{Name: "shortner_owner_token_zgnygola", Value: result.Data.OwnerToken}
	page, pageHTML := getTokenPageWithCookies(t, client, ownerCookie)
	csrf := csrfPattern.FindStringSubmatch(pageHTML)
	if page.StatusCode != http.StatusOK || csrf == nil {
		t.Fatalf("HTTP token form page = %d or has no CSRF token", page.StatusCode)
	}
	created := postFormWithCookie(t, client, "/server/preferences/tokens", url.Values{
		"csrf_token": {csrf[1]},
		"name":       {"tier2 client"},
		"expiry":     {"never"},
	}, &ownerCookie)
	_ = created.Body.Close()
	if created.StatusCode != http.StatusSeeOther {
		t.Fatalf("no-JavaScript native linked-token POST = %d, want 303", created.StatusCode)
	}
}

// TestTier3BrowserOwnerTokenFormFlow checks that the full browser renders the
// authenticated linked-token form and its server-provided controls.
func TestTier3BrowserOwnerTokenFormFlow(t *testing.T) {
	client := newClient(t)
	issued := tokenAPIRequest(t, client, http.MethodPost, "/api/v1/server/preferences/tokens/primary", "", "")
	var result struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	err := json.NewDecoder(issued.Body).Decode(&result)
	_ = issued.Body.Close()
	if issued.StatusCode != http.StatusCreated || err != nil || result.Data.OwnerToken == "" {
		t.Fatalf("issue owner token for browser flow: status=%d decode_error=%v", issued.StatusCode, err)
	}

	ctx, browserErrs := browserContext(t)
	if err := setBrowserOwnerCookie(ctx, result.Data.OwnerToken); err != nil {
		t.Fatalf("inject owner cookie into browser context: %v", err)
	}
	var html string
	err = chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/server/preferences/tokens"),
		chromedp.WaitVisible(`#create-linked`, chromedp.ByQuery),
	)
	if err == nil {
		err = chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery))
	}
	if err != nil {
		t.Fatalf("loading linked-token form in browser: %v", err)
	}
	if !strings.Contains(html, `action="/server/preferences/tokens"`) || !strings.Contains(html, `name="csrf_token"`) {
		saveArtifacts(t, "tier3-token-management", html, nil)
		t.Errorf("browser linked-token form omitted its native POST controls")
	}
	if errs := browserErrs.list(); len(errs) > 0 {
		t.Errorf("browser reported console or network errors during token creation: %s", strings.Join(errs, " | "))
	}
}

func setBrowserOwnerCookie(ctx context.Context, raw string) error {
	// The E2E server is HTTP. CDP injects the test credential without changing
	// production cookie flags; the application still emits Secure cookies.
	return chromedp.Run(ctx, network.SetCookie("shortner_owner_token_zgnygola", raw).
		WithURL(browserBaseURL).
		WithPath("/").
		WithHTTPOnly(true).
		WithSecure(false).
		WithSameSite(network.CookieSameSiteStrict))
}
