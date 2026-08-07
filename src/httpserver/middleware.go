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
	"context"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
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
}

// setupMiddleware wraps handler with the full PART 12 chain, per AI.md
// PART 5 "Middleware Order". Wrapping order is reversed from execution
// order: the last middleware applied here runs first.
func (d *deps) setupMiddleware(handler http.Handler) http.Handler {
	handler = d.loggingMiddleware(handler)       // 10
	handler = d.authMiddleware(handler)          // 9
	handler = d.geoIPMiddleware(handler)         // 8
	handler = d.rateLimitMiddleware(handler)     // 7
	handler = d.blocklistMiddleware(handler)     // 6
	handler = d.allowlistMiddleware(handler)     // 5
	handler = securityHeadersMiddleware(handler) // 4
	handler = pathSecurityMiddleware(handler)    // 3
	handler = requestIDMiddleware(handler)       // 2
	handler = urlNormalizeMiddleware(handler)    // 1
	return handler
}

// repeatedSlashes matches two or more consecutive "/" for URLNormalize.
var repeatedSlashes = regexp.MustCompile(`/{2,}`)

// urlNormalizeMiddleware collapses repeated slashes in the request path
// (execution position 1), per AI.md PART 5 "Middleware Order" and the
// "GET //api///v1//items" -> "/api/{api_version}/items" example.
func urlNormalizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "//") {
			cleaned := repeatedSlashes.ReplaceAllString(r.URL.Path, "/")
			if cleaned != "/" && strings.HasSuffix(r.URL.Path, "/") && !strings.HasSuffix(cleaned, "/") {
				cleaned += "/"
			}
			r.URL.Path = cleaned
		}
		next.ServeHTTP(w, r)
	})
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
