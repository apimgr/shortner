package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStateChangingMethod covers every mutating verb plus GET/HEAD, which
// must never be treated as state-changing.
func TestStateChangingMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{http.MethodPost, true},
		{http.MethodPut, true},
		{http.MethodPatch, true},
		{http.MethodDelete, true},
		{http.MethodGet, false},
		{http.MethodHead, false},
		{http.MethodOptions, false},
	}
	for _, tt := range tests {
		if got := stateChangingMethod(tt.method); got != tt.want {
			t.Errorf("stateChangingMethod(%q) = %v, want %v", tt.method, got, tt.want)
		}
	}
}

// TestIsCSRFExempt covers a prefix match and a non-matching path.
func TestIsCSRFExempt(t *testing.T) {
	if !isCSRFExempt("/api/v1/links") {
		t.Error("expected /api/v1/links to be CSRF-exempt")
	}
	if isCSRFExempt("/server/consent") {
		t.Error("expected /server/consent to not be CSRF-exempt")
	}
}

// TestSecFetchMiddlewareDisabled proves the whole check is skipped when
// SecFetchValidation is off, even for a cross-site POST.
func TestSecFetchMiddlewareDisabled(t *testing.T) {
	d := testDeps(t)
	d.cfgHeaders.SecFetchValidation = false

	h := d.secFetchMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/server/consent", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when validation is disabled", rec.Code)
	}
}

// TestSecFetchMiddlewareGetBypasses proves a non-state-changing method
// never triggers validation, regardless of Sec-Fetch-Site.
func TestSecFetchMiddlewareGetBypasses(t *testing.T) {
	d := testDeps(t)
	d.cfgHeaders.SecFetchValidation = true

	h := d.secFetchMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/server/consent", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a GET regardless of Sec-Fetch-Site", rec.Code)
	}
}

// TestSecFetchMiddlewareBlocksCrossSitePost proves a cross-site,
// non-exempt, non-token POST is rejected.
func TestSecFetchMiddlewareBlocksCrossSitePost(t *testing.T) {
	d := testDeps(t)
	d.cfgHeaders.SecFetchValidation = true

	h := d.secFetchMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/server/consent", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a cross-site POST", rec.Code)
	}
}

// TestSecFetchMiddlewareAllowsCrossSiteWithBearerToken proves a Bearer
// token exempts the request from the Sec-Fetch-Site cross-site rejection,
// since a cross-site form POST cannot carry it.
func TestSecFetchMiddlewareAllowsCrossSiteWithBearerToken(t *testing.T) {
	d := testDeps(t)
	d.cfgHeaders.SecFetchValidation = true

	h := d.secFetchMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/server/consent", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Authorization", "Bearer tok_something")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a cross-site request carrying a bearer token", rec.Code)
	}
}

// TestSecFetchMiddlewareAllowsCrossSiteOnExemptPath proves the CSRF
// exempt-path list also exempts the Sec-Fetch-Site check.
func TestSecFetchMiddlewareAllowsCrossSiteOnExemptPath(t *testing.T) {
	d := testDeps(t)
	d.cfgHeaders.SecFetchValidation = true

	h := d.secFetchMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a cross-site request on an exempt path", rec.Code)
	}
}

// TestSecFetchMiddlewareBlocksNavigateModeOnAPI proves a form-based
// navigation to an API URL is rejected outright, per the navigation-CSRF
// guard, even when Sec-Fetch-Site is same-origin.
func TestSecFetchMiddlewareBlocksNavigateModeOnAPI(t *testing.T) {
	d := testDeps(t)
	d.cfgHeaders.SecFetchValidation = true

	h := d.secFetchMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a navigate-mode POST to an API URL", rec.Code)
	}
}

// TestSecFetchMiddlewareAllowsAbsentHeaders proves that missing Fetch
// Metadata headers (older browsers) fall through as a legacy pass-through.
func TestSecFetchMiddlewareAllowsAbsentHeaders(t *testing.T) {
	d := testDeps(t)
	d.cfgHeaders.SecFetchValidation = true

	h := d.secFetchMiddleware(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/server/consent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when Fetch Metadata headers are entirely absent", rec.Code)
	}
}
