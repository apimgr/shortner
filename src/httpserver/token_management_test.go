package httpserver

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
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
	restoreResponse := httptest.NewRecorder()
	fd.tokenRestoreHandler(restoreResponse, restoreRequest)
	if restoreResponse.Code != http.StatusSeeOther {
		t.Fatalf("restore status = %d, body = %q", restoreResponse.Code, restoreResponse.Body.String())
	}
	setCookie := restoreResponse.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, ownerTokenCookieName+"="+primaryRaw) || !strings.Contains(setCookie, "HttpOnly") || !strings.Contains(setCookie, "Secure") || !strings.Contains(setCookie, "SameSite=Strict") {
		t.Errorf("restore cookie missing required attributes: %q", setCookie)
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
	if revealResponse.Code != http.StatusOK || !strings.Contains(revealResponse.Body.String(), primaryRaw) {
		t.Fatalf("primary reveal status/body did not show the token: %d, %s", revealResponse.Code, revealResponse.Body.String())
	}
	if got := revealResponse.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("reveal Cache-Control = %q, want no-store", got)
	}

	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/tokens", nil)
	apiRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: primaryRaw})
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
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Use an existing token") {
		t.Fatalf("token route status/body = %d, %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Set-Cookie") == "" {
		t.Error("page GET did not issue the CSRF cookie required by its forms")
	}
}
