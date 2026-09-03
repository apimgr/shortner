package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestIsValidTheme(t *testing.T) {
	tests := []struct {
		theme string
		want  bool
	}{
		{"dark", true},
		{"light", true},
		{"auto", true},
		{"", false},
		{"blue", false},
	}
	for _, tt := range tests {
		if got := isValidTheme(tt.theme); got != tt.want {
			t.Errorf("isValidTheme(%q) = %v, want %v", tt.theme, got, tt.want)
		}
	}
}

func TestRequestTheme(t *testing.T) {
	fd, _ := testFrontendDeps(t)

	t.Run("no cookie falls back to config default", func(t *testing.T) {
		req := htmlRequest(http.MethodGet, "/", nil)
		if got := requestTheme(req, fd.cfg); got != fd.cfg.Web.Theme {
			t.Errorf("requestTheme() = %q, want config default %q", got, fd.cfg.Web.Theme)
		}
	})

	t.Run("valid cookie wins over config default", func(t *testing.T) {
		req := htmlRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: themeCookieName, Value: "light"})
		if got := requestTheme(req, fd.cfg); got != "light" {
			t.Errorf("requestTheme() = %q, want %q", got, "light")
		}
	})

	t.Run("invalid cookie falls back to config default", func(t *testing.T) {
		req := htmlRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: themeCookieName, Value: "bogus"})
		if got := requestTheme(req, fd.cfg); got != fd.cfg.Web.Theme {
			t.Errorf("requestTheme() = %q, want config default %q", got, fd.cfg.Web.Theme)
		}
	})

	t.Run("invalid config default falls back to dark", func(t *testing.T) {
		req := htmlRequest(http.MethodGet, "/", nil)
		orig := fd.cfg.Web.Theme
		fd.cfg.Web.Theme = "bogus"
		defer func() { fd.cfg.Web.Theme = orig }()
		if got := requestTheme(req, fd.cfg); got != "dark" {
			t.Errorf("requestTheme() = %q, want %q", got, "dark")
		}
	})
}

func TestThemeHandler_SetsCookieAndRedirects(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{"theme": {"light"}, "return_to": {"/server/help"}}
	rec := httptest.NewRecorder()
	fd.themeHandler(rec, htmlRequest(http.MethodPost, "/server/theme", form))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/server/help" {
		t.Errorf("Location = %q, want %q", got, "/server/help")
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == themeCookieName {
			found = true
			if c.Value != "light" {
				t.Errorf("cookie value = %q, want %q", c.Value, "light")
			}
		}
	}
	if !found {
		t.Fatal("expected theme cookie to be set")
	}
}

func TestThemeHandler_InvalidThemeSkipsCookie(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{"theme": {"bogus"}}
	rec := httptest.NewRecorder()
	fd.themeHandler(rec, htmlRequest(http.MethodPost, "/server/theme", form))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == themeCookieName {
			t.Fatal("expected no theme cookie to be set for an invalid theme value")
		}
	}
}

func TestHomeHandler_RendersThemeClassFromCookie(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	req := htmlRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: themeCookieName, Value: "light"})
	rec := httptest.NewRecorder()
	fd.homeHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `class="theme-light"`) {
		t.Errorf("expected html class=\"theme-light\" in body, got: %s", rec.Body.String())
	}
}

func TestThemeHandler_RejectsOpenRedirect(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	form := url.Values{"theme": {"dark"}, "return_to": {"//evil.example.com"}}
	rec := httptest.NewRecorder()
	fd.themeHandler(rec, htmlRequest(http.MethodPost, "/server/theme", form))

	if got := rec.Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want %q", got, "/")
	}
}
