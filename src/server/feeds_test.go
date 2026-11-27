package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
)

func TestFeedHandlersSharePublicLinksAndConditionalCaching(t *testing.T) {
	ld, sqlDB := testLinkDeps(t)
	slug, _ := createTestLink(t, ld)
	deps := &sitemapDeps{cfg: config.Default(""), resolver: ld.resolver, startTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), sqlDB: sqlDB}

	atomRec := httptest.NewRecorder()
	atomReq := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
	deps.atomFeedHandler(atomRec, atomReq)
	if atomRec.Code != http.StatusOK || atomRec.Header().Get("Content-Type") != "application/atom+xml; charset=utf-8" {
		t.Fatalf("Atom status/type = %d/%q, body=%s", atomRec.Code, atomRec.Header().Get("Content-Type"), atomRec.Body.String())
	}
	for _, want := range []string{"http://example.com/feed.xml", "http://example.com/" + slug, "Public short link created."} {
		if !strings.Contains(atomRec.Body.String(), want) {
			t.Errorf("Atom feed missing %q: %s", want, atomRec.Body.String())
		}
	}
	if atomRec.Header().Get("ETag") == "" || atomRec.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Errorf("Atom cache headers = %v", atomRec.Header())
	}

	rssRec := httptest.NewRecorder()
	deps.rssFeedHandler(rssRec, httptest.NewRequest(http.MethodGet, "/rss.xml", nil))
	if rssRec.Code != http.StatusOK || rssRec.Header().Get("Content-Type") != "application/rss+xml; charset=utf-8" {
		t.Fatalf("RSS status/type = %d/%q, body=%s", rssRec.Code, rssRec.Header().Get("Content-Type"), rssRec.Body.String())
	}
	if !strings.Contains(rssRec.Body.String(), "/"+slug) {
		t.Errorf("RSS feed does not contain the same public link: %s", rssRec.Body.String())
	}

	conditional := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
	conditional.Header.Set("If-None-Match", atomRec.Header().Get("ETag"))
	conditionalRec := httptest.NewRecorder()
	deps.atomFeedHandler(conditionalRec, conditional)
	if conditionalRec.Code != http.StatusNotModified || conditionalRec.Body.Len() != 0 {
		t.Errorf("conditional Atom response = %d with %d body bytes, want 304 empty", conditionalRec.Code, conditionalRec.Body.Len())
	}
}

func TestEmptyFeedsRemainValidAndXMLTextIsBounded(t *testing.T) {
	_, sqlDB := testLinkDeps(t)
	deps := &sitemapDeps{cfg: config.Default(""), resolver: NewProxyResolver(nil), startTime: time.Now().UTC(), sqlDB: sqlDB}
	rec := httptest.NewRecorder()
	deps.rssFeedHandler(rec, httptest.NewRequest(http.MethodGet, "/rss.xml", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<rss version=\"2.0\">") {
		t.Fatalf("empty RSS response = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := len([]rune(truncateXMLText(strings.Repeat("a", 200), 140))); got != 140 {
		t.Errorf("truncated title length = %d, want 140 runes", got)
	}
	if got := cleanXMLText("valid\x01text"); got != "validtext" {
		t.Errorf("cleanXMLText() = %q, want invalid XML character removed", got)
	}
	if _, _, err := db.ListSitemapLinks(context.Background(), sqlDB, 10, 0, time.Now().UTC()); err != nil {
		t.Fatalf("ListSitemapLinks() error = %v", err)
	}
}
