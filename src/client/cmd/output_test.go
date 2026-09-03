package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/client/api"
)

// expiresPtr is a small helper so link fixtures can set a pointer field
// inline.
func expiresPtr(value string) *string { return &value }

// TestPrintLinkFormats covers default (key-value), plain, csv, and json
// rendering of a single link against fixed golden strings.
func TestPrintLinkFormats(t *testing.T) {
	link := &api.Link{
		ShortCode:      "abc123",
		ShortURL:       "https://sh.rt/abc123",
		DestinationURL: "https://example.com",
		CreatedAt:      "2026-01-01T00:00:00Z",
		ClickCount:     5,
	}

	t.Run("default key-value", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		if err := p.PrintLink(link); err != nil {
			t.Fatalf("PrintLink: %v", err)
		}
		got := out.String()
		for _, want := range []string{"Short code", "abc123", "Clicks", "5", "Expires", "never"} {
			if !strings.Contains(got, want) {
				t.Errorf("output missing %q; got:\n%s", want, got)
			}
		}
	})

	t.Run("plain is tab-joined", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "plain", false, false)
		if err := p.PrintLink(link); err != nil {
			t.Fatalf("PrintLink: %v", err)
		}
		want := "abc123\thttps://example.com\t5\t2026-01-01T00:00:00Z\tnever\n"
		if out.String() != want {
			t.Fatalf("got %q, want %q", out.String(), want)
		}
	})

	t.Run("csv", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "csv", false, false)
		if err := p.PrintLink(link); err != nil {
			t.Fatalf("PrintLink: %v", err)
		}
		want := "CODE,DESTINATION,CLICKS,CREATED,EXPIRES\nabc123,https://example.com,5,2026-01-01T00:00:00Z,never\n"
		if out.String() != want {
			t.Fatalf("got %q, want %q", out.String(), want)
		}
	})

	t.Run("json", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "json", false, false)
		if err := p.PrintLink(link); err != nil {
			t.Fatalf("PrintLink: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, `"short_code": "abc123"`) {
			t.Fatalf("json missing short_code; got:\n%s", got)
		}
	})

	t.Run("expires_at set overrides never", func(t *testing.T) {
		expiring := *link
		expiring.ExpiresAt = expiresPtr("2030-01-01T00:00:00Z")
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "plain", false, false)
		if err := p.PrintLink(&expiring); err != nil {
			t.Fatalf("PrintLink: %v", err)
		}
		if !strings.Contains(out.String(), "2030-01-01T00:00:00Z") {
			t.Fatalf("got %q, want expiry included", out.String())
		}
	})
}

// TestPrintCreatedLinkOwnerToken covers the one-time owner-token warning
// message, and that it is suppressed in quiet mode and machine formats.
func TestPrintCreatedLinkOwnerToken(t *testing.T) {
	created := &api.CreatedLink{
		Link:       api.Link{ShortCode: "abc123", ShortURL: "https://sh.rt/abc123", DestinationURL: "https://example.com"},
		OwnerToken: "owner-tok",
	}

	t.Run("default shows save-now message", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		if err := p.PrintCreatedLink(created); err != nil {
			t.Fatalf("PrintCreatedLink: %v", err)
		}
		if !strings.Contains(out.String(), "Save the owner token now") {
			t.Fatalf("missing warning: %s", out.String())
		}
	})

	t.Run("quiet suppresses the message but not the token itself", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, true)
		if err := p.PrintCreatedLink(created); err != nil {
			t.Fatalf("PrintCreatedLink: %v", err)
		}
		if strings.Contains(out.String(), "Save the owner token now") {
			t.Fatalf("quiet mode should suppress the message: %s", out.String())
		}
		if !strings.Contains(out.String(), "owner-tok") {
			t.Fatalf("owner token itself should still print: %s", out.String())
		}
	})

	t.Run("plain prints short url then token", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "plain", false, false)
		if err := p.PrintCreatedLink(created); err != nil {
			t.Fatalf("PrintCreatedLink: %v", err)
		}
		want := "https://sh.rt/abc123\nowner-tok\n"
		if out.String() != want {
			t.Fatalf("got %q, want %q", out.String(), want)
		}
	})

	t.Run("empty owner token omits token line", func(t *testing.T) {
		noToken := &api.CreatedLink{Link: api.Link{ShortURL: "https://sh.rt/xyz"}}
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "plain", false, false)
		if err := p.PrintCreatedLink(noToken); err != nil {
			t.Fatalf("PrintCreatedLink: %v", err)
		}
		if out.String() != "https://sh.rt/xyz\n" {
			t.Fatalf("got %q", out.String())
		}
	})
}

// TestPrintLinksEmptyAndPopulated covers the empty-list message and the
// table + pagination-footer path, plus quiet suppression of the footer.
func TestPrintLinksEmptyAndPopulated(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		if err := p.PrintLinks(&api.LinkList{}); err != nil {
			t.Fatalf("PrintLinks: %v", err)
		}
		if !strings.Contains(out.String(), "No links found.") {
			t.Fatalf("got %q", out.String())
		}
	})

	list := &api.LinkList{
		Data: []api.Link{
			{ShortCode: "a1", DestinationURL: "https://a.example.com", ClickCount: 1},
			{ShortCode: "b2", DestinationURL: "https://b.example.com", ClickCount: 2},
		},
		Pagination: api.Pagination{Page: 1, Pages: 3, Total: 25},
	}

	t.Run("table with pagination footer", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		if err := p.PrintLinks(list); err != nil {
			t.Fatalf("PrintLinks: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "Page 1 of 3 (25 links total)") {
			t.Fatalf("missing pagination footer: %s", got)
		}
		if !strings.Contains(got, "a1") || !strings.Contains(got, "b2") {
			t.Fatalf("missing rows: %s", got)
		}
	})

	t.Run("quiet suppresses pagination footer", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, true)
		if err := p.PrintLinks(list); err != nil {
			t.Fatalf("PrintLinks: %v", err)
		}
		if strings.Contains(out.String(), "Page 1 of 3") {
			t.Fatalf("quiet mode should suppress footer: %s", out.String())
		}
	})

	t.Run("plain lists rows without footer", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "plain", false, false)
		if err := p.PrintLinks(list); err != nil {
			t.Fatalf("PrintLinks: %v", err)
		}
		if strings.Contains(out.String(), "Page") {
			t.Fatalf("plain format should never print the pagination prose: %s", out.String())
		}
		if !strings.Contains(out.String(), "a1\thttps://a.example.com\t1") {
			t.Fatalf("got %q", out.String())
		}
	})
}

// TestPrintStatsTablesAndReferrerSort covers the time-series table, the
// referrer table sorted by descending click count, and csv/plain.
func TestPrintStatsTablesAndReferrerSort(t *testing.T) {
	stats := &api.Stats{
		ShortCode:   "abc123",
		TotalClicks: 30,
		Referrers:   map[string]int{"direct": 10, "google.com": 20},
		TimeSeries: []api.DayCount{
			{Date: "2026-01-01", Count: 15},
			{Date: "2026-01-02", Count: 15},
		},
	}

	t.Run("default renders both tables, referrers sorted by clicks desc", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		if err := p.PrintStats(stats); err != nil {
			t.Fatalf("PrintStats: %v", err)
		}
		got := out.String()
		googleIdx := strings.Index(got, "google.com")
		directIdx := strings.Index(got, "direct")
		if googleIdx == -1 || directIdx == -1 || googleIdx > directIdx {
			t.Fatalf("expected google.com (20 clicks) before direct (10 clicks); got:\n%s", got)
		}
	})

	t.Run("csv emits date/clicks rows only", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "csv", false, false)
		if err := p.PrintStats(stats); err != nil {
			t.Fatalf("PrintStats: %v", err)
		}
		want := "DATE,CLICKS\n2026-01-01,15\n2026-01-02,15\n"
		if out.String() != want {
			t.Fatalf("got %q, want %q", out.String(), want)
		}
	})

	t.Run("plain prints summary then series", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "plain", false, false)
		if err := p.PrintStats(stats); err != nil {
			t.Fatalf("PrintStats: %v", err)
		}
		want := "abc123\t30\n2026-01-01\t15\n2026-01-02\t15\n"
		if out.String() != want {
			t.Fatalf("got %q, want %q", out.String(), want)
		}
	})

	t.Run("empty time series and referrers print nothing extra", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		bare := &api.Stats{ShortCode: "x", TotalClicks: 0}
		if err := p.PrintStats(bare); err != nil {
			t.Fatalf("PrintStats: %v", err)
		}
		if strings.Contains(out.String(), "DATE") || strings.Contains(out.String(), "REFERRER") {
			t.Fatalf("should not render empty tables: %s", out.String())
		}
	})
}

// TestPrintHealthFormats covers default, csv, and plain rendering.
func TestPrintHealthFormats(t *testing.T) {
	health := &api.Health{Status: "ok", Version: "1.0.0", GoVersion: "go1.23", Mode: "production", Uptime: "1h"}
	health.Project.Name = "shortner"

	t.Run("default key-value", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		if err := p.PrintHealth(health); err != nil {
			t.Fatalf("PrintHealth: %v", err)
		}
		got := out.String()
		for _, want := range []string{"Status", "ok", "shortner", "1.0.0", "go1.23", "production", "1h"} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q; got:\n%s", want, got)
			}
		}
	})

	t.Run("csv", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "csv", false, false)
		if err := p.PrintHealth(health); err != nil {
			t.Fatalf("PrintHealth: %v", err)
		}
		want := "FIELD,VALUE\nStatus,ok\nProject,shortner\nVersion,1.0.0\nGo version,go1.23\nMode,production\nUptime,1h\n"
		if out.String() != want {
			t.Fatalf("got %q, want %q", out.String(), want)
		}
	})

	t.Run("plain", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "plain", false, false)
		if err := p.PrintHealth(health); err != nil {
			t.Fatalf("PrintHealth: %v", err)
		}
		want := "Status\tok\nProject\tshortner\nVersion\t1.0.0\nGo version\tgo1.23\nMode\tproduction\nUptime\t1h\n"
		if out.String() != want {
			t.Fatalf("got %q, want %q", out.String(), want)
		}
	})
}

// TestMessageWarnErrorSuppression covers the suppression rules for Message
// (quiet or machine format) and Warn (quiet only), and that Error is never
// suppressed.
func TestMessageWarnErrorSuppression(t *testing.T) {
	t.Run("Message suppressed by quiet", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, true)
		p.Message("hello %s", "world")
		if out.Len() != 0 {
			t.Fatalf("got %q, want empty", out.String())
		}
	})

	for _, format := range []string{"json", "yaml", "csv"} {
		t.Run("Message suppressed by "+format, func(t *testing.T) {
			var out bytes.Buffer
			p := NewPrinter(&out, &out, format, false, false)
			p.Message("hello")
			if out.Len() != 0 {
				t.Fatalf("got %q, want empty for format %s", out.String(), format)
			}
		})
	}

	t.Run("Message prints for table format", func(t *testing.T) {
		var out bytes.Buffer
		p := NewPrinter(&out, &out, "table", false, false)
		p.Message("hello %s", "world")
		if out.String() != "hello world\n" {
			t.Fatalf("got %q", out.String())
		}
	})

	t.Run("Warn suppressed by quiet", func(t *testing.T) {
		var errOut bytes.Buffer
		p := NewPrinter(&errOut, &errOut, "table", false, true)
		p.Warn("careful")
		if errOut.Len() != 0 {
			t.Fatalf("got %q, want empty", errOut.String())
		}
	})

	t.Run("Warn prints even for json format", func(t *testing.T) {
		var errOut bytes.Buffer
		p := NewPrinter(&errOut, &errOut, "json", false, false)
		p.Warn("careful")
		if !strings.Contains(errOut.String(), "warning: careful") {
			t.Fatalf("got %q", errOut.String())
		}
	})

	t.Run("Error is never suppressed, even in quiet mode", func(t *testing.T) {
		var errOut bytes.Buffer
		p := NewPrinter(&errOut, &errOut, "json", false, true)
		p.Error("boom")
		if !strings.Contains(errOut.String(), "error: boom") {
			t.Fatalf("got %q", errOut.String())
		}
	})
}

// TestColorizeNoOp covers that colorize is a no-op when color is disabled
// or the color code is empty.
func TestColorizeNoOp(t *testing.T) {
	var out bytes.Buffer
	p := NewPrinter(&out, &out, "table", false, false)
	if got := p.colorize("196", "text"); got != "text" {
		t.Fatalf("colorize with color disabled = %q, want plain text", got)
	}

	p2 := NewPrinter(&out, &out, "table", true, false)
	if got := p2.colorize("", "text"); got != "text" {
		t.Fatalf("colorize with empty code = %q, want plain text", got)
	}
	if got := p2.colorize("196", "text"); got == "text" {
		t.Fatalf("colorize with color enabled and a code should wrap the text")
	}
}

// TestPadTruncateRuneLen covers boundary conditions for the low-level table
// helpers: exact width, over width, unicode runes, and width<=1.
func TestPadTruncateRuneLen(t *testing.T) {
	if got := pad("ab", 4); got != " ab   " {
		t.Fatalf("pad(\"ab\", 4) = %q", got)
	}
	if got := truncate("hello", 5); got != "hello" {
		t.Fatalf("truncate exact width = %q", got)
	}
	if got := truncate("hello world", 5); got != "hell…" {
		t.Fatalf("truncate over width = %q", got)
	}
	if got := truncate("hello", 1); got != "h" {
		t.Fatalf("truncate width<=1 = %q", got)
	}
	if got := runeLen("héllo"); got != 5 {
		t.Fatalf("runeLen(héllo) = %d, want 5", got)
	}
}

// TestBorder covers the box-drawing rule builder.
func TestBorder(t *testing.T) {
	got := border("┌", "┬", "┐", []int{2, 3})
	want := "┌────┬─────┐"
	if got != want {
		t.Fatalf("border() = %q, want %q", got, want)
	}
}

// TestSortRowsAndLessRow covers numeric-descending sort, a tie falling back
// to string comparison, and non-numeric string-ascending sort.
func TestSortRowsAndLessRow(t *testing.T) {
	rows := [][]string{
		{"b", "5"},
		{"a", "10"},
		{"c", "10"},
	}
	sortRows(rows)
	want := [][]string{{"a", "10"}, {"c", "10"}, {"b", "5"}}
	if !equalRows(rows, want) {
		t.Fatalf("sortRows numeric = %v, want %v", rows, want)
	}

	nonNumeric := [][]string{{"zebra", "n/a"}, {"apple", "n/a"}}
	sortRows(nonNumeric)
	wantNonNumeric := [][]string{{"apple", "n/a"}, {"zebra", "n/a"}}
	if !equalRows(nonNumeric, wantNonNumeric) {
		t.Fatalf("sortRows non-numeric = %v, want %v", nonNumeric, wantNonNumeric)
	}
}

func equalRows(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
