package urlutil

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildAPIURLPathParams(t *testing.T) {
	got := BuildAPIURL("https://example.com", "/api/v1/links/{slug}", map[string]string{"slug": "my slug/../etc"}, nil)
	if strings.Contains(got, " ") {
		t.Fatalf("unencoded space in %q", got)
	}
	if strings.Contains(got, "/../") {
		t.Fatalf("path traversal survived encoding: %q", got)
	}
	if !strings.HasPrefix(got, "https://example.com/api/v1/links/") {
		t.Fatalf("unexpected prefix: %q", got)
	}
}

func TestBuildAPIURLTrailingSlashBase(t *testing.T) {
	got := BuildAPIURL("https://example.com/", "/api/v1/links", nil, nil)
	if got != "https://example.com/api/v1/links" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildAPIURLQueryParams(t *testing.T) {
	got := BuildAPIURL("https://example.com", "/api/v1/links", nil, map[string]string{"q": "a b&c", "page": "2"})
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("unparseable: %v", err)
	}
	if parsed.Query().Get("q") != "a b&c" {
		t.Fatalf("query round-trip failed: %q", parsed.Query().Get("q"))
	}
	if parsed.Query().Get("page") != "2" {
		t.Fatalf("page lost: %q", got)
	}
}

func TestBuildAPIURLInvalidBase(t *testing.T) {
	if got := BuildAPIURL("://nope", "/x", nil, nil); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestEncodePathSegmentPlus(t *testing.T) {
	if got := EncodePathSegment("a+b"); got != "a%2Bb" {
		t.Fatalf("got %q", got)
	}
}

func TestEncodeQueryValue(t *testing.T) {
	if got := EncodeQueryValue("a b"); got != "a+b" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildQueryString(t *testing.T) {
	got := BuildQueryString(map[string]string{"b": "2", "a": "1"})
	if got != "a=1&b=2" {
		t.Fatalf("got %q", got)
	}
}
