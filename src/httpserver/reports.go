// The browser Reporting API collection endpoints, per AI.md PART 11
// "Reporting API (Modern + Legacy)".
package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

// maxReportBody caps a single report payload. Browsers send a few KB at
// most; anything larger is a misuse of a public unauthenticated endpoint.
const maxReportBody = 64 << 10

// reportDeps carries what the report endpoints need.
type reportDeps struct {
	cfg      *config.Config
	resolver *ProxyResolver
	audit    *applog.AuditLogger
	limiter  *reportLimiter
}

// newReportDeps builds the report endpoints' dependencies.
func newReportDeps(cfg *config.Config, resolver *ProxyResolver, audit *applog.AuditLogger) *reportDeps {
	return &reportDeps{
		cfg:      cfg,
		resolver: resolver,
		audit:    audit,
		limiter:  newReportLimiter(cfg.Web.Reports),
	}
}

// registerReportRoutes mounts the three report groups under the versioned
// API subtree. They are POST-only and unauthenticated by design — the
// browser sends them with no credentials.
func (rd *reportDeps) registerReportRoutes(r chi.Router) {
	r.Post("/server/reports/csp", rd.handler("csp"))
	r.Post("/server/reports/nel", rd.handler("nel"))
	r.Post("/server/reports/default", rd.handler("default"))
}

// handler returns the collector for one report group. Every response is
// 204 with an empty body: a report endpoint must never tell a caller
// whether its payload was accepted, stored, or dropped, and must never
// echo user-controlled content back (AI.md PART 11's Tier 2 rule for
// public report endpoints).
func (rd *reportDeps) handler(group string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() { _, _ = io.Copy(io.Discard, r.Body) }()
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ip := rd.resolver.ResolveClientIP(r)
		if !rd.limiter.allow(ip, time.Now()) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxReportBody))
		if err == nil && len(body) > 0 {
			rd.record(group, ip, body)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// cspReportEnvelope is the legacy `application/csp-report` shape.
type cspReportEnvelope struct {
	CSPReport struct {
		DocumentURI        string `json:"document-uri"`
		ViolatedDirective  string `json:"violated-directive"`
		EffectiveDirective string `json:"effective-directive"`
		BlockedURI         string `json:"blocked-uri"`
		Disposition        string `json:"disposition"`
		StatusCode         int    `json:"status-code"`
	} `json:"csp-report"`
}

// reportsJSONEntry is one entry of the modern `application/reports+json`
// array shared by CSP, NEL, and the default group.
type reportsJSONEntry struct {
	Type string         `json:"type"`
	URL  string         `json:"url"`
	Body map[string]any `json:"body"`
}

// record writes one sanitized audit entry per report. Only the small set
// of structural fields below is kept: a report body is attacker-supplied
// (any page can be made to violate a policy), so nothing else from it is
// ever persisted or logged.
func (rd *reportDeps) record(group, ip string, body []byte) {
	if rd.audit == nil {
		return
	}

	details := map[string]any{"group": group}
	event := "security.report_received"

	var legacy cspReportEnvelope
	if err := json.Unmarshal(body, &legacy); err == nil && legacy.CSPReport.ViolatedDirective != "" {
		event = "security.csp_violation"
		details["violated_directive"] = legacy.CSPReport.ViolatedDirective
		details["effective_directive"] = legacy.CSPReport.EffectiveDirective
		details["blocked_uri"] = legacy.CSPReport.BlockedURI
		details["disposition"] = legacy.CSPReport.Disposition
	} else {
		var modern []reportsJSONEntry
		if err := json.Unmarshal(body, &modern); err == nil && len(modern) > 0 {
			types := make([]string, 0, len(modern))
			for _, entry := range modern {
				types = append(types, entry.Type)
				if entry.Type == "csp-violation" {
					event = "security.csp_violation"
					if v, ok := entry.Body["effectiveDirective"].(string); ok {
						details["effective_directive"] = v
					}
					if v, ok := entry.Body["blockedURL"].(string); ok {
						details["blocked_uri"] = v
					}
				}
				if entry.Type == "network-error" {
					if v, ok := entry.Body["type"].(string); ok {
						details["network_error"] = v
					}
				}
			}
			details["types"] = types
		}
	}

	_ = rd.audit.Write(applog.Entry{
		Time:     time.Now().UTC(),
		Event:    event,
		Category: "security",
		Severity: applog.SeverityWarn,
		Actor:    applog.Actor{IP: ip},
		Details:  details,
		Result:   applog.ResultSuccess,
	})
}

// reportLimiter is the per-IP token bucket for the public report
// endpoints, sized by `web.reports`, per AI.md PART 11: "All report
// endpoints share the same public reports rules — same rate limits".
type reportLimiter struct {
	perMinute int
	burst     int

	mu      sync.Mutex
	counts  map[string]int
	resetAt time.Time
}

// newReportLimiter builds the limiter from config.
func newReportLimiter(cfg config.Reports) *reportLimiter {
	return &reportLimiter{
		perMinute: cfg.RateLimitPerMinute,
		burst:     cfg.RateLimitPerIPBurst,
		counts:    map[string]int{},
		resetAt:   time.Now().Add(time.Minute),
	}
}

// allow reports whether this IP may submit another report right now. The
// per-IP burst is the hard ceiling; the per-minute value bounds the whole
// endpoint so one browser cannot drown the audit log.
func (l *reportLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.After(l.resetAt) {
		l.counts = map[string]int{}
		l.resetAt = now.Add(time.Minute)
	}

	limit := l.burst
	if l.perMinute > 0 && l.perMinute < limit {
		limit = l.perMinute
	}
	if limit <= 0 {
		return false
	}
	if l.counts[ip] >= limit {
		return false
	}
	l.counts[ip]++
	return true
}
