package httpserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/db"
)

func testLinkDeps(t *testing.T) (*linkDeps, *sql.DB) {
	t.Helper()
	sqlDB := openTestDB(t)
	if err := db.EnsureSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return &linkDeps{sqlDB: sqlDB, resolver: NewProxyResolver(nil)}, sqlDB
}

func newChiRequest(method, target string, body []byte, slug string) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rctx := chi.NewRouteContext()
	if slug != "" {
		rctx.URLParams.Add("slug", slug)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// wantsText (negotiate.go) treats an empty User-Agent as a
	// non-interactive HTTP client and returns text/plain; tests that need
	// the JSON body set a browser-like default here and override with
	// their own User-Agent when they specifically want the text path.
	req.Header.Set("User-Agent", "Mozilla/5.0")
	return req
}

func TestCreateLinkHandler_AutoCode(t *testing.T) {
	ld, _ := testLinkDeps(t)
	body := []byte(`{"url":"https://example.com/page"}`)
	req := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	ld.createLinkHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK   bool               `json:"ok"`
		Data CreateLinkResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Fatal("expected ok=true")
	}
	if resp.Data.ShortCode == "" {
		t.Error("expected a generated short code")
	}
	if resp.Data.OwnerToken == "" {
		t.Error("expected an owner token")
	}
	if resp.Data.DestinationURL != "https://example.com/page" {
		t.Errorf("destination = %q", resp.Data.DestinationURL)
	}
}

func TestCreateLinkHandler_CustomSlug(t *testing.T) {
	ld, _ := testLinkDeps(t)
	body := []byte(`{"url":"https://example.com","slug":"mylink"}`)
	req := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	rec := httptest.NewRecorder()

	ld.createLinkHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data CreateLinkResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.ShortCode != "mylink" {
		t.Errorf("short_code = %q, want mylink", resp.Data.ShortCode)
	}
}

func TestCreateLinkHandler_DuplicateSlugConflict(t *testing.T) {
	ld, _ := testLinkDeps(t)
	body := []byte(`{"url":"https://example.com","slug":"dup"}`)

	req1 := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	rec1 := httptest.NewRecorder()
	ld.createLinkHandler(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first create status = %d", rec1.Code)
	}

	req2 := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	rec2 := httptest.NewRecorder()
	ld.createLinkHandler(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("second create status = %d, want 409", rec2.Code)
	}
}

func TestCreateLinkHandler_ReservedSlug(t *testing.T) {
	ld, _ := testLinkDeps(t)
	body := []byte(`{"url":"https://example.com","slug":"api"}`)
	req := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	rec := httptest.NewRecorder()

	ld.createLinkHandler(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateLinkHandler_InvalidURL(t *testing.T) {
	ld, _ := testLinkDeps(t)
	body := []byte(`{"url":"not-a-url"}`)
	req := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	rec := httptest.NewRecorder()

	ld.createLinkHandler(rec, req)

	if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want a validation error status; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateLinkHandler_BadJSON(t *testing.T) {
	ld, _ := testLinkDeps(t)
	req := newChiRequest(http.MethodPost, "/api/v1/links", []byte("{not json"), "")
	rec := httptest.NewRecorder()

	ld.createLinkHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateLinkHandler_TextResponse(t *testing.T) {
	ld, _ := testLinkDeps(t)
	body := []byte(`{"url":"https://example.com"}`)
	req := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	req.Header.Set("User-Agent", "curl/8.0")
	rec := httptest.NewRecorder()

	ld.createLinkHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func createTestLink(t *testing.T, ld *linkDeps) (slug, ownerToken string) {
	t.Helper()
	body := []byte(`{"url":"https://example.com/original"}`)
	req := newChiRequest(http.MethodPost, "/api/v1/links", body, "")
	rec := httptest.NewRecorder()
	ld.createLinkHandler(rec, req)
	var resp struct {
		Data CreateLinkResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.Data.ShortCode, resp.Data.OwnerToken
}

func TestGetLinkHandler(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	req := newChiRequest(http.MethodGet, "/api/v1/links/"+slug, nil, slug)
	rec := httptest.NewRecorder()
	ld.getLinkHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK   bool         `json:"ok"`
		Data LinkResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Errorf("ok = false, want true")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\n  \"data\": {") {
		t.Errorf("body is not indented with 2 spaces per PART 14: %s", body)
	}
	if !strings.HasSuffix(body, "}\n") || strings.HasSuffix(body, "\n\n") {
		t.Errorf("body must end with exactly one trailing newline: %q", body)
	}
	if resp.Data.ShortCode != slug {
		t.Errorf("short_code = %q, want %q", resp.Data.ShortCode, slug)
	}
}

func TestGetLinkHandler_NotFound(t *testing.T) {
	ld, _ := testLinkDeps(t)
	req := newChiRequest(http.MethodGet, "/api/v1/links/missing", nil, "missing")
	rec := httptest.NewRecorder()
	ld.getLinkHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListLinksHandler(t *testing.T) {
	ld, _ := testLinkDeps(t)
	var slugs []string
	for i := 0; i < 3; i++ {
		slug, _ := createTestLink(t, ld)
		slugs = append(slugs, slug)
	}

	req := newChiRequest(http.MethodGet, "/api/v1/links", nil, "")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	ld.listLinksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp listLinksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("len(data) = %d, want 3", len(resp.Data))
	}
	if resp.Pagination.Total != 3 {
		t.Errorf("pagination.total = %d, want 3", resp.Pagination.Total)
	}
	if resp.Pagination.Page != 1 || resp.Pagination.Pages != 1 {
		t.Errorf("pagination = %+v, want page=1 pages=1", resp.Pagination)
	}
	// Newest-first: the most recently created link is first.
	if resp.Data[0].ShortCode != slugs[2] {
		t.Errorf("data[0].short_code = %q, want %q (newest first)", resp.Data[0].ShortCode, slugs[2])
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\n  \"data\": [") {
		t.Errorf("body is not indented with 2 spaces per PART 14: %s", body)
	}
}

func TestListLinksHandler_Pagination(t *testing.T) {
	ld, _ := testLinkDeps(t)
	for i := 0; i < 3; i++ {
		createTestLink(t, ld)
	}

	req := newChiRequest(http.MethodGet, "/api/v1/links?page=2&limit=2", nil, "")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	ld.listLinksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp listLinksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(resp.Data))
	}
	if resp.Pagination.Page != 2 || resp.Pagination.Limit != 2 || resp.Pagination.Pages != 2 {
		t.Errorf("pagination = %+v, want page=2 limit=2 pages=2", resp.Pagination)
	}
}

func TestListLinksHandler_TextResponse(t *testing.T) {
	ld, _ := testLinkDeps(t)
	createTestLink(t, ld)

	req := newChiRequest(http.MethodGet, "/api/v1/links", nil, "")
	req.Header.Set("User-Agent", "curl/8.0")
	rec := httptest.NewRecorder()
	ld.listLinksHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain prefix", ct)
	}
	if !strings.Contains(rec.Body.String(), "short_code: ") {
		t.Errorf("body missing short_code field: %s", rec.Body.String())
	}
}

func TestUpdateLinkHandler_WithOwnerToken(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, ownerToken := createTestLink(t, ld)

	body := []byte(`{"url":"https://example.com/updated"}`)
	req := newChiRequest(http.MethodPatch, "/api/v1/links/"+slug, body, slug)
	req.Header.Set("Authorization", ownerToken)
	rec := httptest.NewRecorder()

	ld.updateLinkHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateLinkHandler_Unauthorized(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	body := []byte(`{"url":"https://example.com/updated"}`)
	req := newChiRequest(http.MethodPatch, "/api/v1/links/"+slug, body, slug)
	rec := httptest.NewRecorder()

	ld.updateLinkHandler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestUpdateLinkHandler_OperatorBypass(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	body := []byte(`{"url":"https://example.com/updated"}`)
	req := newChiRequest(http.MethodPatch, "/api/v1/links/"+slug, body, slug)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyOperator, true))
	rec := httptest.NewRecorder()

	ld.updateLinkHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateLinkHandler_ClearExpiry(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, ownerToken := createTestLink(t, ld)

	body := []byte(`{"expires_at":""}`)
	req := newChiRequest(http.MethodPatch, "/api/v1/links/"+slug, body, slug)
	req.Header.Set("Authorization", ownerToken)
	rec := httptest.NewRecorder()

	ld.updateLinkHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateLinkHandler_InvalidExpiresAt(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, ownerToken := createTestLink(t, ld)

	body := []byte(`{"expires_at":"not-a-date"}`)
	req := newChiRequest(http.MethodPatch, "/api/v1/links/"+slug, body, slug)
	req.Header.Set("Authorization", ownerToken)
	rec := httptest.NewRecorder()

	ld.updateLinkHandler(rec, req)
	if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want validation error; body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteLinkHandler(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, ownerToken := createTestLink(t, ld)

	req := newChiRequest(http.MethodDelete, "/api/v1/links/"+slug, nil, slug)
	req.Header.Set("Authorization", ownerToken)
	rec := httptest.NewRecorder()

	ld.deleteLinkHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	req2 := newChiRequest(http.MethodGet, "/api/v1/links/"+slug, nil, slug)
	rec2 := httptest.NewRecorder()
	ld.getLinkHandler(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("after delete status = %d, want 404", rec2.Code)
	}
}

func TestDeleteLinkHandler_Unauthorized(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	req := newChiRequest(http.MethodDelete, "/api/v1/links/"+slug, nil, slug)
	rec := httptest.NewRecorder()
	ld.deleteLinkHandler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestResolveHandler_Redirects(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	req := newChiRequest(http.MethodGet, "/"+slug, nil, slug)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rec := httptest.NewRecorder()

	ld.resolveHandler(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://example.com/original" {
		t.Errorf("Location = %q", loc)
	}
}

func TestResolveHandler_BotSkipsClickTracking(t *testing.T) {
	ld, sqlDB := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	req := newChiRequest(http.MethodGet, "/"+slug, nil, slug)
	req.Header.Set("User-Agent", "Googlebot/2.1")
	rec := httptest.NewRecorder()
	ld.resolveHandler(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	link, err := db.GetLinkByShortCode(context.Background(), sqlDB, slug)
	if err != nil {
		t.Fatalf("GetLinkByShortCode: %v", err)
	}
	if link.ClickCount != 0 {
		t.Errorf("click_count = %d, want 0 for bot UA", link.ClickCount)
	}
}

func TestResolveHandler_NotFound(t *testing.T) {
	ld, _ := testLinkDeps(t)
	req := newChiRequest(http.MethodGet, "/missing", nil, "missing")
	rec := httptest.NewRecorder()
	ld.resolveHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestResolveHandler_Expired(t *testing.T) {
	ld, sqlDB := testLinkDeps(t)
	past := time.Now().Add(-time.Hour)
	link, err := db.CreateLinkCustomSlug(context.Background(), sqlDB, "expired", "https://example.com", &past)
	if err != nil {
		t.Fatalf("CreateLinkCustomSlug: %v", err)
	}

	req := newChiRequest(http.MethodGet, "/"+link.ShortCode, nil, link.ShortCode)
	rec := httptest.NewRecorder()
	ld.resolveHandler(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
}

func TestStatsHandler(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	req := newChiRequest(http.MethodGet, "/"+slug, nil, slug)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://ref.example.com")
	rec := httptest.NewRecorder()
	ld.resolveHandler(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("resolve status = %d", rec.Code)
	}

	statsReq := newChiRequest(http.MethodGet, "/"+slug+"/stats", nil, slug)
	statsRec := httptest.NewRecorder()
	ld.statsHandler(statsRec, statsReq)

	if statsRec.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200; body=%s", statsRec.Code, statsRec.Body.String())
	}
	var envelope struct {
		OK   bool          `json:"ok"`
		Data StatsResponse `json:"data"`
	}
	if err := json.Unmarshal(statsRec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	resp := envelope.Data
	if !envelope.OK {
		t.Errorf("ok = false, want true")
	}
	if resp.TotalClicks != 1 {
		t.Errorf("total_clicks = %d, want 1", resp.TotalClicks)
	}
	if resp.Referrers["https://ref.example.com"] != 1 {
		t.Errorf("referrers = %v", resp.Referrers)
	}
	if len(resp.TimeSeries) != 1 {
		t.Errorf("time_series = %v, want 1 entry", resp.TimeSeries)
	}
}

func TestStatsHandler_TextResponse(t *testing.T) {
	ld, _ := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)

	req := newChiRequest(http.MethodGet, "/"+slug+"/stats", nil, slug)
	req.Header.Set("User-Agent", "curl/8.0")
	rec := httptest.NewRecorder()
	ld.statsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "short_code: "+slug) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestValidateDestinationURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://example.com", true},
		{"http://example.com/path?x=1", true},
		{"", false},
		{"ftp://example.com", false},
		{"not-a-url", false},
		{"https://", false},
	}
	for _, c := range cases {
		_, ok := validateDestinationURL(c.in)
		if ok != c.want {
			t.Errorf("validateDestinationURL(%q) ok = %v, want %v", c.in, ok, c.want)
		}
	}
}

func TestIsBotUserAgent(t *testing.T) {
	cases := map[string]bool{
		"":                         true,
		"Mozilla/5.0":              false,
		"Googlebot/2.1":            true,
		"curl/8.0":                 true,
		"facebookexternalhit/1.1":  true,
		"Mozilla/5.0 (X11; Linux)": false,
	}
	for ua, want := range cases {
		if got := isBotUserAgent(ua); got != want {
			t.Errorf("isBotUserAgent(%q) = %v, want %v", ua, got, want)
		}
	}
}

func TestCorsAPIMiddleware(t *testing.T) {
	handler := corsAPIMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/links", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}

	optReq := httptest.NewRequest(http.MethodOptions, "/api/v1/links", nil)
	optRec := httptest.NewRecorder()
	handler.ServeHTTP(optRec, optReq)
	if optRec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", optRec.Code)
	}
}

func TestValidateDestinationURLLength(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", maxDestinationURLLen)
	if _, ok := validateDestinationURL(long); ok {
		t.Errorf("validateDestinationURL(%d bytes) ok = true, want false", len(long))
	}

	atLimit := "https://example.com/" + strings.Repeat("a", maxDestinationURLLen-len("https://example.com/"))
	if _, ok := validateDestinationURL(atLimit); !ok {
		t.Errorf("validateDestinationURL(%d bytes) ok = false, want true", len(atLimit))
	}
}
