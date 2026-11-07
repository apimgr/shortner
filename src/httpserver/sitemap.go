// /favicon.ico and /sitemap.xml, per AI.md PART 16 "Static Files" ->
// "Sitemap.xml" and the "/favicon.ico" row of the "Static Files" table.
package httpserver

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/server"
)

const sitemapProtocolLimit = 50000

// sitemapDeps carries the configuration and database access needed to
// enumerate public pages and currently-resolvable public links.
type sitemapDeps struct {
	cfg       *config.Config
	resolver  *ProxyResolver
	startTime time.Time
	sqlDB     *sql.DB
}

// registerSitemapRoutes mounts the embedded favicon and sitemap index pages.
func (sd *sitemapDeps) registerSitemapRoutes(r chi.Router) {
	r.Get("/favicon.ico", faviconHandler)
	r.Head("/favicon.ico", faviconHandler)
	r.Get("/sitemap.xml", sd.sitemapHandler)
	r.Head("/sitemap.xml", sd.sitemapHandler)
	r.Get("/sitemap-{part:[0-9]+}.xml", sd.sitemapPartHandler)
	r.Head("/sitemap-{part:[0-9]+}.xml", sd.sitemapPartHandler)
}

// faviconHandler serves the embedded default icon from the binary.
func faviconHandler(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(server.StaticFS, "static/favicon.ico")
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeContent(w, r, "favicon.ico", time.Time{}, strings.NewReader(string(b)))
}

type sitemapURL struct {
	Loc        string
	Lastmod    string
	ChangeFreq string
	Priority   string
}

func (sd *sitemapDeps) staticSitemapEntries() []sitemapURL {
	lastmod := sd.startTime.UTC().Format("2006-01-02")
	entries := []sitemapURL{
		{Loc: "/", Lastmod: lastmod, ChangeFreq: "daily", Priority: "1.0"},
		{Loc: "/list", Lastmod: lastmod, ChangeFreq: "daily", Priority: "0.8"},
		{Loc: "/server/about", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/help", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/privacy", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/terms", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/contact", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.6"},
		{Loc: "/server/security", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.6"},
		{Loc: "/server/security/policy", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.6"},
		{Loc: "/server/security/thanks", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.6"},
	}
	if sd.cfg.Server.Privacy.Data.Sold {
		entries = append(entries, sitemapURL{Loc: "/server/ccpa", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.6"})
	}
	if sd.cfg.Server.Compliance.RequiresDPOContact() {
		entries = append(entries, sitemapURL{Loc: "/server/dpo", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.6"})
	}
	return entries
}

func (sd *sitemapDeps) sitemapHandler(w http.ResponseWriter, r *http.Request) {
	if !sd.cfg.Server.SEO.Sitemap.Enabled {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	static := sd.staticSitemapEntries()
	linkCount, err := sd.countSitemapLinks(r.Context())
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	maxURLs := sd.maxURLs()
	total := int64(len(static)) + linkCount*2
	if total > int64(maxURLs) {
		sd.writeSitemapIndex(w, r, maxURLs, total)
		return
	}
	linkLimit := (maxURLs-len(static)+1)/2 + 1
	links, err := sd.sitemapLinks(r.Context(), linkLimit, 0)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	entries := sd.sitemapPageEntries(static, links, 0, maxURLs, 0)
	sd.writeURLSet(w, r, entries)
}

func (sd *sitemapDeps) sitemapPartHandler(w http.ResponseWriter, r *http.Request) {
	if !sd.cfg.Server.SEO.Sitemap.Enabled {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	part, err := strconv.Atoi(chi.URLParam(r, "part"))
	if err != nil || part < 1 {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	static := sd.staticSitemapEntries()
	linkCount, err := sd.countSitemapLinks(r.Context())
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	maxURLs := sd.maxURLs()
	total := int64(len(static)) + linkCount*2
	partCount := (total + int64(maxURLs) - 1) / int64(maxURLs)
	if int64(part) > partCount {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	start := (part - 1) * maxURLs
	linksStart := start - len(static)
	if linksStart < 0 {
		linksStart = 0
	}
	firstLink := linksStart / 2
	linkOffset := 0
	if linksStart > 0 {
		linkOffset = firstLink
	}
	linkLimit := maxURLs/2 + 2
	links, err := sd.sitemapLinks(r.Context(), linkLimit, linkOffset)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	entries := sd.sitemapPageEntries(static, links, start, maxURLs, linkOffset)
	sd.writeURLSet(w, r, entries)
}

func (sd *sitemapDeps) maxURLs() int {
	limit := sd.cfg.Server.SEO.Sitemap.MaxURLs
	if limit < 1 || limit > sitemapProtocolLimit {
		return sitemapProtocolLimit
	}
	return limit
}

func (sd *sitemapDeps) countSitemapLinks(ctx context.Context) (int64, error) {
	if sd.sqlDB == nil {
		return 0, nil
	}
	_, total, err := db.ListSitemapLinks(ctx, sd.sqlDB, 0, 0, time.Now().UTC())
	return total, err
}

func (sd *sitemapDeps) sitemapLinks(ctx context.Context, limit, offset int) ([]db.SitemapLink, error) {
	if sd.sqlDB == nil || limit == 0 {
		return nil, nil
	}
	links, _, err := db.ListSitemapLinks(ctx, sd.sqlDB, limit, offset, time.Now().UTC())
	return links, err
}

func (sd *sitemapDeps) sitemapPageEntries(static []sitemapURL, links []db.SitemapLink, start, limit, linkOffset int) []sitemapURL {
	allStatic := len(static)
	end := start + limit
	entries := make([]sitemapURL, 0, limit)
	if start < allStatic {
		staticEnd := end
		if staticEnd > allStatic {
			staticEnd = allStatic
		}
		entries = append(entries, static[start:staticEnd]...)
	}
	linkEntryStart := start - allStatic
	if linkEntryStart < 0 {
		linkEntryStart = 0
	}
	linkEntryEnd := end - allStatic
	if linkEntryEnd <= 0 {
		return entries
	}
	for entry := linkEntryStart; entry < linkEntryEnd; entry++ {
		linkIndex := entry/2 - linkOffset
		if linkIndex < 0 || linkIndex >= len(links) {
			break
		}
		link := links[linkIndex]
		path := "/" + link.ShortCode
		if entry%2 == 1 {
			path += "/stats"
		}
		entries = append(entries, sitemapURL{Loc: path, Lastmod: link.CreatedAt.Format("2006-01-02"), ChangeFreq: "weekly", Priority: "0.6"})
	}
	return entries
}

func (sd *sitemapDeps) writeSitemapIndex(w http.ResponseWriter, r *http.Request, maxURLs int, total int64) {
	parts := (total + int64(maxURLs) - 1) / int64(maxURLs)
	lastmod := sd.startTime.UTC().Format("2006-01-02")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for part := int64(1); part <= parts; part++ {
		b.WriteString("  <sitemap><loc>")
		writeXMLEscaped(&b, sd.resolver.BuildURL(r, fmt.Sprintf("/sitemap-%d.xml", part)))
		fmt.Fprintf(&b, "</loc><lastmod>%s</lastmod></sitemap>\n", lastmod)
	}
	b.WriteString(`</sitemapindex>` + "\n")
	sd.writeXML(w, b.String())
}

func (sd *sitemapDeps) writeURLSet(w http.ResponseWriter, r *http.Request, entries []sitemapURL) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, entry := range entries {
		b.WriteString("  <url><loc>")
		writeXMLEscaped(&b, sd.resolver.BuildURL(r, entry.Loc))
		fmt.Fprintf(&b, "</loc><lastmod>%s</lastmod><changefreq>%s</changefreq><priority>%s</priority></url>\n", entry.Lastmod, entry.ChangeFreq, entry.Priority)
	}
	b.WriteString(`</urlset>` + "\n")
	sd.writeXML(w, b.String())
}

func writeXMLEscaped(b *strings.Builder, value string) {
	_ = xml.EscapeText(b, []byte(value))
}

func (sd *sitemapDeps) writeXML(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(value))
}
