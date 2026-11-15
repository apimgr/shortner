package config

import (
	"strings"
	"testing"
)

func TestValidateAnnouncements(t *testing.T) {
	cfg := Default(":memory:")
	cfg.Web.Announcements.Messages = []Announcement{
		{ID: "maintenance-1", Type: "warning", Message: "Scheduled", Start: "2026-11-20T02:00:00Z", End: "2026-11-20T04:00:00Z"},
		{ID: "bad id", Type: "info", Message: "Invalid id"},
		{ID: "maintenance-1", Type: "error", Message: "Duplicate id"},
		{ID: "bad-window", Type: "info", Message: "Invalid window", Start: "tomorrow"},
	}
	warnings := Validate(cfg)
	if len(cfg.Web.Announcements.Messages) != 1 || cfg.Web.Announcements.Messages[0].ID != "maintenance-1" {
		t.Fatalf("validated announcements = %#v", cfg.Web.Announcements.Messages)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %#v, want 3 invalid-entry warnings", warnings)
	}
	for _, want := range []string{"bad id", "unique", "RFC 3339"} {
		found := false
		for _, warning := range warnings {
			if strings.Contains(warning, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("warnings %q do not contain %q", warnings, want)
		}
	}
}
