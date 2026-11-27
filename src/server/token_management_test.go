package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
)

func TestTokenRestoreRevealAndPrimaryOnlyAPI(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	primaryRaw, primary, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	linkedRaw, linked, err := db.CreateLinkedToken(context.Background(), ld.sqlDB, ownerID, "integration", &expiry, 25, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}

	form := "token=" + primaryRaw
	restoreRequest := httptest.NewRequest(http.MethodPost, "/server/preferences/token/restore", strings.NewReader(form))
	restoreRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	restoreRequest.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: primaryRaw})
	restoreResponse := httptest.NewRecorder()
	fd.tokenRestoreHandler(restoreResponse, restoreRequest)
	if restoreResponse.Code != http.StatusSeeOther {
		t.Fatalf("restore status = %d, body = %q", restoreResponse.Code, restoreResponse.Body.String())
	}
	setCookie := restoreResponse.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, ownerTokenCookieName+"="+primaryRaw) || !strings.Contains(setCookie, "HttpOnly") || !strings.Contains(setCookie, "Secure") || !strings.Contains(setCookie, "SameSite=Strict") {
		t.Errorf("restore cookie missing required attributes: %q", setCookie)
	}
	if !strings.Contains(strings.Join(restoreResponse.Header().Values("Set-Cookie"), "\n"), legacyOwnerTokenCookieName+"=; Path=/; Max-Age=0") {
		t.Errorf("restore did not clear the legacy cookie: %q", restoreResponse.Header().Values("Set-Cookie"))
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	pageRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: primaryRaw})
	pageResponse := httptest.NewRecorder()
	fd.tokenPageHandler(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("token page status = %d, body = %s", pageResponse.Code, pageResponse.Body.String())
	}
	if strings.Contains(pageResponse.Body.String(), primaryRaw) || strings.Contains(pageResponse.Body.String(), linkedRaw) {
		t.Error("masked token page exposed a raw token")
	}
	if got := pageResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("token page Cache-Control = %q, want no-store", got)
	}

	revealRequest := httptest.NewRequest(http.MethodPost, "/server/preferences/token/reveal", nil)
	revealRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: primaryRaw})
	revealResponse := httptest.NewRecorder()
	fd.tokenPrimaryRevealHandler(revealResponse, revealRequest)
	if revealResponse.Code != http.StatusSeeOther || revealResponse.Header().Get("Location") != "/server/preferences/tokens#primary" {
		t.Fatalf("primary reveal did not redirect after POST: %d, %q", revealResponse.Code, revealResponse.Header().Get("Location"))
	}
	if got := revealResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("reveal Cache-Control = %q, want no-store", got)
	}
	var viewCookie *http.Cookie
	for _, cookie := range revealResponse.Result().Cookies() {
		if cookie.Name == tokenViewCookieName {
			viewCookie = cookie
		}
	}
	if viewCookie == nil || verifyTokenViewCookie(fd.secretKeys, viewCookie.Value) != "primary" || viewCookie.MaxAge != 60 || viewCookie.Path != "/server/preferences/tokens" || !viewCookie.HttpOnly || !viewCookie.Secure || viewCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("primary reveal view cookie = %+v", viewCookie)
	}
	revealPageRequest := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	revealPageRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: primaryRaw})
	revealPageRequest.AddCookie(viewCookie)
	revealPageResponse := httptest.NewRecorder()
	fd.tokenPageHandler(revealPageResponse, revealPageRequest)
	if revealPageResponse.Code != http.StatusOK || !strings.Contains(revealPageResponse.Body.String(), primaryRaw) {
		t.Fatalf("one-use reveal page did not show primary token: %d, %s", revealPageResponse.Code, revealPageResponse.Body.String())
	}
	clearedView := false
	for _, cookie := range revealPageResponse.Result().Cookies() {
		clearedView = clearedView || (cookie.Name == tokenViewCookieName && cookie.MaxAge <= 0)
	}
	if !clearedView {
		t.Fatal("one-use reveal cookie was not cleared by the page GET")
	}
	maskedPageRequest := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	maskedPageRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: primaryRaw})
	maskedPageResponse := httptest.NewRecorder()
	fd.tokenPageHandler(maskedPageResponse, maskedPageRequest)
	if strings.Contains(maskedPageResponse.Body.String(), primaryRaw) {
		t.Fatal("reloaded token page exposed a previously revealed token")
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	apiRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: primaryRaw})
	apiRequest.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: primaryRaw})
	apiResponse := httptest.NewRecorder()
	fd.tokenAPISnapshotHandler(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusUnauthorized {
		t.Errorf("cookie-only API status = %d, want 401", apiResponse.Code)
	}

	apiRequest = httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	apiRequest.Header.Set("Authorization", "Bearer "+primaryRaw)
	apiResponse = httptest.NewRecorder()
	fd.tokenAPISnapshotHandler(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusOK || strings.Contains(apiResponse.Body.String(), primaryRaw) || strings.Contains(apiResponse.Body.String(), linkedRaw) {
		t.Fatalf("primary API list exposed tokens or failed: %d, %s", apiResponse.Code, apiResponse.Body.String())
	}

	apiRequest = httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	apiRequest.Header.Set("Authorization", "Bearer "+linkedRaw)
	apiResponse = httptest.NewRecorder()
	fd.tokenAPISnapshotHandler(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusForbidden {
		t.Errorf("linked-token management status = %d, want 403", apiResponse.Code)
	}

	if primary.OwnerID != linked.OwnerID {
		t.Fatal("test tokens do not share an owner")
	}
}

func TestTokenManagementPageRoutesMounted(t *testing.T) {
	sqlDB := openTestDB(t)
	cfg := config.Default(":memory:")
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	srv := New(Options{Config: cfg, DB: sqlDB, Version: "test", CommitID: "123456789"})
	req := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Use an existing token") || !strings.Contains(rec.Body.String(), "/server/preferences/token/create") {
		t.Fatalf("token route status/body = %d, %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Set-Cookie") == "" {
		t.Error("page GET did not issue the CSRF cookie required by its forms")
	}
}

func TestLinkedTokenAPIUsesCanonicalUUIDAndScopeContract(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	primary, _, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens", strings.NewReader(`{"name":"readonly","scope":"read","expires":"never"}`))
	req.Header.Set("Authorization", "Bearer "+primary)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fd.tokenAPICreateHandler(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status/body = %d, %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Kind        string `json:"kind"`
			Scope       string `json:"scope"`
			Masked      string `json:"token_masked"`
			LinkedToken string `json:"linked_token"`
			Created     string `json:"created_at"`
			Status      string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ID == "" || response.Data.Name != "readonly" || response.Data.Kind != "linked" || response.Data.Scope != "read" || response.Data.Masked == "" || response.Data.LinkedToken == "" || response.Data.Created == "" || response.Data.Status != "active" {
		t.Fatalf("create response does not match token contract: %s", rec.Body.String())
	}
	if _, err := db.GetTokenByPublicID(context.Background(), ld.sqlDB, response.Data.ID); err != nil {
		t.Fatalf("UUID token id lookup: %v", err)
	}
}

func TestNewPrimaryTokenUsesOneUseViewAndCurrentCookie(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodPost, "/server/preferences/token/create", nil)
	rec := httptest.NewRecorder()
	fd.tokenIssueHandler(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/server/preferences/tokens#primary" {
		t.Fatalf("issue status/location = %d, %q", rec.Code, rec.Header().Get("Location"))
	}
	var ownerCookie, viewCookie *http.Cookie
	legacyCleared := false
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case ownerTokenCookieName:
			ownerCookie = cookie
		case legacyOwnerTokenCookieName:
			legacyCleared = cookie.MaxAge <= 0
		case tokenViewCookieName:
			viewCookie = cookie
		}
	}
	if ownerCookie == nil || ownerCookie.Value == "" || viewCookie == nil || verifyTokenViewCookie(fd.secretKeys, viewCookie.Value) != "primary" || !legacyCleared {
		t.Fatalf("issue cookies = %+v; legacy cleared = %v", rec.Result().Cookies(), legacyCleared)
	}
	if strings.Contains(rec.Header().Get("Location"), ownerCookie.Value) || strings.Contains(rec.Body.String(), ownerCookie.Value) {
		t.Fatal("issued token was exposed in the redirect or POST body")
	}
	pageReq := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	pageReq.AddCookie(ownerCookie)
	pageReq.AddCookie(viewCookie)
	pageRec := httptest.NewRecorder()
	fd.tokenPageHandler(pageRec, pageReq)
	if pageRec.Code != http.StatusOK || !strings.Contains(pageRec.Body.String(), ownerCookie.Value) {
		t.Fatalf("one-use issue page did not show token: %d, %s", pageRec.Code, pageRec.Body.String())
	}
}

func TestAPIPrimaryIssueIgnoresBothOwnerCookies(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/primary", nil)
	req.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: "own_invalid"})
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: "own_legacy"})
	rec := httptest.NewRecorder()
	fd.tokenAPIIssueHandler(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("API primary issue status/body = %d, %s", rec.Code, rec.Body.String())
	}
	var issued struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil || issued.Data.OwnerToken == "" {
		t.Fatalf("API issue payload = %s, %v", rec.Body.String(), err)
	}
	if len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("API primary issue emitted browser cookies: %q", rec.Header().Values("Set-Cookie"))
	}
	conflictReq := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences/tokens/primary", nil)
	conflictReq.Header.Set("Authorization", "Bearer "+issued.Data.OwnerToken)
	conflictRec := httptest.NewRecorder()
	fd.tokenAPIIssueHandler(conflictRec, conflictReq)
	if conflictRec.Code != http.StatusConflict {
		t.Fatalf("API issue with valid token status = %d, want 409", conflictRec.Code)
	}
}

func TestPrimaryRevokeRequiresLastFourAndClearsOwnerCookies(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	raw, token, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	link, err := db.CreateLinkAutoCodeOwned(context.Background(), ld.sqlDB, ownerID, "https://example.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordClick(context.Background(), ld.sqlDB, link.ID, "192.0.2.1", "test", "", "", ""); err != nil {
		t.Fatal(err)
	}
	badReq := httptest.NewRequest(http.MethodPost, "/server/preferences/token/revoke", strings.NewReader("confirm=!!!!"))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badReq.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: raw})
	badRec := httptest.NewRecorder()
	fd.tokenPrimaryRevokeHandler(badRec, badReq)
	if badRec.Code != http.StatusSeeOther {
		t.Fatalf("bad revoke status = %d", badRec.Code)
	}
	if _, err := db.LookupTokenByRaw(context.Background(), ld.sqlDB, raw); err != nil {
		t.Fatalf("wrong last4 revoked token: %v", err)
	}
	goodReq := httptest.NewRequest(http.MethodPost, "/server/preferences/token/revoke", strings.NewReader("confirm="+token.TokenLast4))
	goodReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	goodReq.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: raw})
	goodRec := httptest.NewRecorder()
	fd.tokenPrimaryRevokeHandler(goodRec, goodReq)
	if goodRec.Code != http.StatusSeeOther {
		t.Fatalf("valid revoke status = %d", goodRec.Code)
	}
	if _, err := db.LookupTokenByRaw(context.Background(), ld.sqlDB, raw); err == nil {
		t.Fatal("primary token remained valid after owner deletion")
	}
	if _, err := db.GetOwner(context.Background(), ld.sqlDB, ownerID); err == nil {
		t.Fatal("owner remained after primary token revocation")
	}
	if _, err := db.GetLinkByID(context.Background(), ld.sqlDB, link.ID); err == nil {
		t.Fatal("owner resource remained after primary token revocation")
	}
	setCookies := strings.Join(goodRec.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(setCookies, ownerTokenCookieName+"=; Path=/; Max-Age=0") || !strings.Contains(setCookies, legacyOwnerTokenCookieName+"=; Path=/; Max-Age=0") {
		t.Errorf("primary revoke did not clear both owner cookies: %q", setCookies)
	}
}

func TestTokenCardMarksHashOnlyLegacyMetadataUnavailable(t *testing.T) {
	card := tokenCardFor(&db.APIToken{TokenPrefix: "own_abcdefgh", TokenEnc: "", TokenLast4: ""}, "", nil)
	if card.Last4Available || card.RevealAvailable || card.Masked != "own_••••••••••••••••••••••••" {
		t.Fatalf("hash-only token card falsely advertises recoverable data: %+v", card)
	}
	card = tokenCardFor(&db.APIToken{TokenPrefix: "own_abcdefgh", TokenHash: "expected-hash", TokenEnc: "not-valid-ciphertext", TokenLast4: ""}, "", make([]byte, 32))
	if card.RevealAvailable || card.Last4Available {
		t.Fatalf("unrecoverable token metadata was advertised as available: %+v", card)
	}
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"id":""`) || !strings.Contains(string(encoded), `"token_masked"`) || strings.Contains(string(encoded), `"masked"`) || strings.Contains(string(encoded), `last4_available`) {
		t.Fatalf("token object did not use the canonical JSON fields: %s", encoded)
	}
}

func TestAPIPrimaryRevokeRequiresBearerAndIgnoresCookies(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	raw, token, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/server/preferences/tokens/primary", nil)
	req.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: raw})
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: raw})
	unauthorized := httptest.NewRecorder()
	fd.tokenAPIActionHandler(unauthorized, req)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("cookie-only API revoke status = %d", unauthorized.Code)
	}
	for _, body := range []string{"", `{"confirm":"xxxx"}`} {
		req = httptest.NewRequest(http.MethodDelete, "/api/v1/server/preferences/tokens/primary", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+raw)
		rec := httptest.NewRecorder()
		fd.tokenAPIActionHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("API primary revoke with confirmation %q status/body = %d, %s", body, rec.Code, rec.Body.String())
		}
		if _, err := db.GetOwner(context.Background(), ld.sqlDB, ownerID); err != nil {
			t.Fatalf("invalid API confirmation deleted owner: %v", err)
		}
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/server/preferences/tokens/primary", strings.NewReader(`{"confirm":"`+token.TokenLast4+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+raw)
	req.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: "own_invalid"})
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: "own_legacy"})
	rec := httptest.NewRecorder()
	fd.tokenAPIActionHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer API revoke status/body = %d, %s", rec.Code, rec.Body.String())
	}
	if _, err := db.GetOwner(context.Background(), ld.sqlDB, ownerID); err == nil {
		t.Fatal("API primary revoke did not delete owner")
	}
	if len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("API revoke emitted browser cookies: %q", rec.Header().Values("Set-Cookie"))
	}
}

func TestLegacyOwnerCookieMigratesAfterValidation(t *testing.T) {
	if ownerTokenCookieName != "shortner_owner_token_zgnygola" {
		t.Fatalf("current owner cookie name = %q, want IDEA.md value", ownerTokenCookieName)
	}
	fd, ld := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	primaryRaw, _, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: primaryRaw})
	rec := httptest.NewRecorder()
	owner, _, ok := fd.webPrimaryOwner(rec, req)
	if !ok || owner.ID != ownerID {
		t.Fatalf("legacy cookie owner = %v, ok = %v; want %q", owner, ok, ownerID)
	}
	setCookies := strings.Join(rec.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(setCookies, ownerTokenCookieName+"="+primaryRaw) || !strings.Contains(setCookies, "Max-Age="+strconv.Itoa(fd.cfg.Server.Tokens.CookieMaxAgeSeconds())) || !strings.Contains(setCookies, "HttpOnly") || !strings.Contains(setCookies, "Secure") || !strings.Contains(setCookies, "SameSite=Strict") {
		t.Errorf("migration did not issue the current cookie with required attributes: %q", setCookies)
	}
	if !strings.Contains(setCookies, legacyOwnerTokenCookieName+"=; Path=/; Max-Age=0") {
		t.Errorf("migration did not expire the legacy cookie: %q", setCookies)
	}
}

func TestInvalidLegacyOwnerCookieIsCleared(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: "own_invalid"})
	rec := httptest.NewRecorder()
	if _, _, ok := fd.webPrimaryOwner(rec, req); ok {
		t.Fatal("invalid legacy cookie was accepted")
	}
	setCookies := strings.Join(rec.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(setCookies, legacyOwnerTokenCookieName+"=; Path=/; Max-Age=0") {
		t.Errorf("invalid legacy cookie was not cleared: %q", setCookies)
	}
}

func TestRevokedLegacyOwnerCookieIsCleared(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	raw, token, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeToken(context.Background(), ld.sqlDB, token.TokenPrefix, "test revoke"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: raw})
	rec := httptest.NewRecorder()
	if _, _, ok := fd.webPrimaryOwner(rec, req); ok {
		t.Fatal("revoked legacy cookie was accepted")
	}
	setCookies := strings.Join(rec.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(setCookies, legacyOwnerTokenCookieName+"=; Path=/; Max-Age=0") {
		t.Errorf("revoked legacy cookie was not cleared: %q", setCookies)
	}
}

func TestInvalidCurrentOwnerCookieDoesNotFallBackToLegacy(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	legacyRaw, _, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/server/preferences/tokens", nil)
	req.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: "own_invalid"})
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: legacyRaw})
	rec := httptest.NewRecorder()
	if _, _, ok := fd.webPrimaryOwner(rec, req); ok {
		t.Fatal("valid legacy cookie overrode an invalid current cookie")
	}
	setCookies := strings.Join(rec.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(setCookies, ownerTokenCookieName+"=; Path=/; Max-Age=0") || !strings.Contains(setCookies, legacyOwnerTokenCookieName+"=; Path=/; Max-Age=0") {
		t.Errorf("invalid current cookie did not clear both cookie names: %q", setCookies)
	}
}

func TestPrimaryRotationIssuesOnlyCurrentCookieName(t *testing.T) {
	fd, ld := testFrontendDeps(t)
	fd.resolver = NewProxyResolver(nil)
	ownerID, err := db.CreateOwner(context.Background(), ld.sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	primaryRaw, _, err := db.CreatePrimaryToken(context.Background(), ld.sqlDB, ownerID, ld.tokenKey)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/server/preferences/token/rotate", strings.NewReader("confirm=rotate"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: legacyOwnerTokenCookieName, Value: primaryRaw})
	rec := httptest.NewRecorder()
	fd.tokenPrimaryRotateHandler(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/server/preferences/tokens#primary" {
		t.Fatalf("rotation status = %d, body = %q", rec.Code, rec.Body.String())
	}
	var rotatedRaw string
	for _, line := range rec.Header().Values("Set-Cookie") {
		if !strings.HasPrefix(line, ownerTokenCookieName+"=") {
			continue
		}
		value := strings.SplitN(strings.TrimPrefix(line, ownerTokenCookieName+"="), ";", 2)[0]
		if value != "" {
			rotatedRaw = value
		}
	}
	if rotatedRaw == "" || rotatedRaw == primaryRaw {
		t.Fatalf("rotation did not issue a new current cookie value: %q", rec.Header().Values("Set-Cookie"))
	}
	viewSet := false
	for _, cookie := range rec.Result().Cookies() {
		viewSet = viewSet || (cookie.Name == tokenViewCookieName && verifyTokenViewCookie(fd.secretKeys, cookie.Value) == "primary" && cookie.MaxAge == 60)
	}
	if !viewSet {
		t.Fatalf("rotation did not set one-use primary view cookie: %q", rec.Header().Values("Set-Cookie"))
	}
	if !strings.Contains(strings.Join(rec.Header().Values("Set-Cookie"), "\n"), legacyOwnerTokenCookieName+"=; Path=/; Max-Age=0") {
		t.Errorf("rotation did not clear the legacy cookie: %q", rec.Header().Values("Set-Cookie"))
	}
	if _, err := db.LookupTokenByRaw(context.Background(), ld.sqlDB, primaryRaw); err == nil {
		t.Error("rotated-away token remained valid")
	}
	if _, err := db.LookupTokenByRaw(context.Background(), ld.sqlDB, rotatedRaw); err != nil {
		t.Errorf("new rotated token is invalid: %v", err)
	}
}
