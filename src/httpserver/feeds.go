package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apimgr/shortner/src/db"
)

const feedDescription = "Recent public short links"

type feedEntry struct {
	Title   string
	Link    string
	Updated time.Time
	Summary string
}

type atomFeed struct {
	XMLName xml.Name       `xml:"feed"`
	XMLNS   string         `xml:"xmlns,attr"`
	ID      string         `xml:"id"`
	Title   string         `xml:"title"`
	Updated string         `xml:"updated"`
	Links   []atomFeedLink `xml:"link"`
	Entries []atomEntry    `xml:"entry"`
}

type atomFeedLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}

type atomEntry struct {
	ID        string         `xml:"id"`
	Title     string         `xml:"title"`
	Links     []atomFeedLink `xml:"link"`
	Updated   string         `xml:"updated"`
	Published string         `xml:"published"`
	Summary   string         `xml:"summary"`
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string     `xml:"title"`
	Link        string     `xml:"link"`
	Description string     `xml:"description"`
	LastBuild   string     `xml:"lastBuildDate"`
	Items       []rssEntry `xml:"item"`
}

type rssEntry struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Published   string `xml:"pubDate"`
	Description string `xml:"description"`
}

func (sd *sitemapDeps) registerFeedRoutes(r interface {
	Get(string, http.HandlerFunc)
	Head(string, http.HandlerFunc)
}) {
	r.Get("/feed.xml", sd.atomFeedHandler)
	r.Head("/feed.xml", sd.atomFeedHandler)
	r.Get("/rss.xml", sd.rssFeedHandler)
	r.Head("/rss.xml", sd.rssFeedHandler)
}

func (sd *sitemapDeps) atomFeedHandler(w http.ResponseWriter, r *http.Request) {
	entries, lastUpdated, err := sd.feedEntries(r)
	if err != nil {
		sendError(w, r, mapLookupErr(err))
		return
	}
	base := sd.resolver.BuildURL(r, "/")
	title := sd.feedTitle()
	updated := lastUpdated
	if updated.IsZero() {
		updated = sd.startTime.UTC()
	}
	feed := atomFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		ID:      base,
		Title:   title,
		Updated: updated.UTC().Format(time.RFC3339),
		Links: []atomFeedLink{
			{Href: base, Rel: "alternate"},
			{Href: sd.resolver.BuildURL(r, "/feed.xml"), Rel: "self"},
		},
	}
	for _, entry := range entries {
		feed.Entries = append(feed.Entries, atomEntry{
			ID: entry.Link, Title: entry.Title,
			Links:     []atomFeedLink{{Href: entry.Link}},
			Updated:   entry.Updated.UTC().Format(time.RFC3339),
			Published: entry.Updated.UTC().Format(time.RFC3339),
			Summary:   entry.Summary,
		})
	}
	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		sendError(w, r, mapLookupErr(err))
		return
	}
	sd.serveFeed(w, r, "application/atom+xml; charset=utf-8", body, lastUpdated)
}

func (sd *sitemapDeps) rssFeedHandler(w http.ResponseWriter, r *http.Request) {
	entries, lastUpdated, err := sd.feedEntries(r)
	if err != nil {
		sendError(w, r, mapLookupErr(err))
		return
	}
	base := sd.resolver.BuildURL(r, "/")
	lastBuild := lastUpdated
	if lastBuild.IsZero() {
		lastBuild = sd.startTime.UTC()
	}
	feed := rssFeed{Version: "2.0", Channel: rssChannel{
		Title: sd.feedTitle(), Link: base, Description: feedDescription,
		LastBuild: lastBuild.UTC().Format(time.RFC1123Z),
	}}
	for _, entry := range entries {
		feed.Channel.Items = append(feed.Channel.Items, rssEntry{
			Title: entry.Title, Link: entry.Link, GUID: entry.Link,
			Published:   entry.Updated.UTC().Format(time.RFC1123Z),
			Description: entry.Summary,
		})
	}
	body, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		sendError(w, r, mapLookupErr(err))
		return
	}
	sd.serveFeed(w, r, "application/rss+xml; charset=utf-8", body, lastUpdated)
}

func (sd *sitemapDeps) feedEntries(r *http.Request) ([]feedEntry, time.Time, error) {
	limit := sd.cfg.Server.SEO.Feed.MaxItems
	if limit < 1 || limit > 200 {
		limit = 50
	}
	items, total, err := db.ListSitemapLinks(r.Context(), sd.sqlDB, limit, 0, time.Now().UTC())
	if err != nil {
		return nil, time.Time{}, err
	}
	if total > int64(len(items)) {
		items, _, err = db.ListSitemapLinks(r.Context(), sd.sqlDB, limit, int(total)-len(items), time.Now().UTC())
		if err != nil {
			return nil, time.Time{}, err
		}
	}
	entries := make([]feedEntry, 0, len(items))
	var lastUpdated time.Time
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		permalink := sd.resolver.BuildURL(r, "/"+item.ShortCode)
		title := truncateXMLText(item.ShortCode, 140)
		entries = append(entries, feedEntry{
			Title: title, Link: permalink, Updated: item.CreatedAt.UTC(),
			Summary: "Public short link created.",
		})
		if item.CreatedAt.After(lastUpdated) {
			lastUpdated = item.CreatedAt.UTC()
		}
	}
	return entries, lastUpdated, nil
}

func (sd *sitemapDeps) serveFeed(w http.ResponseWriter, r *http.Request, contentType string, body []byte, modified time.Time) {
	clean := cleanXMLText(string(body))
	body = []byte(xml.Header + clean)
	hash := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(hash[:]) + `"`
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", etag)
	if strings.Contains(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if modified.IsZero() {
		modified = sd.startTime.UTC()
	}
	http.ServeContent(w, r, "feed.xml", modified, bytes.NewReader(body))
}

func (sd *sitemapDeps) feedTitle() string {
	if title := strings.TrimSpace(sd.cfg.Server.SEO.Feed.Title); title != "" {
		return truncateXMLText(title, 140)
	}
	if title := strings.TrimSpace(sd.branding.load(sd.cfg).Branding.Title); title != "" {
		return truncateXMLText(title, 140)
	}
	return "Shortner"
}

func truncateXMLText(value string, maxRunes int) string {
	value = cleanXMLText(value)
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

func cleanXMLText(value string) string {
	var cleaned strings.Builder
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		if r == utf8.RuneError && size == 1 {
			continue
		}
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || (r >= 0x10000 && r <= 0x10FFFF) {
			cleaned.WriteRune(r)
		}
	}
	return cleaned.String()
}
