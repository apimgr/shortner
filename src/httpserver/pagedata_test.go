package httpserver

import (
	"strings"
	"testing"
	"time"
)

// TestExpandFooterVariables covers AI.md PART 16 "Available Footer
// Variables": every documented token is substituted, an unknown token is
// left visible so the operator can spot their own typo, and an
// unpublished overlay address expands to nothing rather than a stale
// value.
func TestExpandFooterVariables(t *testing.T) {
	vars := footerVariables{
		currentYear:    "2026",
		projectName:    "shortner",
		projectOrg:     "apimgr",
		projectVersion: "1.2.3",
		buildDateTime:  "August 20, 2026 at 00:00:00 UTC",
		onionAddress:   "exampleonionaddress.onion",
		i2pAddress:     "exampleeepsite.b32.i2p",
	}

	tests := []struct {
		name string
		html string
		want string
	}{
		{"empty input", "", ""},
		{"current year", "<p>{current_year}</p>", "<p>2026</p>"},
		{"project name", "<p>{project_name}</p>", "<p>shortner</p>"},
		{"project org", "<p>{project_org}</p>", "<p>apimgr</p>"},
		{"project version", "<p>v{project_version}</p>", "<p>v1.2.3</p>"},
		{
			"build datetime",
			"<p>{build_datetime}</p>",
			"<p>August 20, 2026 at 00:00:00 UTC</p>",
		},
		{
			"onion address",
			"<code>{onion_address}</code>",
			"<code>exampleonionaddress.onion</code>",
		},
		{
			"i2p address",
			"<code>{i2p_address}</code>",
			"<code>exampleeepsite.b32.i2p</code>",
		},
		{
			"repeated token",
			"{onion_address} {onion_address}",
			"exampleonionaddress.onion exampleonionaddress.onion",
		},
		{"unknown token untouched", "<p>{not_a_variable}</p>", "<p>{not_a_variable}</p>"},
		{"percent is literal", "<p>100% {project_name}</p>", "<p>100% shortner</p>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandFooterVariables(tc.html, vars); got != tc.want {
				t.Fatalf("expandFooterVariables(%q) = %q, want %q", tc.html, got, tc.want)
			}
		})
	}
}

// TestExpandFooterVariablesUnpublishedOverlay proves an overlay that has
// not published an address contributes an empty string, per AI.md PART 16's
// "only when enabled, running, and an address is published" rule.
func TestExpandFooterVariablesUnpublishedOverlay(t *testing.T) {
	got := expandFooterVariables(
		"<code>{onion_address}</code><code>{i2p_address}</code>",
		footerVariables{},
	)
	if strings.Contains(got, "{onion_address}") || strings.Contains(got, "{i2p_address}") {
		t.Fatalf("unpublished overlay left a placeholder: %q", got)
	}
	if got != "<code></code><code></code>" {
		t.Fatalf("got %q, want empty substitutions", got)
	}
}

// TestFooterBuildDateTimeOverlayIsUTC covers AI.md PART 31 "Tor Timestamp
// Normalization": an overlay request must never render the server's local
// zone, since it narrows the operator's location.
func TestFooterBuildDateTimeOverlayIsUTC(t *testing.T) {
	// A fixed offset zone that is never UTC, so a local-zone render is
	// distinguishable from a UTC one regardless of the host's TZ.
	zone := time.FixedZone("TEST", 5*3600)
	build := time.Date(2026, time.August, 20, 12, 0, 0, 0, zone).Format(time.RFC3339)

	overlayText := footerBuildDateTime(build, true)
	if !strings.Contains(overlayText, "UTC") {
		t.Fatalf("overlay footer timestamp %q is not UTC", overlayText)
	}
	if !strings.Contains(overlayText, "07:00:00") {
		t.Fatalf("overlay footer timestamp %q was not converted to UTC", overlayText)
	}
}

// TestFooterBuildDateTimeUnparseable covers the `go run`/`go test` build,
// where BuildEpoch is unset and the value must pass through untouched
// rather than rendering a fabricated date.
func TestFooterBuildDateTimeUnparseable(t *testing.T) {
	if got := footerBuildDateTime("N/A", false); got != "N/A" {
		t.Fatalf("footerBuildDateTime(\"N/A\") = %q, want \"N/A\"", got)
	}
}

// TestOverlayTime asserts the zone selection both ways.
func TestOverlayTime(t *testing.T) {
	base := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.FixedZone("TEST", 5*3600))
	if got := overlayTime(base, true); got.Location() != time.UTC {
		t.Fatalf("overlay time location = %v, want UTC", got.Location())
	}
	if got := overlayTime(base, false); got.Location() != time.Local {
		t.Fatalf("clearnet time location = %v, want Local", got.Location())
	}
}
