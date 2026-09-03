package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/security"
)

// statusRequest builds a request routed as chi would route it, so
// chi.URLParam("tracking_id") resolves inside the handler.
func statusRequest(fd *frontendDeps, trackingID, token string) *httptest.ResponseRecorder {
	target := "/server/security/report/" + trackingID
	if token != "" {
		target += "?token=" + token
	}
	req := htmlRequest(http.MethodGet, target, nil)

	router := chi.NewRouter()
	router.Get("/server/security/report/{tracking_id}", fd.securityReportStatusHandler)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// seedReport inserts one report and returns its tracking id.
func seedReport(t *testing.T, fd *frontendDeps) string {
	t.Helper()
	trackingID, err := db.NewTrackingID()
	if err != nil {
		t.Fatalf("db.NewTrackingID() error = %v", err)
	}
	now := time.Now().UTC()
	rep := db.SecurityReport{
		TrackingID:   trackingID,
		ReceivedAt:   now,
		Severity:     "high",
		Component:    "api",
		Sealed:       "sealed-body",
		DisclosureAt: now.AddDate(0, 0, 90),
	}
	if err := db.InsertSecurityReport(context.Background(), fd.ld.sqlDB, rep); err != nil {
		t.Fatalf("db.InsertSecurityReport() error = %v", err)
	}
	return trackingID
}

func TestSecurityReportStatusRequiresValidToken(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	trackingID := seedReport(t, fd)

	tests := []struct {
		name  string
		token string
	}{
		{name: "no token"},
		{name: "wrong token", token: strings.Repeat("a", 32)},
		{name: "token for another report", token: security.ReportToken(fd.installSecret, "sec_0000000000000000")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := statusRequest(fd, trackingID, tt.token)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (the page must never confirm a tracking id exists)", rec.Code)
			}
		})
	}
}

func TestSecurityReportStatusUnknownReportIs404(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	const trackingID = "sec_1234567890abcdef"
	rec := statusRequest(fd, trackingID, security.ReportToken(fd.installSecret, trackingID))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestSecurityReportStatusRendersReceivedState(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	trackingID := seedReport(t, fd)

	rec := statusRequest(fd, trackingID, security.ReportToken(fd.installSecret, trackingID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, trackingID) {
		t.Error("body does not carry the tracking id")
	}
	if !strings.Contains(body, "Received") {
		t.Error("body does not carry the triage state")
	}
	if strings.Contains(body, "sealed-body") {
		t.Error("body leaks the encrypted report payload")
	}
}

func TestSecurityReportStatusIsOncePerDay(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	trackingID := seedReport(t, fd)
	token := security.ReportToken(fd.installSecret, trackingID)

	if rec := statusRequest(fd, trackingID, token); rec.Code != http.StatusOK {
		t.Fatalf("first view status = %d, want 200", rec.Code)
	}
	if rec := statusRequest(fd, trackingID, token); rec.Code != http.StatusTooManyRequests {
		t.Errorf("second view status = %d, want 429", rec.Code)
	}

	// A view older than the interval must be allowed again.
	past := time.Now().UTC().Add(-25 * time.Hour)
	if err := db.TouchSecurityReportView(context.Background(), fd.ld.sqlDB, trackingID, past); err != nil {
		t.Fatalf("db.TouchSecurityReportView() error = %v", err)
	}
	if rec := statusRequest(fd, trackingID, token); rec.Code != http.StatusOK {
		t.Errorf("view after the interval status = %d, want 200", rec.Code)
	}
}

func TestReportStatusTokenMatchesSecurityPackage(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	const trackingID = "sec_1234567890abcdef"
	if got := fd.reportStatusToken(trackingID); got != security.ReportToken(fd.installSecret, trackingID) {
		t.Errorf("reportStatusToken() = %q, does not match security.ReportToken", got)
	}
}

func TestSplitComments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{name: "empty", in: "", want: 0},
		{name: "whitespace only", in: "   \n\n  ", want: 0},
		{name: "one paragraph", in: "confirmed, fix in progress", want: 1},
		{name: "two paragraphs", in: "confirmed\n\nfix shipped in 1.2.0", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(splitComments(tt.in)); got != tt.want {
				t.Errorf("splitComments() length = %d, want %d", got, tt.want)
			}
		})
	}
}
