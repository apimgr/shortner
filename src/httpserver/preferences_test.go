package httpserver

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/common/i18n"
)

func TestPreferenceCodeAcceptsQueryURLAndBase64(t *testing.T) {
	query := "lang=fr&theme=light"
	for _, code := range []string{
		query,
		"https://example.test/server/preferences/sync/import?" + query,
		base64.RawURLEncoding.EncodeToString([]byte(query)),
		base64.URLEncoding.EncodeToString([]byte(query)),
	} {
		values := decodePreferenceCode(code)
		if values.Get("lang") != "fr" || values.Get("theme") != "light" {
			t.Errorf("decodePreferenceCode(%q) = %v", code, values)
		}
	}
}

func TestPreferencePageRegistryHasRoutableHubEntries(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	router := chi.NewRouter()
	registerPreferencePages(router, fd, fd.cfg)
	entries := registeredPreferencePages(fd.cfg, "en")
	if len(entries) != 2 {
		t.Fatalf("registered preference pages = %d, want tokens and sync", len(entries))
	}
	for _, entry := range entries {
		ctx := chi.NewRouteContext()
		if !router.Match(ctx, http.MethodGet, entry.Path) {
			t.Errorf("registered page %q has no GET route", entry.Path)
		}
		if entry.Title == "" || entry.Description == "" {
			t.Errorf("registered page %q has untranslated hub text: %+v", entry.Path, entry)
		}
	}
	if entries[0].Path != "/server/preferences/tokens" || entries[1].Path != "/server/preferences/sync" {
		t.Fatalf("registered preference page order = %+v", entries)
	}
}

func TestRemovedPreferenceImportAndExportRoutesAreNotRegistered(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	router := chi.NewRouter()
	router.Get("/server/preferences", fd.preferencesHandler)
	registerPreferencePages(router, fd, fd.cfg)
	for _, path := range []string{"/server/preferences/export", "/server/preferences/import"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("legacy preference route %q status = %d, want 404", path, rec.Code)
		}
	}
}

func TestHelpPageRegistryHasRoutableHubEntries(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	router := chi.NewRouter()
	registerHelpPages(router, fd, fd.cfg)
	entries := registeredHelpPages(fd.cfg, "en")
	if len(entries) != 1 || entries[0].Path != "/server/help/mcp" {
		t.Fatalf("registered help pages = %+v, want MCP help", entries)
	}
	for _, entry := range entries {
		ctx := chi.NewRouteContext()
		if !router.Match(ctx, http.MethodGet, entry.Path) {
			t.Errorf("registered help page %q has no GET route", entry.Path)
		}
		if entry.Title == "" || entry.Description == "" {
			t.Errorf("registered help page %q has untranslated hub text: %+v", entry.Path, entry)
		}
	}
}

func TestPreferenceSyncRedirectRejectsExternalAndSaveRoutes(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/server/preferences/sync", nil)
	for _, unsafe := range []string{"//evil.example", "/\\evil", "https://evil.example", "/server/preferences", "/server/preferences/sync", "/server/preferences/sync/import?theme=light"} {
		if got := preferenceRedirectTarget(unsafe, "", req); got != "/" {
			t.Errorf("preferenceRedirectTarget(%q) = %q", unsafe, got)
		}
	}
	req.Host = "example.test"
	if got := preferenceRedirectTarget("", "https://example.test/server/help?q=ok", req); got != "/server/help?q=ok" {
		t.Errorf("same-origin referer redirect = %q", got)
	}
	if got := preferenceRedirectTarget("", "https://evil.test/server/help", req); got != "/" {
		t.Errorf("external referer redirect = %q", got)
	}
}

func TestPreferenceSyncPostAppliesCodeAndDirectValuesWin(t *testing.T) {
	fd := &frontendDeps{resolver: NewProxyResolver(nil)}
	form := url.Values{
		"code":     {"theme=dark&lang=fr"},
		"theme":    {"light"},
		"redirect": {"/"},
	}
	req := httptest.NewRequest(http.MethodPost, "/server/preferences/sync", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	fd.preferenceSyncPost(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/server/preferences/sync" {
		t.Fatalf("sync POST response = %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	values := map[string]string{}
	for _, cookie := range rec.Result().Cookies() {
		values[cookie.Name] = cookie.Value
	}
	if values["theme"] != "light" || values["lang"] != "fr" {
		t.Fatalf("sync POST cookies = %#v", values)
	}
}

func TestPreferenceSyncImportDirectParametersOverrideCode(t *testing.T) {
	fd := &frontendDeps{resolver: NewProxyResolver(nil)}
	code := base64.RawURLEncoding.EncodeToString([]byte("theme=dark&lang=fr"))
	req := httptest.NewRequest(http.MethodGet, "/server/preferences/sync/import?code="+url.QueryEscape(code)+"&theme=light", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	fd.preferenceSyncImportHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync import status = %d, want 200", rec.Code)
	}
	values := map[string]string{}
	for _, cookie := range rec.Result().Cookies() {
		values[cookie.Name] = cookie.Value
	}
	if values["theme"] != "light" || values["lang"] != "fr" {
		t.Fatalf("sync import cookies = %#v", values)
	}
}

func TestDecodePreferenceCodeRejectsOversizeAndMalformed(t *testing.T) {
	if got := decodePreferenceCode(strings.Repeat("a", 2049)); len(got) != 0 {
		t.Fatalf("oversized code decoded to %v", got)
	}
	if got := decodePreferenceCode("%%%bad"); len(got) != 0 {
		t.Fatalf("malformed code decoded to %v", got)
	}
}

func TestCanonicalPreferenceQueryIsSortedAndExcludesBadValues(t *testing.T) {
	got := canonicalPreferenceQuery(preferenceSnapshot{
		Theme: "dark", Lang: "en", Prefs: map[string]string{"z": "last", "a": "first", "bad/name": "ignored"},
	})
	if got != "lang=en&shortner_pref_a=first&shortner_pref_z=last&theme=dark" {
		t.Fatalf("canonical query = %q", got)
	}
}

func TestPreferenceValidationAndRedirect(t *testing.T) {
	for _, test := range []struct {
		key, value string
		ok         bool
	}{
		{"theme", "auto", true}, {"theme", "blue", false},
		{"lang", "fr-CA", true}, {"lang", "auto", true}, {"lang", "xx", false},
		{"shortner_pref_sort", "name", true}, {"shortner_pref_sort", "<script>", false},
		{"cookie_consent", "yes", false},
	} {
		_, _, ok := validatePreference(test.key, test.value)
		if ok != test.ok {
			t.Errorf("validatePreference(%q, %q) ok = %v, want %v", test.key, test.value, ok, test.ok)
		}
	}
	for _, unsafe := range []string{"//evil.example", "/\\evil.example", "https://evil.example/path", "/safe\r\nLocation: https://evil"} {
		if got := safePreferenceRedirect(unsafe, ""); got != "/" {
			t.Errorf("safePreferenceRedirect(%q) = %q", unsafe, got)
		}
	}
	if got := safePreferenceRedirect("/server/help?q=1", ""); got != "/server/help?q=1" {
		t.Errorf("safe local redirect = %q", got)
	}
}

func TestApplyAutomaticLanguageExpiresOverrideCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/server/preferences", nil)
	req.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "fr"})
	rec := httptest.NewRecorder()
	result := applyPreferences(rec, req, url.Values{"lang": {"auto"}})
	if !result.State.LangAutomatic {
		t.Fatalf("automatic language selection state = %+v", result.State)
	}
	var found bool
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == i18n.CookieName {
			found = true
			if cookie.MaxAge != -1 || cookie.Value != "" {
				t.Fatalf("language override cookie was not expired: %+v", cookie)
			}
		}
	}
	if !found {
		t.Fatal("language override cookie was not cleared")
	}
}

func TestApplyPreferencesSetsCookiesAndPreservesUnmentionedValues(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/server/preferences", nil)
	req.AddCookie(&http.Cookie{Name: "shortner_pref_sort", Value: "name"})
	values := url.Values{"theme": {"light"}, "lang": {"de"}, "shortner_pref_layout": {"compact"}, "cookie_consent": {"forbidden"}}
	rec := httptest.NewRecorder()
	result := applyPreferences(rec, req, values)
	if result.State.Theme != "light" || result.State.Lang != "de" || result.State.Prefs["sort"] != "name" || result.State.Prefs["layout"] != "compact" {
		t.Fatalf("result state = %+v", result.State)
	}
	if len(result.Ignored) != 1 || result.Ignored[0] != "cookie_consent" {
		t.Fatalf("ignored = %v", result.Ignored)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 3 {
		t.Fatalf("cookies = %+v, want exactly the three submitted preference cookies", cookies)
	}
	for _, cookie := range cookies {
		if cookie.Path != "/" || cookie.MaxAge != preferenceCookieMaxAge || cookie.SameSite != http.SameSiteLaxMode || cookie.HttpOnly {
			t.Errorf("preference cookie attributes = %+v", cookie)
		}
	}
}

func TestPreferenceCookieSecureUsesResolvedProxyScheme(t *testing.T) {
	resolver := NewProxyResolver(nil)
	req := httptest.NewRequest(http.MethodPost, "/server/preferences", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.RemoteAddr = "127.0.0.1:10000"
	req = preferenceRequest(req, resolver)
	rec := httptest.NewRecorder()
	setPreferenceCookie(rec, req, "theme", "dark")
	cookie := rec.Result().Cookies()[0]
	if !cookie.Secure {
		t.Fatal("trusted TLS-terminating proxy request did not set Secure")
	}

	untrusted := httptest.NewRequest(http.MethodPost, "/server/preferences", nil)
	untrusted.Header.Set("X-Forwarded-Proto", "https")
	untrusted.RemoteAddr = "203.0.113.5:10000"
	untrusted = preferenceRequest(untrusted, resolver)
	other := httptest.NewRecorder()
	setPreferenceCookie(other, untrusted, "theme", "dark")
	if other.Result().Cookies()[0].Secure {
		t.Fatal("untrusted forwarded proto enabled Secure")
	}
}

func TestResetPreferenceCookiesClearsAllAppCookiesButNotCredentials(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/server/preferences", nil)
	for _, name := range []string{
		"theme", "lang", consentCookieName, "ccpa_opt_out", "shortner_build",
		"dismissed_announcements", "shortner_pref_sort", "owner_token", "csrf_token",
	} {
		req.AddCookie(&http.Cookie{Name: name, Value: "saved"})
	}
	rec := httptest.NewRecorder()
	resetPreferenceCookies(rec, req)

	deleted := map[string]bool{}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.MaxAge < 0 {
			deleted[cookie.Name] = true
		}
	}
	for _, name := range []string{
		"theme", "lang", consentCookieName, "ccpa_opt_out", "shortner_build",
		"dismissed_announcements", "shortner_pref_sort",
	} {
		if !deleted[name] {
			t.Errorf("reset did not clear %q", name)
		}
	}
	if deleted["owner_token"] || deleted["csrf_token"] {
		t.Fatal("reset cleared a credential cookie")
	}
}
