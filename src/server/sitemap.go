// /favicon.ico and /sitemap.xml, per AI.md PART 16 "Static Files" ->
// "Sitemap.xml" and the "/favicon.ico" row of the "Static Files" table.
package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
)

const sitemapProtocolLimit = 50000

// sitemapDeps carries the configuration and database access needed to
// enumerate public pages and currently-resolvable public links.
type sitemapDeps struct {
	cfg       *config.Config
	resolver  *ProxyResolver
	startTime time.Time
	sqlDB     db.Handle
	configDir string
	cacheDir  string
	brandMu   sync.Mutex
	branding  *brandingStateStore
}

// registerSitemapRoutes mounts the embedded favicon and sitemap index pages.
func (sd *sitemapDeps) registerSitemapRoutes(r chi.Router) {
	r.Get("/favicon.ico", faviconHandler)
	r.Head("/favicon.ico", faviconHandler)
	r.Get("/branding/favicon", sd.brandingAssetHandler("favicon"))
	r.Head("/branding/favicon", sd.brandingAssetHandler("favicon"))
	r.Get("/branding/logo", sd.brandingAssetHandler("logo"))
	r.Head("/branding/logo", sd.brandingAssetHandler("logo"))
	r.Get("/branding/og-image", sd.brandingAssetHandler("og-image"))
	r.Head("/branding/og-image", sd.brandingAssetHandler("og-image"))
	sd.registerFeedRoutes(r)
	r.Get("/sitemap.xml", sd.sitemapHandler)
	r.Head("/sitemap.xml", sd.sitemapHandler)
	r.Get("/sitemap-{part:[0-9]+}.xml", sd.sitemapPartHandler)
	r.Head("/sitemap-{part:[0-9]+}.xml", sd.sitemapPartHandler)
}

func (sd *sitemapDeps) brandingAssetHandler(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings := sd.branding.load(sd.cfg)
		var value string
		switch kind {
		case "logo":
			value = settings.Branding.Logo
		case "og-image":
			value = settings.SEO.OGImage
		default:
			value = settings.Branding.Favicon
		}
		var body io.ReadSeeker
		var modTime time.Time
		var contentType string
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "https://") {
			data, mimeType, modified, err := sd.remoteBrandingImage(r.Context(), value)
			if err != nil {
				data, mimeType, err = embeddedBrandingFallback(kind)
				if err != nil {
					sendError(w, r, apperr.New(apperr.CodeNotFound))
					return
				}
				modified = time.Time{}
			}
			body, contentType, modTime = bytes.NewReader(data), mimeType, modified
		} else if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:") {
			data, mimeType, err := decodeBrandingDataURI(value)
			if err != nil {
				sendError(w, r, apperr.New(apperr.CodeNotFound))
				return
			}
			body, contentType = bytes.NewReader(data), mimeType
		} else {
			path, err := configuredBrandingPath(sd.configDir, value)
			if err != nil {
				sendError(w, r, apperr.New(apperr.CodeNotFound))
				return
			}
			file, err := os.Open(path)
			if err != nil {
				sendError(w, r, apperr.New(apperr.CodeNotFound))
				return
			}
			defer func() { _ = file.Close() }()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
				sendError(w, r, apperr.New(apperr.CodeNotFound))
				return
			}
			contentType = mimeTypeForBranding(path)
			if contentType == "" {
				sendError(w, r, apperr.New(apperr.CodeNotFound))
				return
			}
			body, modTime = file, info.ModTime()
		}
		variant, validVariant := requestedBrandingVariant(kind, r)
		if !validVariant {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		if variant != "" {
			if data, err := io.ReadAll(io.LimitReader(body, (10<<20)+1)); err == nil && len(data) <= 10<<20 {
				if scaled, scaleErr := sd.scaledBrandingImage(data, variant); scaleErr == nil {
					body, contentType = bytes.NewReader(scaled), "image/png"
				} else if contentType != "image/svg+xml" && contentType != "image/x-icon" {
					sendError(w, r, apperr.New(apperr.CodeNotFound))
					return
				} else {
					body = bytes.NewReader(data)
				}
			} else {
				sendError(w, r, apperr.New(apperr.CodeNotFound))
				return
			}
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeContent(w, r, "branding-image", modTime, body)
	}
}

func requestedBrandingVariant(kind string, r *http.Request) (string, bool) {
	if value := r.URL.Query().Get("width"); value != "" {
		return "width=" + value, kind == "logo" && (value == "50" || value == "200")
	}
	if value := r.URL.Query().Get("size"); value != "" {
		if kind == "og-image" {
			return "size=" + value, value == "1200x630"
		}
		valid := value == "16" || value == "32" || value == "48" || value == "180" || value == "192" || value == "512"
		return "size=" + value, kind == "favicon" && valid
	}
	return "", true
}

func embeddedBrandingFallback(kind string) ([]byte, string, error) {
	path, contentType := "static/icons/icon-192.png", "image/png"
	switch kind {
	case "favicon":
		path, contentType = "static/favicon.ico", "image/x-icon"
	case "og-image":
		path = "static/icons/icon-512.png"
	}
	body, err := fs.ReadFile(StaticFS, path)
	return body, contentType, err
}

func configuredBrandingPath(configDir, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || filepath.Base(value) != value || strings.ContainsAny(value, `/\\`) {
		return "", os.ErrNotExist
	}
	ext := strings.ToLower(filepath.Ext(value))
	switch ext {
	case ".svg", ".png", ".ico", ".webp", ".jpg":
	default:
		return "", os.ErrNotExist
	}
	if configDir == "" {
		return "", os.ErrNotExist
	}
	root, err := filepath.EvalSymlinks(filepath.Join(configDir, "assets"))
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, value)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", os.ErrNotExist
	}
	return resolved, nil
}

func mimeTypeForBranding(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ico":
		return "image/x-icon"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

// faviconHandler serves the embedded default icon from the binary.
func faviconHandler(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(StaticFS, "static/favicon.ico")
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "no-cache")
	if stamp := w.Header().Get("X-Build"); stamp != "" {
		etag := buildETag(stamp)
		w.Header().Set("ETag", etag)
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
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
		{Loc: "/server/docs/swagger", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.7"},
		{Loc: "/server/docs/graphql", Lastmod: lastmod, ChangeFreq: "weekly", Priority: "0.7"},
	}
	allowed := entries[:0]
	for _, entry := range entries {
		if sd.robotsAllows(entry.Loc) {
			allowed = append(allowed, entry)
		}
	}
	return allowed
}

func (sd *sitemapDeps) sitemapHandler(w http.ResponseWriter, r *http.Request) {
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
	var allowed int64
	for offset := 0; ; offset += 500 {
		links, _, err := db.ListSitemapLinks(ctx, sd.sqlDB, 500, offset, time.Now().UTC())
		if err != nil {
			return 0, err
		}
		for _, link := range links {
			if sd.robotsAllows("/" + link.ShortCode) {
				allowed++
			}
		}
		if len(links) < 500 {
			return allowed, nil
		}
	}
}

func (sd *sitemapDeps) sitemapLinks(ctx context.Context, limit, offset int) ([]db.SitemapLink, error) {
	if sd.sqlDB == nil || limit == 0 {
		return nil, nil
	}
	const batchSize = 500
	links := make([]db.SitemapLink, 0, limit)
	allowedOffset := 0
	for dbOffset := 0; len(links) < limit; dbOffset += batchSize {
		batch, _, err := db.ListSitemapLinks(ctx, sd.sqlDB, batchSize, dbOffset, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, link := range batch {
			if !sd.robotsAllows("/" + link.ShortCode) {
				continue
			}
			if allowedOffset < offset {
				allowedOffset++
				continue
			}
			links = append(links, link)
			if len(links) == limit {
				break
			}
		}
		if len(batch) < batchSize {
			break
		}
	}
	return links, nil
}

func (sd *sitemapDeps) robotsAllows(path string) bool {
	allow := false
	for _, rule := range sd.cfg.Web.Robots.Allow {
		if robotsPathMatches(rule, path) {
			allow = true
			break
		}
	}
	if !allow {
		return false
	}
	for _, rule := range sd.cfg.Web.Robots.Deny {
		if robotsPathMatches(rule, path) {
			return false
		}
	}
	return true
}

func robotsPathMatches(rule, path string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	if rule == "/" {
		return strings.HasPrefix(path, "/")
	}
	rule = strings.TrimSuffix(rule, "/")
	return path == rule || strings.HasPrefix(path, rule+"/")
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
