package i18n

import (
	"net/http"
	"strconv"
	"strings"
)

// CookieName is AI.md PART 30's default language cookie name; the operator
// can override it via server.i18n.cookie_name.
const CookieName = "lang"

// CookieMaxAge is the default lifetime of the language cookie (one year in
// seconds), per AI.md PART 30 "Language Selection via Query Parameter".
const CookieMaxAge = 365 * 24 * 60 * 60

// LangFromRequest resolves the language for r using AI.md PART 30's
// priority order: `?lang=` query parameter, then the language cookie, then
// the Accept-Language header, then the supplied default. Every step is
// validated against the embedded locales, so an unsupported value at any
// level falls through rather than erroring.
func LangFromRequest(r *http.Request, cookieName, defaultLang string) string {
	if cookieName == "" {
		cookieName = CookieName
	}
	if q := r.URL.Query().Get("lang"); q != "" {
		if code := Normalize(q); IsSupported(code) {
			return code
		}
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		if code := Normalize(c.Value); IsSupported(code) {
			return code
		}
	}
	if code := FromAcceptLanguage(r.Header.Get("Accept-Language")); code != "" {
		return code
	}
	if code := Normalize(defaultLang); IsSupported(code) {
		return code
	}
	return DefaultLanguage
}

// QueryLang returns the supported language explicitly requested via
// `?lang=`, or "" when the query parameter is absent or unsupported. The
// middleware uses it to decide when to persist the choice as a cookie.
func QueryLang(r *http.Request) string {
	q := r.URL.Query().Get("lang")
	if q == "" {
		return ""
	}
	if code := Normalize(q); IsSupported(code) {
		return code
	}
	return ""
}

// FromAcceptLanguage parses an RFC 7231 Accept-Language header and returns
// the highest-quality supported language, or "" when none match. Entries
// with q=0 are explicitly rejected by the client and never selected.
func FromAcceptLanguage(header string) string {
	best := ""
	bestQ := -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag := part
		q := 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			for _, param := range strings.Split(part[i+1:], ";") {
				param = strings.TrimSpace(param)
				if !strings.HasPrefix(param, "q=") {
					continue
				}
				if parsed, err := strconv.ParseFloat(strings.TrimPrefix(param, "q="), 64); err == nil {
					q = parsed
				}
			}
		}
		if tag == "*" || q <= 0 {
			continue
		}
		code := Normalize(tag)
		if !IsSupported(code) {
			continue
		}
		if q > bestQ {
			best, bestQ = code, q
		}
	}
	return best
}

// SetLanguageCookie persists the visitor's explicit language choice, per
// AI.md PART 30: path "/", one-year lifetime, SameSite=Lax, HttpOnly, and
// Secure only on an HTTPS request (an overlay address is always plain
// HTTP, where a Secure cookie would simply be dropped).
func SetLanguageCookie(w http.ResponseWriter, r *http.Request, cookieName, lang string, maxAge int) {
	if cookieName == "" {
		cookieName = CookieName
	}
	if maxAge <= 0 {
		maxAge = CookieMaxAge
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    Resolve(lang),
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}
