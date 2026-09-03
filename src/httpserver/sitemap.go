// /favicon.ico and /sitemap.xml, per AI.md PART 16 "Static Files" ->
// "Sitemap.xml" and the "/favicon.ico" row of the "Static Files" table.
package httpserver

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/server"
)

// sitemapDeps carries what /sitemap.xml needs to enumerate public pages
// and gate the ones that are conditionally registered.
type sitemapDeps struct {
	cfg       *config.Config
	resolver  *ProxyResolver
	startTime time.Time
}

// registerSitemapRoutes mounts /favicon.ico (served from the embedded
// default icon, per AI.md PART 16 "Static Files" -> "/favicon.ico" ->
// "Embedded default, customizable") and /sitemap.xml.
//
// server.branding.favicon (a local path or remote URL) is accepted by the
// config schema (PART 16 "Branding Configuration") but not yet wired here
// — the remote-fetch/multi-size scaling pipeline is a separate deferred
// item tracked in TODO.AI.md ("Remote branding/SEO image fetching"). The
// embedded default always serves today, satisfying the "embedded default"
// half of the spec row.
func (sd *sitemapDeps) registerSitemapRoutes(r chi.Router) {
	r.Get("/favicon.ico", faviconHandler)
	r.Head("/favicon.ico", faviconHandler)
	r.Get("/sitemap.xml", sd.sitemapHandler)
	r.Head("/sitemap.xml", sd.sitemapHandler)
}

// faviconHandler serves the embedded default favicon.ico from
// src/server/static/favicon.ico. It never 404s: the asset is embedded in
// the binary, so it is always present regardless of operator config.
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

// sitemapURL is one <url> entry.
type sitemapURL struct {
	Loc        string
	ChangeFreq string
	Priority   string
}

// sitemapHandler renders the dynamic sitemap, per AI.md PART 16 "Sitemap
// Generation Rules". Only static, always-public pages are listed —
// individual short links are user content, never enumerated (AI.md's own
// rule table lists no per-resource row for a project's dynamic content
// unless it is a published/public resource page, and PART 14 explicitly
// excludes API endpoints; a URL shortener's redirect slugs are neither).
// Login/consent/management-only endpoints are never included, per the
// "Authenticated server-management pages: NEVER" rule.
func (sd *sitemapDeps) sitemapHandler(w http.ResponseWriter, r *http.Request) {
	if !sd.cfg.Server.SEO.Sitemap.Enabled {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}

	lastmod := sd.startTime.UTC().Format("2006-01-02")
	entries := []sitemapURL{
		{Loc: "/", ChangeFreq: "daily", Priority: "1.0"},
		{Loc: "/list", ChangeFreq: "daily", Priority: "0.8"},
		{Loc: "/server/about", ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/help", ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/privacy", ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/terms", ChangeFreq: "weekly", Priority: "0.8"},
		{Loc: "/server/contact", ChangeFreq: "weekly", Priority: "0.6"},
		{Loc: "/server/security", ChangeFreq: "weekly", Priority: "0.6"},
		{Loc: "/server/security/policy", ChangeFreq: "weekly", Priority: "0.6"},
		{Loc: "/server/security/thanks", ChangeFreq: "weekly", Priority: "0.6"},
	}
	if sd.cfg.Server.Privacy.Data.Sold {
		entries = append(entries, sitemapURL{Loc: "/server/ccpa", ChangeFreq: "weekly", Priority: "0.6"})
	}
	if sd.cfg.Server.Compliance.RequiresDPOContact() {
		entries = append(entries, sitemapURL{Loc: "/server/dpo", ChangeFreq: "weekly", Priority: "0.6"})
	}

	maxURLs := sd.cfg.Server.SEO.Sitemap.MaxURLs
	if maxURLs > 0 && len(entries) > maxURLs {
		entries = entries[:maxURLs]
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "  <url>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>%s</changefreq>\n    <priority>%s</priority>\n  </url>\n",
			sd.resolver.BuildURL(r, e.Loc), lastmod, e.ChangeFreq, e.Priority)
	}
	b.WriteString(`</urlset>` + "\n")

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}
