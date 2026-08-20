// Inbound privacy-signal handling (Sec-GPC, DNT), per AI.md PART 11
// "Privacy Signal Headers".
package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

// privacyDeps carries what the privacy-signal stage needs.
type privacyDeps struct {
	cfg      *config.Config
	resolver *ProxyResolver
	audit    *applog.AuditLogger
}

// privacySignalMiddleware sets the request's `gpc_opt_out` flag when a
// honored opt-out signal is present, per AI.md PART 11 "Privacy Signal
// Headers". Sec-GPC is honored by default; DNT is not, because it was
// de-facto removed from Firefox/Chrome — operators with EU-only audiences
// opt in via `web.headers.honor_dnt`.
//
// The signal is an opt-OUT only: its absence never grants consent.
func (pd *privacyDeps) privacySignalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		optOut := pd.signalPresent(r)
		if optOut {
			pd.auditGPC(r)
		}
		ctx := context.WithValue(r.Context(), ctxKeyGPCOptOut, optOut)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// signalPresent reports whether this request carries a privacy opt-out
// signal the operator has chosen to honor.
func (pd *privacyDeps) signalPresent(r *http.Request) bool {
	h := pd.cfg.Web.Headers
	if h.HonorSecGPC && r.Header.Get("Sec-GPC") == "1" {
		return true
	}
	if h.HonorDNT && r.Header.Get("DNT") == "1" {
		return true
	}
	return false
}

// auditGPC records that the signal was honored so a compliance officer
// can prove it, per AI.md PART 11 "Privacy Signal Headers" step 3. No
// researcher/user PII is recorded — only the client IP the audit format
// already carries and the path.
func (pd *privacyDeps) auditGPC(r *http.Request) {
	if pd.audit == nil {
		return
	}
	_ = pd.audit.Write(applog.Entry{
		Time:     time.Now().UTC(),
		Event:    "compliance.gpc_honored",
		Category: "compliance",
		Severity: applog.SeverityInfo,
		Actor:    applog.Actor{IP: pd.resolver.ResolveClientIP(r)},
		Details:  map[string]any{"path": r.URL.Path},
		Result:   applog.ResultSuccess,
	})
}

// EssentialCookiesOnly reports whether this request must be limited to
// strictly-required cookies. It is the single check every cookie-setting
// path uses so the GPC rule cannot be forgotten in one branch, per AI.md
// PART 11 "Privacy Signal Headers" step 2.
func EssentialCookiesOnly(r *http.Request) bool {
	return IsGPCOptOut(r.Context())
}
