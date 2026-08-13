// The project-specific link API and resolve routes, per AI.md PART 14
// "API Structure" and IDEA.md "Business logic" / "Endpoints". Covers:
//   - POST   /api/{api_version}/links            create (auto or custom slug)
//   - GET    /api/{api_version}/links/{slug}      link info
//   - PATCH  /api/{api_version}/links/{slug}      update (owner/operator token)
//   - DELETE /api/{api_version}/links/{slug}      delete (owner/operator token)
//   - GET    /api/{api_version}/links/{slug}/stats click analytics
//   - GET    /{slug}                              resolve/redirect (public)
//   - GET    /{slug}/stats                        vanity alias for stats
//
// The root-scope /{slug} and /{slug}/stats routes are registered directly
// by frontend.go's registerFrontendRoutes (AI.md PART 16), which wraps
// statsHandler with an HTML-negotiating variant for browsers and reuses
// resolveHandler unchanged; the JSON/text bodies here are still exactly
// what non-browser clients receive.
package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/security"
)

// linkDeps bundles what the link handlers need.
type linkDeps struct {
	sqlDB    *sql.DB
	resolver *ProxyResolver
}

// LinkResponse is the canonical public shape of a link, per the "Single
// Item Response" convention in AI.md PART 14.
type LinkResponse struct {
	ShortCode      string  `json:"short_code"`
	ShortURL       string  `json:"short_url"`
	DestinationURL string  `json:"destination_url"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      *string `json:"expires_at"`
	ClickCount     int64   `json:"click_count"`
}

// CreateLinkResponse adds the one-time owner token to LinkResponse, per
// IDEA.md "Business logic": "Anonymous POST creates a link ... and returns
// a one-time owner_token."
type CreateLinkResponse struct {
	LinkResponse
	OwnerToken string `json:"owner_token"`
}

func toLinkResponse(l *db.Link, shortURL string) LinkResponse {
	resp := LinkResponse{
		ShortCode:      l.ShortCode,
		ShortURL:       shortURL,
		DestinationURL: l.DestinationURL,
		CreatedAt:      l.CreatedAt.Format(time.RFC3339),
		ClickCount:     l.ClickCount,
	}
	if l.ExpiresAt != nil {
		s := l.ExpiresAt.Format(time.RFC3339)
		resp.ExpiresAt = &s
	}
	return resp
}

// registerLinkAPIRoutes mounts the link resource under apiGroup, already
// scoped to "/api/{api_version}", per AI.md PART 14 "Route Naming
// Convention" (plural noun, versioned).
func (ld *linkDeps) registerLinkAPIRoutes(apiGroup chi.Router) {
	apiGroup.Route("/links", func(sr chi.Router) {
		sr.Post("/", ld.createLinkHandler)
		sr.Get("/{slug}", ld.getLinkHandler)
		sr.Patch("/{slug}", ld.updateLinkHandler)
		sr.Delete("/{slug}", ld.deleteLinkHandler)
		sr.Get("/{slug}/stats", ld.statsHandler)
	})
}

// corsAPIMiddleware sets the CORS headers required on every API endpoint,
// per AI.md PART 14 "Authentication & CORS" / api-rules.md: "Access-
// Control-Allow-Origin: * for API endpoints".
func corsAPIMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key, API-Key, X-Auth-Token, X-Access-Token, X-Token, Token")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// createLinkRequest is the POST /links body.
type createLinkRequest struct {
	URL       string  `json:"url"`
	Slug      string  `json:"slug,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func (ld *linkDeps) createLinkHandler(w http.ResponseWriter, r *http.Request) {
	var req createLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apperr.SendError(w, apperr.New(apperr.CodeBadRequest))
		return
	}

	destination, ok := validateDestinationURL(req.URL)
	if !ok {
		apperr.SendError(w, apperr.New(apperr.CodeValidationFailed).WithDetails(map[string]any{
			"field": "url", "rule": "format",
		}))
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			apperr.SendError(w, apperr.New(apperr.CodeValidationFailed).WithDetails(map[string]any{
				"field": "expires_at", "rule": "rfc3339",
			}))
			return
		}
		expiresAt = &t
	}

	ctx := r.Context()
	var link *db.Link
	var err error

	if slug := strings.TrimSpace(req.Slug); slug != "" {
		if !security.ValidateSlugFormat(slug) {
			apperr.SendError(w, apperr.New(apperr.CodeValidationFailed).WithDetails(map[string]any{
				"field": "slug", "rule": "format",
			}))
			return
		}
		if security.IsReservedSlug(slug) {
			apperr.SendError(w, apperr.New(apperr.CodeConflict).WithMessage("Slug is reserved"))
			return
		}
		link, err = db.CreateLinkCustomSlug(ctx, ld.sqlDB, slug, destination, expiresAt)
		if errors.Is(err, db.ErrSlugTaken) {
			apperr.SendError(w, apperr.New(apperr.CodeConflict).WithMessage("Slug already in use"))
			return
		}
	} else {
		link, err = db.CreateLinkAutoCode(ctx, ld.sqlDB, destination, expiresAt)
	}
	if err != nil {
		apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
		return
	}

	raw, _, err := db.CreateResourceToken(ctx, ld.sqlDB, "link", strconv.FormatInt(link.ID, 10), nil)
	if err != nil {
		apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
		return
	}

	shortURL := ld.resolver.BuildURL(r, "/"+link.ShortCode)
	resp := CreateLinkResponse{
		LinkResponse: toLinkResponse(link, shortURL),
		OwnerToken:   raw,
	}

	if wantsText(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, "%s\n", shortURL)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(apperr.APIResponse{OK: true, Data: resp})
}

// validateDestinationURL requires an absolute http(s) URL with a host.
func validateDestinationURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	if u.Host == "" {
		return "", false
	}
	return raw, true
}

func (ld *linkDeps) getLinkHandler(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := lookupLink(r.Context(), ld.sqlDB, slug)
	if err != nil {
		apperr.SendError(w, mapLookupErr(err))
		return
	}

	shortURL := ld.resolver.BuildURL(r, "/"+link.ShortCode)
	resp := toLinkResponse(link, shortURL)

	if wantsText(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "short_code: %s\nshort_url: %s\ndestination_url: %s\nclick_count: %d\n",
			resp.ShortCode, resp.ShortURL, resp.DestinationURL, resp.ClickCount)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// updateLinkRequest is the PATCH /links/{slug} body. ExpiresAt semantics:
// absent (nil) leaves expiration untouched; present and empty ("") clears
// it; present and non-empty parses as RFC3339.
type updateLinkRequest struct {
	URL       *string `json:"url,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func (ld *linkDeps) updateLinkHandler(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := lookupLink(r.Context(), ld.sqlDB, slug)
	if err != nil {
		apperr.SendError(w, mapLookupErr(err))
		return
	}

	if !ld.authorized(r, link.ID) {
		apperr.SendError(w, apperr.New(apperr.CodeForbidden))
		return
	}

	var req updateLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apperr.SendError(w, apperr.New(apperr.CodeBadRequest))
		return
	}

	destination := link.DestinationURL
	if req.URL != nil {
		d, ok := validateDestinationURL(*req.URL)
		if !ok {
			apperr.SendError(w, apperr.New(apperr.CodeValidationFailed).WithDetails(map[string]any{
				"field": "url", "rule": "format",
			}))
			return
		}
		destination = d
	}

	var expiresAt *time.Time
	clearExpiry := false
	if req.ExpiresAt != nil {
		if strings.TrimSpace(*req.ExpiresAt) == "" {
			clearExpiry = true
		} else {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				apperr.SendError(w, apperr.New(apperr.CodeValidationFailed).WithDetails(map[string]any{
					"field": "expires_at", "rule": "rfc3339",
				}))
				return
			}
			expiresAt = &t
		}
	}

	if err := db.UpdateLinkDestination(r.Context(), ld.sqlDB, link.ID, destination, expiresAt, clearExpiry); err != nil {
		apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
		return
	}

	updated, err := db.GetLinkByID(r.Context(), ld.sqlDB, link.ID)
	if err != nil {
		apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
		return
	}

	shortURL := ld.resolver.BuildURL(r, "/"+updated.ShortCode)
	apperr.SendOK(w, map[string]any{
		"id":      updated.ShortCode,
		"message": "Link updated successfully",
		"link":    toLinkResponse(updated, shortURL),
	})
}

func (ld *linkDeps) deleteLinkHandler(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := lookupLink(r.Context(), ld.sqlDB, slug)
	if err != nil {
		apperr.SendError(w, mapLookupErr(err))
		return
	}

	if !ld.authorized(r, link.ID) {
		apperr.SendError(w, apperr.New(apperr.CodeForbidden))
		return
	}

	if err := db.DeleteLink(r.Context(), ld.sqlDB, link.ID); err != nil {
		apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
		return
	}

	apperr.SendOK(w, map[string]any{
		"id":      slug,
		"message": "Link deleted successfully",
	})
}

// authorized reports whether r carries either the operator token (checked
// upstream by authMiddleware, see ctxKeyOperator) or the resource owner
// token for linkID, per IDEA.md "Business logic": "that token ... authorizes
// later edits/deletes on that link only. The operator's global server.token
// can moderate/delete any link."
func (ld *linkDeps) authorized(r *http.Request, linkID int64) bool {
	if IsOperator(r.Context()) {
		return true
	}
	token := ExtractToken(r)
	if token == "" {
		return false
	}
	tok, err := db.LookupTokenByRaw(r.Context(), ld.sqlDB, token)
	if err != nil {
		return false
	}
	return tok.ResourceType == "link" && tok.ResourceID == strconv.FormatInt(linkID, 10)
}

// botUserAgentSubstrings identifies known bot/crawler user agents, per
// IDEA.md "Business rules": "Click tracking excludes known bot/crawler user
// agents." Not exhaustive — a minimal, commonly-seen working set.
var botUserAgentSubstrings = []string{
	"bot", "crawl", "spider", "slurp", "bingpreview", "facebookexternalhit",
	"whatsapp", "telegrambot", "discordbot", "pingdom", "uptimerobot",
	"monitor", "curl/", "wget/", "python-requests",
}

func isBotUserAgent(ua string) bool {
	ua = strings.ToLower(strings.TrimSpace(ua))
	if ua == "" {
		return true
	}
	for _, s := range botUserAgentSubstrings {
		if strings.Contains(ua, s) {
			return true
		}
	}
	return false
}

// resolveHandler is the public GET /{slug} route: 302 redirects to the
// link's destination, 410 Gone if expired, 404 if unknown. Per IDEA.md
// "Business rules": "Expired links resolve with 410 Gone instead of
// redirecting."
func (ld *linkDeps) resolveHandler(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := lookupLink(r.Context(), ld.sqlDB, slug)
	if err != nil {
		apperr.SendError(w, mapLookupErr(err))
		return
	}
	if link.IsExpired() {
		apperr.SendError(w, apperr.New(apperr.CodeGone))
		return
	}

	ua := r.Header.Get("User-Agent")
	if !isBotUserAgent(ua) {
		ip := ld.resolver.ResolveClientIP(r)
		_, _ = db.RecordClick(r.Context(), ld.sqlDB, link.ID, ip, ua, r.Header.Get("Referer"))
	}

	http.Redirect(w, r, link.DestinationURL, http.StatusFound)
}

// StatsResponse is the click-analytics payload for a link, per IDEA.md
// "Business logic": "total clicks, referrers, time series, approximate
// location (GeoIP)". GeoIP fields are always empty until AI.md PART 19
// lands (see TODO.AI.md).
type StatsResponse struct {
	ShortCode   string         `json:"short_code"`
	TotalClicks int64          `json:"total_clicks"`
	Referrers   map[string]int `json:"referrers"`
	TimeSeries  []DayCount     `json:"time_series"`
	Recent      []ClickInfo    `json:"recent"`
}

// DayCount is one point in the click time series (UTC calendar day).
type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// ClickInfo is a single click, IP already anonymized per IDEA.md "Data
// models" (never the raw IP — see security.AnonymizeIP / db.RecordClick).
type ClickInfo struct {
	Timestamp string `json:"timestamp"`
	IP        string `json:"ip"`
	Referrer  string `json:"referrer,omitempty"`
	Country   string `json:"country,omitempty"`
	Region    string `json:"region,omitempty"`
}

// statsMaxRows bounds how many recent clicks are fetched/aggregated per
// request, keeping the handler O(1) in the worst case for very
// high-traffic links.
const statsMaxRows = 1000

func (ld *linkDeps) statsHandler(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	link, err := lookupLink(r.Context(), ld.sqlDB, slug)
	if err != nil {
		apperr.SendError(w, mapLookupErr(err))
		return
	}

	clicks, err := db.ClicksForLink(r.Context(), ld.sqlDB, link.ID, statsMaxRows)
	if err != nil {
		apperr.SendError(w, apperr.Wrap(apperr.CodeServerError, err))
		return
	}

	resp := buildStatsResponse(link, clicks)

	if wantsText(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "short_code: %s\ntotal_clicks: %d\n", resp.ShortCode, resp.TotalClicks)
		for _, dc := range resp.TimeSeries {
			fmt.Fprintf(w, "clicks[%s]: %d\n", dc.Date, dc.Count)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

const statsRecentLimit = 50

func buildStatsResponse(link *db.Link, clicks []db.Click) StatsResponse {
	referrers := map[string]int{}
	byDay := map[string]int{}
	recent := make([]ClickInfo, 0, min(len(clicks), statsRecentLimit))

	for i, c := range clicks {
		ref := c.Referrer
		if ref == "" {
			ref = "(direct)"
		}
		referrers[ref]++
		day := c.Timestamp.Format("2006-01-02")
		byDay[day]++
		if i < statsRecentLimit {
			recent = append(recent, ClickInfo{
				Timestamp: c.Timestamp.Format(time.RFC3339),
				IP:        c.IP,
				Referrer:  c.Referrer,
				Country:   c.Country,
				Region:    c.Region,
			})
		}
	}

	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	series := make([]DayCount, 0, len(days))
	for _, d := range days {
		series = append(series, DayCount{Date: d, Count: byDay[d]})
	}

	return StatsResponse{
		ShortCode:   link.ShortCode,
		TotalClicks: link.ClickCount,
		Referrers:   referrers,
		TimeSeries:  series,
		Recent:      recent,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// lookupLink fetches a link by slug, converting db.ErrNotFound into a
// sentinel this package's handlers can map to the correct API error.
func lookupLink(ctx context.Context, sqlDB *sql.DB, slug string) (*db.Link, error) {
	return db.GetLinkByShortCode(ctx, sqlDB, slug)
}

func mapLookupErr(err error) *apperr.AppError {
	if errors.Is(err, db.ErrNotFound) {
		return apperr.New(apperr.CodeNotFound)
	}
	return apperr.Wrap(apperr.CodeServerError, err)
}
