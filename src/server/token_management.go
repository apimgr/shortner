package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/security"
)

const (
	ownerTokenCookieName       = "shortner_owner_token_zgnygola"
	legacyOwnerTokenCookieName = "shortner_owner_token_Lu2YwQRQ"
	tokenViewCookieName        = "shortner_token_view"
)

type tokenCard struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Scope           string  `json:"scope"`
	TokenMasked     string  `json:"token_masked"`
	CreatedAt       string  `json:"created_at"`
	LastUsedAt      *string `json:"last_used_at"`
	ExpiresAt       *string `json:"expires_at"`
	Status          string  `json:"status"`
	OwnerToken      string  `json:"owner_token,omitempty"`
	LinkedToken     string  `json:"linked_token,omitempty"`
	Masked          string  `json:"-"`
	Created         string  `json:"-"`
	LastUsed        string  `json:"-"`
	Expires         string  `json:"-"`
	Revealed        string  `json:"-"`
	Last4           string  `json:"-"`
	Last4Available  bool    `json:"-"`
	RevealAvailable bool    `json:"-"`
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
	ResourceCount int
}

func (fd *frontendDeps) tokenPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	owner, primary, ok := fd.webPrimaryOwner(w, r)
	data := tokenPageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "token_page.title"), fd.cfg.Server.Branding.Description), Restore: !ok, ResourceLabel: "items"}
	view, cookieErr := r.Cookie(tokenViewCookieName)
	viewCard := ""
	if cookieErr == nil {
		viewCard = verifyTokenViewCookie(fd.secretKeys, view.Value)
		clearTokenViewCookie(w)
	}
	if ok {
		data.OwnerID = owner.ID
		data.Primary = tokenCardFor(primary, "", fd.ld.tokenKey)
		if err := fd.ld.sqlDB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM links WHERE owner_id=?`, owner.ID).Scan(&data.ResourceCount); err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		items, err := db.ListOwnerTokens(r.Context(), fd.ld.sqlDB, owner.ID)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		for _, item := range items {
			if item.Kind == "linked" {
				data.Linked = append(data.Linked, tokenCardFor(&item, "", fd.ld.tokenKey))
			}
		}
		if viewCard != "" {
			if viewCard == "primary" {
				if raw, err := db.RevealToken(primary, fd.ld.tokenKey); err == nil {
					data.Primary.Revealed = raw
				}
			} else if id := viewCard; id != "" {
				if token, err := db.GetTokenByPublicID(r.Context(), fd.ld.sqlDB, id); err == nil && token.OwnerID == owner.ID && token.Kind == "linked" {
					if raw, err := db.RevealToken(token, fd.ld.tokenKey); err == nil {
						for i := range data.Linked {
							if data.Linked[i].ID == id {
								data.Linked[i].Revealed = raw
								break
							}
						}
					}
				}
			}
		}
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
	tok, err := db.LookupTokenByRaw(r.Context(), fd.ld.sqlDB, raw, fd.ld.tokenKey)
	if err != nil || tok.Kind != "primary" {
		fd.auditToken(r, "token.restored", "", "", applog.ResultFailure)
		fd.renderTokensNotice(w, r, "token_page.restore_failed", http.StatusOK)
		return
	}
	setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, raw)
	fd.auditToken(r, "token.restored", tok.OwnerID, tok.TokenPrefix, applog.ResultSuccess)
	fd.tokenRedirect(w, r, "/server/preferences/tokens#primary")
}

func (fd *frontendDeps) tokenIssueHandler(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := fd.webPrimaryOwner(w, r); ok {
		sendError(w, r, apperr.New(apperr.CodeConflict))
		return
	}
	ownerID, err := db.CreateOwner(r.Context(), fd.ld.sqlDB)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	raw, token, err := db.CreatePrimaryToken(r.Context(), fd.ld.sqlDB, ownerID, fd.ld.tokenKey)
	if err != nil {
		_, _ = fd.ld.sqlDB.ExecContext(r.Context(), `DELETE FROM owners WHERE id=?`, ownerID)
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, raw)
	setTokenViewCookie(w, fd.secretKeys, "primary")
	fd.auditToken(r, "token.created", ownerID, token.TokenPrefix, applog.ResultSuccess, "issue")
	fd.tokenRedirect(w, r, "/server/preferences/tokens#primary")
}

func (fd *frontendDeps) tokenForgetHandler(w http.ResponseWriter, r *http.Request) {
	clearOwnerTokenCookie(w, r, fd.cfg, fd.resolver)
	fd.tokenRedirect(w, r, "/server/preferences/tokens")
}

func (fd *frontendDeps) tokenPrimaryRevokeHandler(w http.ResponseWriter, r *http.Request) {
	owner, primary, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	confirmation := r.FormValue("confirm")
	if len(confirmation) != 4 || len(primary.TokenLast4) != 4 || subtle.ConstantTimeCompare([]byte(confirmation), []byte(primary.TokenLast4)) != 1 {
		fd.tokenRedirect(w, r, "/server/preferences/tokens#revoke-primary")
		return
	}
	if _, _, err := db.DeleteOwner(r.Context(), fd.ld.sqlDB, owner.ID); err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	fd.auditToken(r, "token.revoked", owner.ID, primary.TokenPrefix, applog.ResultSuccess)
	clearOwnerTokenCookie(w, r, fd.cfg, fd.resolver)
	fd.tokenRedirect(w, r, "/server/preferences/tokens")
}

func (fd *frontendDeps) tokenPrimaryRevealHandler(w http.ResponseWriter, r *http.Request) {
	owner, tok, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	_, err := db.RevealToken(tok, fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	fd.auditToken(r, "token.revealed", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	setTokenViewCookie(w, fd.secretKeys, "primary")
	fd.tokenRedirect(w, r, "/server/preferences/tokens#primary")
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
	if err := fd.refreshProjectSecretKeys(r); err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	setOwnerTokenCookie(w, r, fd.cfg, fd.resolver, raw)
	fd.auditToken(r, "token.rotated", owner.ID, next.TokenPrefix, applog.ResultSuccess)
	setTokenViewCookie(w, fd.secretKeys, "primary")
	fd.tokenRedirect(w, r, "/server/preferences/tokens#primary")
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
	scope := r.FormValue("scope")
	if scope == "" {
		scope = "read-write"
	}
	if scope != "read" && scope != "read-write" {
		fd.renderTokensNotice(w, r, "token_page.create_failed", http.StatusOK)
		return
	}
	expiry, err := parseTokenExpiry(r.FormValue("expiry"), r.FormValue("expires_on"))
	if err != nil {
		fd.renderTokensNotice(w, r, "token_page.invalid_expiry", http.StatusOK)
		return
	}
	maximum := fd.cfg.Server.Tokens.MaxLinked
	if maximum <= 0 {
		maximum = 25
	}
	_, tok, err := db.CreateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, name, expiry, maximum, fd.ld.tokenKey, scope)
	if err != nil {
		fd.renderTokensNotice(w, r, "token_page.create_failed", http.StatusOK)
		return
	}
	fd.auditToken(r, "token.created", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	setTokenViewCookie(w, fd.secretKeys, tok.PublicID)
	fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+tok.PublicID)
}

func (fd *frontendDeps) tokenLinkedRevealHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	id := tokenRouteID(r)
	tok, err := db.GetTokenByPublicID(r.Context(), fd.ld.sqlDB, id)
	if err != nil || tok.OwnerID != owner.ID || tok.Kind != "linked" {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	_, err = db.RevealToken(tok, fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	fd.auditToken(r, "token.revealed", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	setTokenViewCookie(w, fd.secretKeys, id)
	fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+id)
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
	existing, err := db.GetTokenByPublicID(r.Context(), fd.ld.sqlDB, id)
	if err != nil || existing.OwnerID != owner.ID || existing.Kind != "linked" {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	_, tok, err := db.RotateToken(r.Context(), fd.ld.sqlDB, existing.ID, owner.ID, "linked", fd.ld.tokenKey)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	if err := fd.refreshProjectSecretKeys(r); err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	fd.auditToken(r, "token.rotated", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	setTokenViewCookie(w, fd.secretKeys, id)
	fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+id)
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
	tok, err := db.GetTokenByPublicID(r.Context(), fd.ld.sqlDB, id)
	if err != nil || tok.OwnerID != owner.ID || tok.Kind != "linked" {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	if err := db.UpdateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, tok.ID, r.FormValue("name"), expiry, r.FormValue("expiry") == "never"); err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+id)
}

func (fd *frontendDeps) tokenLinkedRevokeHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.webPrimaryOwner(w, r)
	if !ok {
		fd.tokenRedirect(w, r, "/server/preferences/tokens")
		return
	}
	if r.FormValue("confirm") != "revoke" {
		fd.tokenRedirect(w, r, "/server/preferences/tokens#token-"+tokenRouteID(r))
		return
	}
	id := tokenRouteID(r)
	tok, err := db.GetTokenByPublicID(r.Context(), fd.ld.sqlDB, id)
	if err != nil || tok.OwnerID != owner.ID || tok.Kind != "linked" {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	if err := db.RevokeLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, tok.ID); err != nil {
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
	var primary *tokenCard
	linked := make([]tokenCard, 0, len(tokens))
	for i := range tokens {
		card := tokenCardFor(&tokens[i], "", fd.ld.tokenKey)
		if tokens[i].Kind == "primary" {
			primary = &card
		} else {
			linked = append(linked, card)
		}
	}
	maximum := fd.cfg.Server.Tokens.MaxLinked
	if maximum <= 0 {
		maximum = 25
	}
	w.Header().Set("Cache-Control", "no-store")
	apperr.SendOK(w, map[string]any{"primary": primary, "linked": linked, "max_linked": maximum})
}

func (fd *frontendDeps) tokenAPICreateHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.apiPrimaryOwner(w, r)
	if !ok {
		return
	}
	var input struct {
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		Expires   string `json:"expires"`
		ExpiresOn string `json:"expires_on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		sendError(w, r, apperr.New(apperr.CodeBadRequest))
		return
	}
	if input.Scope == "" {
		input.Scope = "read-write"
	}
	if input.Scope != "read" && input.Scope != "read-write" {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	if input.Expires == "custom" || (input.Expires != "" && input.ExpiresOn != "") {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	expiry, err := parseTokenExpiry(input.Expires, input.ExpiresOn)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	maximum := fd.cfg.Server.Tokens.MaxLinked
	if maximum <= 0 {
		maximum = 25
	}
	raw, tok, err := db.CreateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, input.Name, expiry, maximum, fd.ld.tokenKey, input.Scope)
	if err != nil {
		code := apperr.CodeValidationFailed
		if strings.Contains(err.Error(), "maximum") || strings.Contains(err.Error(), "already exists") {
			code = apperr.CodeConflict
		}
		sendError(w, r, apperr.New(code))
		return
	}
	fd.auditToken(r, "token.created", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
	w.Header().Set("Cache-Control", "no-store")
	card := tokenCardFor(tok, "", fd.ld.tokenKey)
	card.LinkedToken = raw
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	apperr.WriteJSON(w, apperr.APIResponse{OK: true, Data: card})
}

func (fd *frontendDeps) tokenAPIIssueHandler(w http.ResponseWriter, r *http.Request) {
	if raw := bearerToken(r); raw != "" {
		if _, err := db.LookupTokenByRaw(r.Context(), fd.ld.sqlDB, raw, fd.ld.tokenKey); err == nil {
			sendError(w, r, apperr.New(apperr.CodeConflict))
			return
		}
	}
	ownerID, err := db.CreateOwner(r.Context(), fd.ld.sqlDB)
	if err != nil {
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	raw, token, err := db.CreatePrimaryToken(r.Context(), fd.ld.sqlDB, ownerID, fd.ld.tokenKey)
	if err != nil {
		_, _ = fd.ld.sqlDB.ExecContext(r.Context(), `DELETE FROM owners WHERE id=?`, ownerID)
		sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
		return
	}
	fd.auditToken(r, "token.created", ownerID, token.TokenPrefix, applog.ResultSuccess, "issue")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	apperr.WriteJSON(w, apperr.APIResponse{OK: true, Data: map[string]any{"owner_token": raw, "token": tokenCardFor(token, "", fd.ld.tokenKey)}})
}

func (fd *frontendDeps) tokenAPIActionHandler(w http.ResponseWriter, r *http.Request) {
	owner, _, ok := fd.apiPrimaryOwner(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/primary") {
		tok, err := db.GetPrimaryToken(r.Context(), fd.ld.sqlDB, owner.ID)
		if err != nil {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
		var input struct {
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || len(input.Confirm) != 4 || len(tok.TokenLast4) != 4 || subtle.ConstantTimeCompare([]byte(input.Confirm), []byte(tok.TokenLast4)) != 1 {
			sendError(w, r, apperr.New(apperr.CodeValidationFailed))
			return
		}
		links, tokens, err := db.DeleteOwner(r.Context(), fd.ld.sqlDB, owner.ID)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		fd.auditToken(r, "token.revoked", owner.ID, tok.TokenPrefix, applog.ResultSuccess)
		apperr.SendOK(w, map[string]any{"tokens": tokens, "resources": links})
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
	} else {
		tok, err := db.GetTokenByPublicID(r.Context(), fd.ld.sqlDB, id)
		if err != nil || tok.OwnerID != owner.ID {
			sendError(w, r, apperr.New(apperr.CodeNotFound))
			return
		}
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
		card := tokenCardFor(tok, "", fd.ld.tokenKey)
		if primaryRoute {
			card.OwnerToken = raw
		} else {
			card.LinkedToken = raw
		}
		apperr.SendOK(w, card)
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
		if err := fd.refreshProjectSecretKeys(r); err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		fd.auditToken(r, "token.rotated", owner.ID, next.TokenPrefix, applog.ResultSuccess)
		w.Header().Set("Cache-Control", "no-store")
		card := tokenCardFor(next, "", fd.ld.tokenKey)
		if primaryRoute {
			card.OwnerToken = raw
		} else {
			card.LinkedToken = raw
		}
		apperr.SendOK(w, card)
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
		apperr.SendOK(w, map[string]string{"id": tok.PublicID, "status": "revoked"})
		return
	}
	var input struct {
		Name      string `json:"name"`
		Expires   string `json:"expires"`
		ExpiresOn string `json:"expires_on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
		sendError(w, r, apperr.New(apperr.CodeBadRequest))
		return
	}
	if input.Expires == "custom" || (input.Expires != "" && input.ExpiresOn != "") {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	expiry, err := parseTokenExpiry(input.Expires, input.ExpiresOn)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	current, err := db.GetTokenByID(r.Context(), fd.ld.sqlDB, id)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	name := input.Name
	if name == "" {
		name = current.Name
	}
	if len(name) < 1 || len(name) > 64 {
		sendError(w, r, apperr.New(apperr.CodeValidationFailed))
		return
	}
	clearExpiry := input.Expires == "never"
	if input.Expires == "" && input.ExpiresOn == "" {
		expiry = current.ExpiresAt
	}
	if err := db.UpdateLinkedToken(r.Context(), fd.ld.sqlDB, owner.ID, id, name, expiry, clearExpiry); err != nil {
		code := apperr.CodeNotFound
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			code = apperr.CodeConflict
		}
		sendError(w, r, apperr.New(code))
		return
	}
	updated, err := db.GetTokenByID(r.Context(), fd.ld.sqlDB, id)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	apperr.SendOK(w, tokenCardFor(updated, "", fd.ld.tokenKey))
}

func (fd *frontendDeps) apiPrimaryOwner(w http.ResponseWriter, r *http.Request) (*db.Owner, *db.APIToken, bool) {
	raw := bearerToken(r)
	if raw == "" {
		sendError(w, r, apperr.New(apperr.CodeUnauthorized))
		return nil, nil, false
	}
	tok, err := db.LookupTokenByRaw(r.Context(), fd.ld.sqlDB, raw, fd.ld.tokenKey)
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
	cookie, ok := ownerTokenCookie(r)
	if !ok {
		return nil, nil, false
	}
	tok, err := db.LookupTokenByRaw(r.Context(), fd.ld.sqlDB, cookie.Value, fd.ld.tokenKey)
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

func (fd *frontendDeps) renderTokensNotice(w http.ResponseWriter, r *http.Request, key string, status int) {
	owner, primary, ok := fd.webPrimaryOwner(w, r)
	data := tokenPageData{Base: fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "token_page.title"), fd.cfg.Server.Branding.Description), Restore: !ok, ResourceLabel: "items", Notice: t(r, key)}
	if ok {
		data.OwnerID = owner.ID
		data.Primary = tokenCardFor(primary, "", fd.ld.tokenKey)
		_ = fd.ld.sqlDB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM links WHERE owner_id=?`, owner.ID).Scan(&data.ResourceCount)
		items, err := db.ListOwnerTokens(r.Context(), fd.ld.sqlDB, owner.ID)
		if err != nil {
			sendError(w, r, apperr.Wrap(apperr.CodeServerError, err))
			return
		}
		for i := range items {
			if items[i].Kind == "linked" {
				data.Linked = append(data.Linked, tokenCardFor(&items[i], "", fd.ld.tokenKey))
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = renderPage(w, status, "tokens", data)
}

func tokenCardFor(t *db.APIToken, revealed string, encryptionKey []byte) tokenCard {
	if t == nil {
		return tokenCard{}
	}
	revealAvailable := false
	if t.TokenEnc != "" {
		_, err := db.RevealToken(t, encryptionKey)
		revealAvailable = err == nil
	}
	kind, scope := t.Kind, t.Scope
	if kind == "" {
		kind = "linked"
	}
	if scope == "" {
		scope = "read-write"
	}
	var lastUsed, expires *string
	if t.LastUsedAt != nil {
		value := t.LastUsedAt.UTC().Format(time.RFC3339)
		lastUsed = &value
	}
	if t.ExpiresAt != nil {
		value := t.ExpiresAt.UTC().Format(time.RFC3339)
		expires = &value
	}
	masked, created := db.MaskAPIToken(t), t.CreatedAt.UTC().Format(time.RFC3339)
	return tokenCard{ID: t.PublicID, Name: t.Name, Kind: kind, Scope: scope, TokenMasked: masked, CreatedAt: created, LastUsedAt: lastUsed, ExpiresAt: expires, Masked: masked, Created: created, LastUsed: formatOptionalTime(t.LastUsedAt), Expires: formatOptionalTime(t.ExpiresAt), Status: t.Status(), Revealed: revealed, Last4: t.TokenLast4, Last4Available: t.TokenLast4 != "", RevealAvailable: revealAvailable}
}
func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return "Never"
	}
	return t.UTC().Format(time.RFC3339)
}
func tokenRouteID(r *http.Request) string {
	return chi.URLParam(r, "id")
}
func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(value, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

// ownerTokenCookie gives the current cookie strict precedence. The legacy
// cookie is read only when the current cookie is absent, so an invalid new
// value can never fall back to a different credential.
func ownerTokenCookie(r *http.Request) (*http.Cookie, bool) {
	if cookie, err := r.Cookie(ownerTokenCookieName); err == nil {
		return cookie, true
	}
	cookie, err := r.Cookie(legacyOwnerTokenCookieName)
	return cookie, err == nil
}

func ownerTokenCookiePresent(r *http.Request) bool {
	_, ok := ownerTokenCookie(r)
	return ok
}

func setOwnerTokenCookie(w http.ResponseWriter, r *http.Request, cfg *config.Config, resolver *ProxyResolver, raw string) {
	maxAge := cfg.Server.Tokens.CookieMaxAgeSeconds()
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: ownerTokenCookieName, Value: raw, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
	clearLegacyOwnerTokenCookie(w)
}
func clearOwnerTokenCookie(w http.ResponseWriter, r *http.Request, cfg *config.Config, resolver *ProxyResolver) {
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: ownerTokenCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
	clearLegacyOwnerTokenCookie(w)
}

func clearLegacyOwnerTokenCookie(w http.ResponseWriter) {
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: legacyOwnerTokenCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
}

func setTokenViewCookie(w http.ResponseWriter, keys *security.SecretKeyring, card string) {
	current, _ := keys.CookieKeys()
	if len(current) == 0 {
		return
	}
	mac := hmac.New(sha256.New, current)
	_, _ = mac.Write([]byte(card))
	value := card + "." + hex.EncodeToString(mac.Sum(nil))
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: tokenViewCookieName, Value: value, Path: "/server/preferences/tokens", MaxAge: 60, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
}

func verifyTokenViewCookie(keys *security.SecretKeyring, value string) string {
	index := strings.LastIndexByte(value, '.')
	if index <= 0 {
		return ""
	}
	provided, err := hex.DecodeString(value[index+1:])
	if err != nil || len(provided) != sha256.Size {
		return ""
	}
	card := value[:index]
	current, previous := keys.CookieKeys()
	for _, key := range [][]byte{current, previous} {
		if len(key) == 0 {
			continue
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(card))
		if subtle.ConstantTimeCompare(provided, mac.Sum(nil)) == 1 {
			return card
		}
	}
	return ""
}

func clearTokenViewCookie(w http.ResponseWriter) {
	w.Header().Add("Set-Cookie", (&http.Cookie{Name: tokenViewCookieName, Value: "", Path: "/server/preferences/tokens", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String())
}

func (fd *frontendDeps) refreshProjectSecretKeys(r *http.Request) error {
	if _, _, _, err := db.EnsureCoreSecrets(r.Context(), fd.ld.sqlDB); err != nil {
		return err
	}
	read := func(name string) ([]byte, []byte, error) {
		encoded, err := db.GetSecret(r.Context(), fd.ld.sqlDB, name)
		if err != nil {
			return nil, nil, err
		}
		current, err := base64.RawStdEncoding.DecodeString(encoded)
		if err != nil || len(current) != 32 {
			return nil, nil, fmt.Errorf("invalid project secret")
		}
		previousEncoded, err := db.GetPreviousSecret(r.Context(), fd.ld.sqlDB, name, nil)
		if err != nil && !errors.Is(err, db.ErrNotFound) {
			return nil, nil, err
		}
		var previous []byte
		if err == nil {
			previous, err = base64.RawStdEncoding.DecodeString(previousEncoded)
			if err != nil || len(previous) != 32 {
				return nil, nil, fmt.Errorf("invalid previous project secret")
			}
		}
		return current, previous, nil
	}
	return fd.secretKeys.Refresh(func() ([]byte, []byte, []byte, []byte, error) {
		cookieCurrent, cookiePrevious, err := read(db.SecretCookieSigning)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		csrfCurrent, csrfPrevious, err := read(db.SecretCSRFToken)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		return cookieCurrent, cookiePrevious, csrfCurrent, csrfPrevious, nil
	})
}
func parseTokenExpiry(value, date string) (*time.Time, error) {
	if value != "" && value != "custom" && date != "" {
		return nil, fmt.Errorf("expiry and expires_on cannot both be set")
	}
	if date != "" {
		v, err := time.Parse("2006-01-02", date)
		if err != nil {
			return nil, err
		}
		today := time.Now().UTC().Truncate(24 * time.Hour)
		if v.Before(today) {
			return nil, fmt.Errorf("expiry date is in the past")
		}
		end := v.Add(24 * time.Hour)
		return &end, nil
	}
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
		return nil, fmt.Errorf("custom expiry requires expires_on")
	default:
		return nil, fmt.Errorf("invalid token expiry")
	}
}
func (fd *frontendDeps) tokenRedirect(w http.ResponseWriter, r *http.Request, target string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (fd *frontendDeps) auditToken(r *http.Request, event, owner, prefix string, result applog.Result, source ...string) {
	if fd.audit == nil {
		return
	}
	ip := ""
	if fd.resolver != nil {
		ip = fd.resolver.ResolveClientIP(r)
	}
	details := map[string]any{"owner_id": owner, "token_prefix": prefix}
	if len(source) > 0 && source[0] != "" {
		details["source"] = source[0]
	}
	_ = fd.audit.Write(applog.Entry{Event: event, Category: "token", Severity: applog.SeverityInfo, Actor: applog.Actor{Type: "owner", IP: ip, UserID: owner}, Target: &applog.Target{Type: "api_token", ID: prefix}, Details: details, Result: result})
}
