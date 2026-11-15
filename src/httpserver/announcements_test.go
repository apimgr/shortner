package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
)

func TestActiveAnnouncements(t *testing.T) {
	now := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	items := config.WebAnnouncements{Enabled: true, Messages: []config.Announcement{
		{ID: "live", Type: "warning", Title: "Scheduled work", Message: "Window", Start: "2026-01-01T00:00:00Z", End: "2026-01-03T00:00:00Z", Dismissible: true},
		{ID: "future", Message: "Later", Start: "2026-01-03T00:00:00Z"},
		{ID: "expired", Message: "Past", End: "2026-01-01T00:00:00Z"},
		{ID: "invalid", Message: "Bad", Start: "tomorrow"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	active := activeAnnouncements(items, req, now)
	if len(active) != 1 || active[0].ID != "live" || active[0].Type != "warning" {
		t.Fatalf("active announcements = %#v", active)
	}
	items.Enabled = false
	if got := activeAnnouncements(items, req, now); len(got) != 0 {
		t.Fatalf("disabled announcements = %#v, want none", got)
	}
}

func TestAnnouncementDismissHandlerSetsCookieAndRedirectsLocally(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.cfg.Web.Announcements = config.WebAnnouncements{Enabled: true, Messages: []config.Announcement{{ID: "maintenance", Message: "Scheduled maintenance", Dismissible: true}}}
	req := httptest.NewRequest(http.MethodPost, "/announcements/dismiss", strings.NewReader("id=maintenance&return_to=%2Fserver%2Fabout"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	fd.dismissAnnouncementHandler(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/server/about" {
		t.Fatalf("status/location = %d/%q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != "dismissed_announcements" || cookie[0].Value != "maintenance" || cookie[0].HttpOnly {
		t.Fatalf("dismissal cookie = %#v", cookie)
	}
}

func TestAnnouncementDismissHandlerRejectsExternalReturnURL(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.cfg.Web.Announcements = config.WebAnnouncements{Enabled: true, Messages: []config.Announcement{{ID: "maintenance", Message: "Scheduled maintenance", Dismissible: true}}}
	req := httptest.NewRequest(http.MethodPost, "/announcements/dismiss", strings.NewReader("id=maintenance&return_to=https%3A%2F%2Fevil.example"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	fd.dismissAnnouncementHandler(rec, req)
	if got := rec.Header().Get("Location"); got != "/" {
		t.Fatalf("Location = %q, want /", got)
	}
}
