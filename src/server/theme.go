// Package server: theme cookie resolution and the no-JS theme-switch
// endpoint, per AI.md PART 16 "Themes (NON-NEGOTIABLE - PROJECT-WIDE)" ->
// "Theme Detection Flow" and "Theme Switching".
package server

import (
	"net/http"

	"github.com/apimgr/shortner/src/config"
)

// themeCookieName is the server-readable per-visitor theme preference, per
// AI.md PART 16 "Critical Theme Rules": "User preference persisted in the
// theme cookie (server-readable — renders the class on <html>)".
const themeCookieName = "theme"

// isValidTheme reports whether v is one of the three required themes.
func isValidTheme(v string) bool {
	switch v {
	case "dark", "light", "auto":
		return true
	}
	return false
}

// requestTheme resolves the theme to render for r, per PART 16's "Theme
// Detection Flow": the theme cookie wins when present and valid, then the
// operator's configured default, then "dark" (step 4: "Default to dark if
// all detection fails").
func requestTheme(r *http.Request, cfg *config.Config) string {
	return requestThemeDefault(r, cfg.Web.UI.Theme)
}

func requestThemeDefault(r *http.Request, defaultTheme string) string {
	if c, err := r.Cookie(themeCookieName); readConsent(r).Preferences && err == nil && isValidTheme(c.Value) {
		return c.Value
	}
	if isValidTheme(defaultTheme) {
		return defaultTheme
	}
	return "dark"
}

// themeHandler is the no-JS fallback theme switch — per PART 16: "No-JS
// visitors get correct auto theming from pure CSS and can switch via a
// <noscript> form POSTing to the theme endpoint." The same-origin JS
// toggle (app.js) uses this endpoint too when it wants a full navigation,
// but normally sets the cookie and swaps the <html> class directly for a
// reload-free switch — see PART 16 "Theme Switching": "NO page reload
// required".
func (fd *frontendDeps) themeHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	theme := r.FormValue("theme")
	if isValidTheme(theme) {
		if readConsent(r).Preferences {
			setPreferenceCookie(w, preferenceRequest(r, fd.resolver), themeCookieName, theme)
		} else {
			dest := r.FormValue("return_to")
			if dest == "" {
				dest = r.Referer()
			}
			if !isSafeLocalRedirect(dest) {
				dest = "/"
			}
			http.Redirect(w, r, withConsentRequired(dest), http.StatusSeeOther)
			return
		}
	}

	dest := r.FormValue("return_to")
	if dest == "" {
		dest = r.Referer()
	}
	if !isSafeLocalRedirect(dest) {
		dest = "/"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
