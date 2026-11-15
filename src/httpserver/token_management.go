package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
)

const ownerTokenCookieName = "shortner_owner_token_Lu2YwQRQ"

type tokenCard struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Masked   string `json:"masked"`
	Created  string `json:"created"`
	LastUsed string `json:"last_used"`
	Expires  string `json:"expires"`
	Status   string `json:"status"`
	Revealed string `json:"revealed,omitempty"`
}

type tokenPageData struct {
	Base          PageData
	Restore       bool
	Primary       tokenCard
	Linked        []tokenCard
	NewToken      string
	Notice        string
	ResourceLabel string
	OwnerID       string
}

func (fd *frontendDeps) tokenPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, primary, ok := fd.webPrimaryOwner(w, r)
	data := tokenPageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "token_page.title"), fd.cfg.Server.SEO.Description), Restore: !ok, ResourceLabel: "items"}
	if ok {
		data.OwnerID = owner.ID
		data.Primary = tokenCardFor(primary, "")
		items, err := db.ListOwnerTokens(r.Context(), fd.ld.sqlDB, owner.ID)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		for _, item := range items {
			if item.Kind == "linked" {
				data.Linked = append(data.Linked, tokenCardFor(&item, ""))
			}
		}
		setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, rCookieValue(r, ownerTokenCookieName))
	}
	if err := renderPage(w, http.StatusOK, "tokens", data); err != nil {
		sendError(w, r, apperr.New(apperr.CodeServerError))
	}
}

func (fd *frontendDeps) tokenRestoreHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	raw := strings.TrimSpace(r.FormValue("token"))
	tok, err := db.LookupTokenByRaw(r.Context(), fd.ld.sqlDB, raw)
	if err != nil || tok.Kind != "primary" {
		fd.auditToken(r, "token.restored", "", "", applog.ResultFailure)
		fd.renderTokensNotice(w, r, "token_page.restore_failed", http.StatusOK)
		return
	}
	setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, raw)
	fd.auditToken(r, "token.restored", tok.OwnerID, tok.TokenPrefix, applog.ResultSuccess)
	fd.tokenRedirect(w, r, "/server/preferences/tokens#primary")
}

func (fd *frontendDeps) tokenForgetHandler(w http.ResponseWriter, r *http.Request) {
	clearOwnerTokenCookie(w, r, fd.cfg, fd.resolver)
	fd.tokenRedirect(w, r, "/server/preferences/tokens")
}

func (fd *frontendDeps) tokenPrimaryRevealHandler(w http.ResponseWriter, r *http.Request) {
	owner, tok, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	raw, err := db.RevealToken(tok, fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	fd.auditToken(r, "token.revealed", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	fd.renderTokenPageWithReveal(w, r, owner, tok, 0, raw)
}

func (fd *frontendDeps) tokenPrimaryRotateHandler(w http.ResponseWriter, r *http.Request) {
	owner, tok, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	if r.FormValue("confirm") != "rotate" {
		fd.tokenRedirect(w, r, "/server/preferences/tokens#primary")
		return
	}
	raw, next, err := db.RotateToken(r.Context(), fd.ld.sqlDB, tok.ID, owner.ID, "primary", fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, raw)
	fd.auditToken(r, "token.rotated", owner.ID, next.TokenPrefix, applog.ResultSuccess)
	fd.renderTokenPageWithReveal(w, r, owner, next, 0, raw)
}

func (fd *frontendDeps) tokenCreateHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	if err := r.ParseForm(); err != nil {
		fd.tokenRedirect(w, r, "/server/preferences/tokens#linked")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	expiry, err := parseTokenExpiry(r.FormValue("expiry"), r.FormValue("expires_on"))
	if err != nil {
		fd.renderTokensNotice(w, r, "token_page.invalid_expiry", http.StatusOK)
		return
	}
	maximum := fd.cfg.Server.Tokens.MaxLinked
	if maximum <= 0 {
		maximum = 25
	}
	raw, tok, err := db.CreateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, name, expiry, maximum, fd.ld.tokenKey)
	if err != nil {
		fd.renderTokensNotice(w, r, "token_page.create_failed", http.StatusOK)
		return
	}
	fd.auditToken(r, "token.created", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	fd.renderTokenPageWithReveal(w, r, owner, nil, tok.ID, raw)
}

func (fd *frontendDeps) tokenLinkedRevealHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	id := tokenRouteID(r)
	tok, err := db.GetTokenByID(r.Context(), fd.ld.sqlDB, id)
	if err != nil || tok.OwnerID != owner.ID || tok.Kind != "linked" {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	raw, err := db.RevealToken(tok, fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	fd.auditToken(r, "token.revealed", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	fd.renderTokenPageWithReveal(w, r, owner, nil, id, raw)
}

func (fd *frontendDeps) tokenLinkedRotateHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	if r.FormValue("confirm") != "rotate" {
		fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+chi.URLParam(r, "id"))
		return
	}
	id := tokenRouteID(r)
	raw, tok, err := db.RotateToken(r.Context(), fd.ld.sqlDB, id, owner.ID, "linked", fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	fd.auditToken(r, "token.rotated", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	fd.renderTokenPageWithReveal(w, r, owner, nil, id, raw)
}

func (fd *frontendDeps) tokenLinkedUpdateHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	if err := r.ParseForm(); err != nil {
		fd.tokenRedirect(w, r, "/server/preferences/tokens#linked")
		return
	}
	expiry, err := parseTokenExpiry(r.FormValue("expiry"), r.FormValue("expires_on"))
	if err != nil {
		fd.renderTokensNotice(w, r, "token_page.invalid_expiry", http.StatusOK)
		return
	}
	id := tokenRouteID(r)
	if err := db.UpdateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, id, r.FormValue("name"), expiry, r.FormValue("expiry") == "never"); err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+strconv.FormatInt(id, 10))
}

func (fd *frontendDeps) tokenLinkedRevokeHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	if r.FormValue("confirm") != "revoke" {
		fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+strconv.FormatInt(tokenRouteID(r), 10))
		return
	}
	id := tokenRouteID(r)
	tok, err := db.GetTokenByID(r.Context(), fd.ld.sqlDB, id)
	if err != nil || tok.OwnerID != owner.ID || tok.Kind != "linked" {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	if err := db.RevokeLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, id); err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	fd.auditToken(r, "token.revoked", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	fd.tokenRedirect(w, r, "/server/preferences/tokens#linked")
}

func (fd *frontendDeps) tokenAPISnapshotHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.apiPrimaryOwner(w, r)
	if !ok {
		return
	}
	tokens, err := db.ListOwnerTokens(r.Context(), fd.ld.sqlDB, owner.ID)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	data := make([]tokenCard, 0, len(tokens))
	for i := range tokens {
		data = append(data, tokenCardFor(&tokens[i], ""))
	}
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, map[string]any{"tokens": data})
}

func (fd *frontendDeps) tokenAPICreateHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.apiPrimaryOwner(w, r)
	if !ok {
		return
	}
	var input struct {
		Name      string `json:"name"`
		Expiry    string `json:"expiry"`
		ExpiresOn string `json:"expires_on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		sendError(w, r, apperr.New(apperr.CodeBadRequest))
		return
	}
	expiry, err := parseTokenExpiry(input.Expiry, input.ExpiresOn)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	maximum := fd.cfg.Server.Tokens.MaxLinked
	if maximum <= 0 {
		maximum = 25
	}
	raw, tok, err := db.CreateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, input.Name, expiry, maximum, fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	fd.auditToken(r, "token.created", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, map[string]any{"token": raw, "data": tokenCardFor(tok, "")})
}

func (fd *frontendDeps) tokenAPIActionHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.apiPrimaryOwner(w, r)
	if !ok {
		return
	}
	id := tokenRouteID(r)
	if strings.HasSuffix(r.URL.Path, "/primary/reveal") || strings.HasSuffix(r.URL.Path, "/primary/rotate") {
		tok, err := db.GetPrimaryToken(r.Context(), fd.ld.sqlDB, owner.ID)
		if err != nil {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		id = tok.ID
	}
	primaryRoute := strings.Contains(r.URL.Path, "/primary/")
	if strings.HasSuffix(r.URL.Path, "/reveal") {
		tok, err := db.GetTokenByID(r.Context(), fd.ld.sqlDB, id)
		if err != nil || tok.OwnerID != owner.ID || (!primaryRoute && tok.Kind != "linked") {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		raw, err := db.RevealToken(tok, fd.ld.tokenKey)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		fd.auditToken(r, "token.revealed", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
		w.Header().Set("Cache-Control", "no-store")
		apperr.SendOK(w, map[string]string{"token": raw})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/rotate") {
		tok, err := db.GetTokenByID(r.Context(), fd.ld.sqlDB, id)
		if err != nil || tok.OwnerID != owner.ID || (!primaryRoute && tok.Kind != "linked") {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		raw, next, err := db.RotateToken(r.Context(), fd.ld.sqlDB, id, owner.ID, tok.Kind, fd.ld.tokenKey)
		if err != nil {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		fd.auditToken(r, "token.rotated", owner.ID, next.TokenPrefix, applog.ResultSuccess)
		w.Header().Set("Cache-Control", "no-store")
		apperr.SendOK(w, map[string]string{"token": raw})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/revoke") || r.Method == http.MethodDelete {
		tok, err := db.GetTokenByID(r.Context(), fd.ld.sqlDB, id)
		if err != nil || tok.OwnerID != owner.ID || tok.Kind != "linked" {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		if err := db.RevokeLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, id); err != nil {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		fd.auditToken(r, "token.revoked", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
		apperr.SendOK(w, map[string]bool{"revoked": true})
		return
	}
	var input struct {
		Name      string `json:"name"`
		Expiry    string `json:"expiry"`
		ExpiresOn string `json:"expires_on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		sendError(w, r, apperr.New(apperr.CodeBadRequest))
		return
	}
	expiry, err := parseTokenExpiry(input.Expiry, input.ExpiresOn)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	if err := db.UpdateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, id, input.Name, expiry, input.Expiry == "never"); err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	apperr.SendOK(w, map[string]bool{"updated": true})
}

func (fd *frontendDeps) apiPrimaryOwner(w http.ResponseWriter, r *http.Request) (*db.Owner, *db.APIToken, bool) {
	raw := bearerToken(r)
	if raw == "" {
		sendError(w, r, apperr.New(apperr.CodeUnauthorized))
		return nil, nil, false
	}
	tok, err := db.LookupTokenByRaw(r.Context(), fd.ld.sqlDB, raw)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeUnauthorized))
		return nil, nil, false
	}
	if tok.Kind != "primary" {
		sendError(w, r, apperr.New(apperr.CodeForbidden))
		return nil, nil, false
	}
	owner, err := db.GetOwner(r.Context(), fd.ld.sqlDB, tok.OwnerID)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeUnauthorized))
		return nil, nil, false
	}
	return owner, tok, true
}

func (fd *frontendDeps) webPrimaryOwner(w http.ResponseWriter, r *http.Request) (*db.Owner, *db.APIToken, bool) {
	cookie, err := r.Cookie(ownerTokenCookieName)
	if err != nil {
		return nil, nil, false
	}
	tok, err := db.LookupTokenByRaw(r.Context(), fd.ld.sqlDB, cookie.Value)
	if err != nil || tok.Kind != "primary" {
		clearOwnerTokenCookie(w, r, fd.cfg, fd.resolver)
		return nil, nil, false
	}
	owner, err := db.GetOwner(r.Context(), fd.ld.sqlDB, tok.OwnerID)
	if err != nil {
		clearOwnerTokenCookie(w, r, fd.cfg, fd.resolver)
		return nil, nil, false
	}
	setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, cookie.Value)
	return owner, tok, true
}

func (fd *frontendDeps) renderTokenPageWithReveal(w http.ResponseWriter, r *http.Request, owner *db.Owner, primary *db.APIToken, revealID int64, revealed string) {
	w.Header().Set("Cache-Control", "no-store")
	if primary == nil {
		primary, _ = db.GetPrimaryToken(r.Context(), fd.ld.sqlDB, owner.ID)
	}
	items, _ := db.ListOwnerTokens(r.Context(), fd.ld.sqlDB, owner.ID)
	data := tokenPageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "token_page.title"), fd.cfg.Server.SEO.Description), ResourceLabel: "items", OwnerID: owner.ID, Primary: tokenCardFor(primary, "")}
	if primary != nil && revealID == 0 {
		data.Primary.Revealed = revealed
	}
	for i := range items {
		if items[i].Kind != "linked" {
			continue
		}
		card := tokenCardFor(&items[i], "")
		if items[i].ID == revealID {
			card.Revealed = revealed
		}
		data.Linked = append(data.Linked, card)
	}
	if err := renderPage(w, http.StatusOK, "tokens", data); err != nil {
		sendError(w, r, apperr.New(apperr.CodeServerError))
	}
}

func (fd *frontendDeps) renderTokensNotice(w http.ResponseWriter, r *http.Request, key string, status int) {
	owner, primary, ok := fd.webPrimaryOwner(w, r)
	data := tokenPageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "token_page.title"), fd.cfg.Server.SEO.Description), Restore: !ok, ResourceLabel: "items", Notice: t(r, key)}
	if ok {
		data.OwnerID = owner.ID
		data.Primary = tokenCardFor(primary, "")
		items, err := db.ListOwnerTokens(r.Context(), fd.ld.sqlDB, owner.ID)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		for i := range items {
			if items[i].Kind == "linked" {
				data.Linked = append(data.Linked, tokenCardFor(&items[i], ""))
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = renderPage(w, status, "tokens", data)
}

func tokenCardFor(t *db.APIToken, revealed string) tokenCard {
	if t == nil {
		return tokenCard{}
	}
	return tokenCard{ID: t.ID, Name: t.Name, Masked: db.MaskAPIToken(t), Created: t.CreatedAt.Format(time.RFC3339), LastUsed: formatOptionalTime(t.LastUsedAt), Expires: formatOptionalTime(t.ExpiresAt), Status: t.Status(), Revealed: revealed}
}
func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return "Never"
	}
	return t.UTC().Format(time.RFC3339)
}
func tokenRouteID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}
func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(value, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}
func rCookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
func setOwnerTokenCookie(w http.ResponseWriter, r *http.Request, cfg *config.Config, resolver *ProxyResolver, raw string) {
	maxAge := cfg.Server.Tokens.CookieMaxAgeSeconds()
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: ownerTokenCookieName, Value: raw, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
}
func clearOwnerTokenCookie(w http.ResponseWriter, r *http.Request, cfg *config.Config, resolver *ProxyResolver) {
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: ownerTokenCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
}
func parseTokenExpiry(value, date string) (*time.Time, error) {
	switch value {
	case "", "never":
		return nil, nil
	case "7d":
		v := time.Now().UTC().AddDate(0, 0, 7)
		return &v, nil
	case "30d":
		v := time.Now().UTC().AddDate(0, 0, 30)
		return &v, nil
	case "90d":
		v := time.Now().UTC().AddDate(0, 0, 90)
		return &v, nil
	case "1y":
		v := time.Now().UTC().AddDate(1, 0, 0)
		return &v, nil
	case "custom":
		v, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil, err
		}
		end := v.Add(24 * time.Hour)
		return &end, nil
	default:
		return nil, fmt.Errorf("invalid token expiry")
	}
}
func (fd *frontendDeps) tokenRedirect(w http.ResponseWriter, r *http.Request, target string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (fd *frontendDeps) auditToken(r *http.Request, event, owner, prefix string, result applog.Result) {
	if fd.audit == nil {
		return
	}
	ip := ""
	if fd.resolver != nil {
		ip = fd.resolver.ResolveClientIP(r)
	}
	_ = fd.audit.Write(applog.Entry{Event: event, Category: "token", Severity: applog.SeverityInfo, Actor: applog.Actor{IP: ip, UserID: owner}, Target: &applog.Target{Type: "api_token", ID: prefix}, Details: map[string]any{"owner_id": owner, "token_prefix": prefix}, Result: result})
}
