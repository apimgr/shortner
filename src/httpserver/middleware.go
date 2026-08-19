// The PART 12 middleware chain, wired in the exact order specified by
// AI.md PART 5 "Middleware Order": URLNormalize(1) -> RequestID(2) ->
// PathSecurity(3) -> SecurityHeaders(4) -> Allowlist(5) -> Blocklist(6) ->
// RateLimit(7) -> GeoIP(8) -> Auth(9) -> Logging(10).
//
// Allowlist, Blocklist, and GeoIP are real, wired, pass-through stages:
// their backing data (PART 11 allowlist/blocklist config, PART 19 GeoIP
// database) doesn't exist yet, so they perform no filtering but still run
// and set the request context flags their eventual bodies will need — see
// TODO.AI.md.
package httpserver

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/security"
)

// deps bundles the shared state every middleware needs, built once by
// New() and closed over by each middleware function.
type deps struct {
	resolver    *ProxyResolver
	rateLimiter *RateLimiter
	stats       *Stats
	access      *applog.Logger
	operatorTok string
	cors        config.CORS
	csrf        config.CSRF
}

// setupMiddleware wraps handler with the full PART 12 chain, per AI.md
// PART 5 "Middleware Order". Wrapping order is reversed from execution
// order: the last middleware applied here runs first.
func (d *deps) setupMiddleware(handler http.Handler) http.Handler {
	handler = d.loggingMiddleware(handler) // 10
	handler = d.authMiddleware(handler)    // 9
	// CSRF is not one of PART 5's ten numbered stages. It is placed after
	// RateLimit(7)/GeoIP(8) so that a flood of forged form posts is
	// throttled before any request body is parsed; running it ahead of the
	// chain would make body parsing reachable without a rate-limit check.
	handler = d.csrfMiddleware(handler)
	handler = d.geoIPMiddleware(handler)         // 8
	handler = d.rateLimitMiddleware(handler)     // 7
	handler = d.blocklistMiddleware(handler)     // 6
	handler = d.allowlistMiddleware(handler)     // 5
	handler = securityHeadersMiddleware(handler) // 4
	handler = pathSecurityMiddleware(handler)    // 3
	handler = requestIDMiddleware(handler)       // 2
	handler = urlNormalizeMiddleware(handler)    // 1
	// CORS stays outermost so its headers are present on every response,
	// including ones short-circuited by an earlier stage (429, 403).
	handler = d.corsMiddleware(handler)
	return handler
}

// repeatedSlashes matches two or more consecutive "/" for URLNormalize.
var repeatedSlashes = regexp.MustCompile(`/{2,}`)

// urlNormalizeMiddleware collapses repeated slashes and strips a trailing
// slash (execution position 1), per AI.md PART 5 "Middleware Order" and
// PART 16 "URL Normalization Middleware": the root path and paths that look
// like a static file (contain a "." in the final segment) are exempt from
// trailing-slash stripping. A redirect preserves the query string and uses
// 301 (permanent), matching the spec's canonicalization intent.
func urlNormalizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.Contains(p, "//") {
			cleaned := repeatedSlashes.ReplaceAllString(p, "/")
			if cleaned != "/" && strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
				cleaned += "/"
			}
			p = cleaned
		}

		if p != "/" && strings.HasSuffix(p, "/") {
			trimmed := strings.TrimRight(p, "/")
			if trimmed == "" {
				trimmed = "/"
			}
			last := trimmed
			if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
				last = trimmed[idx+1:]
			}
			if !strings.Contains(last, ".") {
				dest := trimmed
				if r.URL.RawQuery != "" {
					dest += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, dest, http.StatusMovedPermanently)
				return
			}
			p = trimmed
		}

		r.URL.Path = p
		next.ServeHTTP(w, r)
	})
}

// csrfExemptPaths are always bypassed regardless of config, since they are
// consumed by non-browser clients that never carry the csrf_token cookie.
var csrfExemptPaths = []string{"/api/"}

// csrfMiddleware implements the double-submit-cookie CSRF pattern, per
// AI.md PART 16 "CSRF Protection". State-changing requests (POST/PUT/PATCH/
// DELETE) authenticated with a bearer/API token (never carried by a
// browser form) bypass the check, as do GET/HEAD/OPTIONS, WebSocket
// upgrades, and configured/explicit exempt paths.
func (d *deps) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.csrf.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			ensureCSRFCookie(w, r, d.csrf)
			next.ServeHTTP(w, r)
			return
		}

		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}
		if ExtractToken(r) != "" {
			// Bearer/API-token auth never carries the CSRF cookie; a
			// forged cross-site request can't read/send it either.
			next.ServeHTTP(w, r)
			return
		}
		for _, p := range csrfExemptPaths {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}
		for _, p := range d.csrf.ExemptPaths {
			if p != "" && strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}

		cookieName := d.csrf.CookieName
		if cookieName == "" {
			cookieName = "csrf_token"
		}
		headerName := d.csrf.HeaderName
		if headerName == "" {
			headerName = "X-CSRF-Token"
		}

		cookie, err := r.Cookie(cookieName)
		if err != nil || cookie.Value == "" {
			d.logCSRFFailure(r, "missing cookie")
			apperr.SendError(w, apperr.New(apperr.CodeCSRFFailed))
			return
		}

		submitted := r.Header.Get(headerName)
		if submitted == "" {
			submitted = r.FormValue("csrf_token")
		}
		if submitted == "" || subtle.ConstantTimeCompare([]byte(submitted), []byte(cookie.Value)) != 1 {
			d.logCSRFFailure(r, "token mismatch")
			apperr.SendError(w, apperr.New(apperr.CodeCSRFFailed))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (d *deps) logCSRFFailure(r *http.Request, reason string) {
	if d.access == nil {
		return
	}
	_ = d.access.WriteLine(applog.LevelWarn, "csrf_failure ip="+d.resolver.ResolveClientIP(r)+
		" path="+r.URL.Path+" reason="+reason)
}

// ensureCSRFCookie issues a csrf_token cookie on a safe (GET/HEAD/OPTIONS)
// request if one isn't already set, so a subsequent form POST/JS request on
// the same origin has a token to submit. SameSite=Strict and NOT HttpOnly
// (JS needs to read it to set the request header) per AI.md PART 16.
func ensureCSRFCookie(w http.ResponseWriter, r *http.Request, csrf config.CSRF) {
	cookieName := csrf.CookieName
	if cookieName == "" {
		cookieName = "csrf_token"
	}
	if _, err := r.Cookie(cookieName); err == nil {
		return
	}

	length := csrf.TokenLength
	if length <= 0 {
		length = 32
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return
	}
	token := hex.EncodeToString(buf)

	secure := csrf.Secure == "true" || (csrf.Secure == "auto" && r.TLS != nil)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		HttpOnly: false,
	})
}

// corsMiddleware sets frontend CORS headers per AI.md PART 16 "CORS".
// Access-Control-Allow-Origin is never "*" when credentials are allowed;
// Allow-Headers enumerates the exact header set AuthMiddleware/ExtractToken
// accept, never a wildcard.
func (d *deps) corsMiddleware(next http.Handler) http.Handler {
	allowHeaders := "Authorization, X-API-Key, API-Key, X-Auth-Token, " +
		"X-Access-Token, X-Token, Token, Content-Type, X-CSRF-Token, X-Request-ID"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			h := w.Header()
			allowed, explicit := d.originAllowed(origin)
			if allowed {
				if explicit {
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Vary", "Origin")
					if d.cors.AllowCredentials {
						h.Set("Access-Control-Allow-Credentials", "true")
					}
				} else {
					h.Set("Access-Control-Allow-Origin", "*")
				}
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", allowHeaders)
				if d.cors.MaxAge > 0 {
					h.Set("Access-Control-Max-Age", strconv.Itoa(d.cors.MaxAge))
				}
			}
		}

		if r.Method == http.MethodOptions && origin != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether origin may receive CORS headers, and
// whether the match was an explicit origin (vs. the "*" wildcard) — an
// explicit match is required before Allow-Credentials can ever be set.
func (d *deps) originAllowed(origin string) (allowed, explicit bool) {
	if len(d.cors.AllowedOrigins) == 0 {
		return true, false
	}
	for _, o := range d.cors.AllowedOrigins {
		if o == "*" {
			if d.cors.AllowCredentials {
				continue // "*" never pairs with credentials; skip to an explicit match
			}
			return true, false
		}
		if strings.EqualFold(o, origin) {
			return true, true
		}
	}
	return false, false
}

// requestIDMiddleware attaches a request ID (execution position 2), per
// AI.md PART 8 "Request ID Handling": use the client/upstream-supplied
// X-Request-ID / X-Correlation-ID / X-Trace-ID if present and a valid
// UUID, otherwise generate a new UUID v4.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = r.Header.Get("X-Correlation-ID")
		}
		if requestID == "" {
			requestID = r.Header.Get("X-Trace-ID")
		}
		if requestID == "" || uuid.Validate(requestID) != nil {
			requestID = uuid.New().String()
		}

		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// pathSecurityMiddleware normalizes paths and blocks traversal attempts
// (execution position 3), per AI.md PART 5 "HTTP Request Path Middleware".
func pathSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		original := r.URL.Path
		rawPath := r.URL.RawPath
		if rawPath == "" {
			rawPath = r.URL.Path
		}

		if strings.Contains(original, "..") ||
			strings.Contains(rawPath, "..") ||
			strings.Contains(strings.ToLower(rawPath), "%2e") {
			apperr.SendError(w, apperr.New(apperr.CodeBadRequest))
			return
		}

		cleaned := path.Clean(original)
		if !strings.HasPrefix(cleaned, "/") {
			cleaned = "/" + cleaned
		}
		if original != "/" && strings.HasSuffix(original, "/") && !strings.HasSuffix(cleaned, "/") {
			cleaned += "/"
		}
		r.URL.Path = cleaned

		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware sets a baseline set of security response
// headers (execution position 4). This is a partial implementation of
// AI.md PART 11's Security Headers section — CSP reporting, Permissions-
// Policy, Cross-Origin Isolation, and the full header matrix are deferred
// (see TODO.AI.md).
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "default-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// allowlistMiddleware sets a context flag when the resolved client IP is
// allowlisted (execution position 5). Real, wired, but always false — the
// backing `server.security.allowlist` config doesn't exist yet (see
// TODO.AI.md, AI.md PART 11 "Allowlist").
func (d *deps) allowlistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxKeyAllowlisted, false)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// blocklistMiddleware checks the resolved client IP/domain against the
// configured blocklist (execution position 6). Real, wired pass-through —
// the backing blocklist store doesn't exist yet (see TODO.AI.md, AI.md
// PART 11 "Blocklists").
func (d *deps) blocklistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware enforces the per-IP sliding-window limits
// (execution position 7), per AI.md PART 12 "Rate Limiting". Allowlisted
// requests bypass rate limiting.
func (d *deps) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsAllowlisted(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}

		ip := d.resolver.ResolveClientIP(r)
		class := classify(r.Method, r.URL.Path)
		allowed, retryAfter := d.rateLimiter.Allow(ip, class)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			apperr.SendError(w, apperr.New(apperr.CodeRateLimited))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// geoIPMiddleware would block requests by country (execution position 8).
// Real, wired pass-through — the GeoIP database doesn't exist yet (see
// TODO.AI.md, AI.md PART 19).
func (d *deps) geoIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// authMiddleware extracts and validates an operator token (execution
// position 9), per AI.md PART 8 "Auth Token Headers" and PART 11 "server
// token routes". It never blocks a request itself — no protected routes
// exist yet in this skeleton — it only attaches ctxKeyOperator for
// handlers/future middleware to consult.
func (d *deps) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ExtractToken(r)
		isOperator := token != "" && security.CompareServerToken(token, d.operatorTok)
		ctx := context.WithValue(r.Context(), ctxKeyOperator, isOperator)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ExtractToken returns the auth token carried by req, checking headers in
// the priority order from AI.md PART 8 "Auth Token Headers" (first found
// wins), falling back to the `?token=` query parameter.
func ExtractToken(req *http.Request) string {
	if v := req.Header.Get("Authorization"); v != "" {
		if tok, ok := strings.CutPrefix(v, "Bearer "); ok {
			return strings.TrimSpace(tok)
		}
		return strings.TrimSpace(v)
	}
	for _, h := range []string{"X-API-Key", "API-Key"} {
		if v := strings.TrimSpace(req.Header.Get(h)); v != "" {
			return v
		}
	}
	for _, h := range []string{"X-Auth-Token", "X-Access-Token"} {
		if v := strings.TrimSpace(req.Header.Get(h)); v != "" {
			return v
		}
	}
	for _, h := range []string{"X-Token", "Token"} {
		if v := strings.TrimSpace(req.Header.Get(h)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(req.URL.Query().Get("token"))
}

// statusRecorder captures the response status/size for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	size   int64
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.size += int64(n)
	return n, err
}

// Flush forwards to the wrapped ResponseWriter so streaming responses
// (e.g. server-sent events) are not buffered until the handler returns.
// Without this, wrapping silently strips the http.Flusher capability.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the wrapped ResponseWriter so protocol upgrades
// (e.g. WebSocket) still work through the logging middleware.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

// loggingMiddleware writes one access-log line per request and updates
// the request-statistics collector (execution position 10 — outermost, so
// it wraps every prior stage's effect on the response).
func (d *deps) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		end := d.stats.BeginRequest()
		defer end()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		d.stats.RecordRequest()
		if d.access != nil {
			entry := applog.AccessLogEntry{
				IP:        d.resolver.ResolveClientIP(r),
				Time:      start,
				Method:    r.Method,
				Path:      r.URL.Path,
				Protocol:  r.Proto,
				Status:    rec.status,
				Size:      rec.size,
				Referer:   r.Header.Get("Referer"),
				UserAgent: r.Header.Get("User-Agent"),
			}
			_ = d.access.WriteLine(applog.LevelInfo, applog.FormatApache(entry))
		}
	})
}
