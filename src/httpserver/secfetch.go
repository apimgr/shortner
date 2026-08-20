// Fetch Metadata request validation, per AI.md PART 11 "Sec-Fetch-*
// Request Validation".
package httpserver

import (
	"net/http"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
)

// stateChangingMethod reports whether the method mutates server state and
// therefore gets Fetch Metadata validation, per AI.md PART 11's
// "Validated on" column: POST/PUT/PATCH/DELETE.
func stateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// secFetchMiddleware rejects cross-site and navigation-form state changes
// that a browser has labelled with Fetch Metadata headers, per AI.md
// PART 11 "Sec-Fetch-* Request Validation".
//
// Validation is present-and-bad reject only: older browsers omit these
// headers entirely, and their absence is treated as a legacy pass-through
// that falls through to the CSRF token check — which still runs either
// way, per the spec's defense-in-depth note.
func (d *deps) secFetchMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.cfgHeaders.SecFetchValidation || !stateChangingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		// A Bearer/API token is not carried by a cross-site form POST, so
		// its presence rules out the browser-driven CSRF class this layer
		// defends against.
		hasToken := ExtractToken(r) != ""

		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" && !hasToken && !isCSRFExempt(r.URL.Path) {
			sendError(w, r, apperr.New(apperr.CodeForbidden))
			return
		}

		// GET/HEAD navigation to an API URL is allowed and never reaches
		// here — only state-changers can be a form-based navigation CSRF.
		if r.Header.Get("Sec-Fetch-Mode") == "navigate" && strings.HasPrefix(r.URL.Path, "/api/") {
			sendError(w, r, apperr.New(apperr.CodeForbidden))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isCSRFExempt reports whether path is covered by the CSRF exempt-path
// list the Sec-Fetch-Site rule defers to.
func isCSRFExempt(path string) bool {
	for _, prefix := range csrfExemptPaths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
