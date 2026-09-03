// The AI.md PART 11 researcher status page,
// /server/security/report/{tracking_id}.
package httpserver

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/security"
)

// reportViewInterval is how long a served status page locks out the next
// view, implementing AI.md PART 11's "Token is single-use-per-day".
const reportViewInterval = 24 * time.Hour

// securityReportStatusPageData is bound to
// page/security_report_status.tmpl.
//
// Every field here is Tier 3 under AI.md PART 11's Public Endpoint Safety
// Principle — disclosed only to the one researcher holding the token, and
// only about their own report. Nothing from the encrypted body, the
// researcher's own PII, or any server-internal state is carried: the page
// shows exactly the three things AI.md lists (triage state, maintainer
// comments visible to the researcher, expected disclosure date) plus the
// tracking id the researcher already has.
type securityReportStatusPageData struct {
	Base         PageData
	TrackingID   string
	Status       string
	StatusLabel  string
	Severity     string
	Component    string
	ReceivedAt   string
	DisclosureAt string
	Comments     []string
	PolicyURL    string
}

// securityReportStatusHandler renders the researcher status page. Access is
// the one-shot token AI.md PART 11 "Public Pages" requires: without a valid
// token the route is a 404, never a 401 or 403, so the endpoint never
// confirms that a given tracking id exists.
func (fd *frontendDeps) securityReportStatusHandler(w http.ResponseWriter, r *http.Request) {
	trackingID := strings.TrimSpace(chi.URLParam(r, "tracking_id"))
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if trackingID == "" || !security.ValidReportToken(fd.installSecret, trackingID, token) {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}

	rep, err := db.GetSecurityReport(r.Context(), fd.ld.sqlDB, trackingID)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}

	now := time.Now().UTC()
	if security.ReportTokenExpired(rep.ClosedAt, now) {
		sendError(w, r, apperr.New(apperr.CodeGone))
		return
	}
	if !rep.LastViewedAt.IsZero() && now.Sub(rep.LastViewedAt) < reportViewInterval {
		sendError(w, r, apperr.New(apperr.CodeRateLimited))
		return
	}
	// A view is recorded even if rendering later fails: the limit exists to
	// stop the token being used as an oracle, so it must not be bypassable
	// by inducing a render error.
	_ = db.TouchSecurityReportView(r.Context(), fd.ld.sqlDB, trackingID, now)

	status := rep.Status
	if !db.IsSecurityReportStatus(status) {
		status = db.StatusReceived
	}

	data := securityReportStatusPageData{
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "security.report_status_title"), fd.cfg.Server.SEO.Description),
		TrackingID:  rep.TrackingID,
		Status:      status,
		StatusLabel: t(r, "security.report_state_"+status),
		Severity:    rep.Severity,
		Component:   rep.Component,
		ReceivedAt:  rep.ReceivedAt.Format(time.RFC3339),
		Comments:    splitComments(rep.Comments),
		PolicyURL:   "/server/security/policy",
	}
	if !rep.DisclosureAt.IsZero() {
		data.DisclosureAt = rep.DisclosureAt.Format("2006-01-02")
	}
	_ = renderPage(w, http.StatusOK, "security_report_status", data)
}

// splitComments breaks maintainer commentary into paragraphs. Splitting
// here rather than in the template keeps the text escaped — maintainer
// commentary is never treated as HTML.
func splitComments(comments string) []string {
	var out []string
	for _, p := range strings.Split(comments, "\n\n") {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// reportStatusToken derives the one-shot token for a tracking id, for the
// status-page link in the researcher acknowledgment email.
func (fd *frontendDeps) reportStatusToken(trackingID string) string {
	return security.ReportToken(fd.installSecret, trackingID)
}
