package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/config"
)

// languageMiddleware implements AI.md PART 30 "Language Selection via Query
// Parameter": it resolves the request language from `?lang=` (which also
// persists the choice as a cookie), then the cookie, then Accept-Language,
// then the operator's default, and puts the result on the context so every
// handler and template renders in one consistent language.
//
// It also sets Content-Language and adds Accept-Language to Vary, so a
// shared cache never serves one visitor's language to another.
func (d *deps) languageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := d.i18n
		lang := cfg.Language()
		if chosen := i18n.QueryLang(r); chosen != "" && allowedLanguage(cfg, chosen) {
			if !readConsent(r).Preferences {
				query := r.URL.Query()
				query.Del("lang")
				query.Set("consent_required", "1")
				location := r.URL.Path
				if encoded := query.Encode(); encoded != "" {
					location += "?" + encoded
				}
				http.Redirect(w, r, location, http.StatusSeeOther)
				return
			}
			i18n.SetLanguageCookie(w, r, i18n.CookieName, chosen, i18n.CookieMaxAge, requestIsHTTPS(preferenceRequest(r, d.resolver)))
			lang = chosen
		} else {
			detected := i18n.FromAcceptLanguage(r.Header.Get("Accept-Language"))
			if readConsent(r).Preferences {
				detected = i18n.LangFromRequest(r, i18n.CookieName, cfg.DefaultLanguage)
			}
			if allowedLanguage(cfg, detected) {
				lang = detected
			}
		}
		w.Header().Set("Content-Language", lang)
		addVary(w, "Accept-Language")
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyLang, lang)))
	})
}

// allowedLanguage reports whether lang is one the operator offers. An empty
// available_languages list means "every embedded locale".
func allowedLanguage(cfg config.I18N, lang string) bool {
	if !i18n.IsSupported(lang) {
		return false
	}
	if len(cfg.AvailableLanguages) == 0 {
		return true
	}
	for _, a := range cfg.AvailableLanguages {
		if i18n.Normalize(a) == i18n.Normalize(lang) {
			return true
		}
	}
	return false
}

// addVary appends value to the response's Vary header without duplicating
// an entry that is already listed.
func addVary(w http.ResponseWriter, value string) {
	for _, existing := range w.Header().Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	w.Header().Add("Vary", value)
}

// langFromContext returns the language languageMiddleware resolved for r,
// falling back to the request's own detection (and ultimately English) for
// handlers reached without the middleware, such as in unit tests.
func langFromContext(r *http.Request) string {
	if r == nil {
		return i18n.DefaultLanguage
	}
	if v, ok := r.Context().Value(ctxKeyLang).(string); ok && v != "" {
		return v
	}
	return i18n.LangFromRequest(r, i18n.CookieName, i18n.DefaultLanguage)
}

// t translates key in the request's language, per AI.md PART 30
// "Template Usage" -> "Request-scoped translation".
func t(r *http.Request, key string) string {
	return i18n.Translate(langFromContext(r), key)
}

// tf translates key in the request's language and substitutes its named
// {token} placeholders literally.
func tf(r *http.Request, key string, args map[string]string) string {
	return i18n.TranslateFormat(langFromContext(r), key, args)
}

// templateFuncs is the FuncMap every page template is parsed with, per
// AI.md PART 30 "Template Usage": {{t .Base.Lang "key"}},
// {{tf .Base.Lang "key" "token" value}}, and {{tp .Base.Lang "key" count}}.
var templateFuncs = map[string]any{
	"nextTheme": func(current string) string {
		switch current {
		case "dark":
			return "light"
		case "light":
			return "auto"
		default:
			return "dark"
		}
	},
	"containerModifier": func(path string) string {
		switch {
		case path == "/list" || path == "/server/healthz" || strings.HasSuffix(path, "/stats"):
			return "container-lg"
		case path == "/" || path == "/server/contact" || path == "/server/ccpa" || path == "/server/preferences" || path == "/server/preferences/tokens" || path == "/server/preferences/sync" || strings.HasPrefix(path, "/server/preferences/"):
			return "container-sm"
		default:
			return "container-md"
		}
	},
	"asset": func(path, stamp string) string {
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		return path + separator + "v=" + url.QueryEscape(stamp)
	},
	"t": func(lang, key string) string {
		return i18n.Translate(lang, key)
	},
	"tf": func(lang, key string, kv ...any) string {
		args := make(map[string]string, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			args[fmt.Sprint(kv[i])] = fmt.Sprint(kv[i+1])
		}
		return i18n.TranslateFormat(lang, key, args)
	},
	"tp": func(lang, key string, count int) string {
		return i18n.TranslatePlural(lang, key, count)
	},
	"healthLabel": func(lang, key string) string {
		translationKey := "health.check." + key
		if i18n.Has(lang, translationKey) {
			return i18n.Translate(lang, translationKey)
		}
		words := strings.ReplaceAll(strings.TrimPrefix(key, "blocklist_"), "_", " ")
		if strings.HasPrefix(key, "blocklist_") {
			return "Blocklist: " + words
		}
		return strings.ReplaceAll(key, "_", " ")
	},
}

// localesHandler serves the embedded locale files at /locales/{lang}.json,
// per AI.md PART 30 "Translation File Location & Embedding" -> "WebUI
// JavaScript". An unsupported language returns the English file rather than
// a 404, so the frontend never has to handle a missing-locale case.
func localesHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/locales/")
	lang := i18n.Resolve(strings.TrimSuffix(name, ".json"))
	body, err := i18n.RawLocale(lang)
	if err != nil {
		http.Error(w, t(r, "errors.server_error"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Language", lang)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// sendError renders translated HTML for browser requests to frontend routes
// and a machine-readable API error envelope for API and non-browser clients.
// The error code remains stable while the human-facing message is localized.
func sendError(w http.ResponseWriter, r *http.Request, err *apperr.AppError) {
	if err == nil {
		err = apperr.New(apperr.CodeServerError)
	}
	lang := langFromContext(r)
	key := err.TranslationKey()
	if i18n.Has(lang, key) {
		err = err.WithMessage(i18n.Translate(lang, key))
	}
	if renderFrontendError(w, r, err) {
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") && detectClientType(r) == clientHTML {
		renderFallbackError(w, r, err.HTTPStatus)
		return
	}
	apperr.SendError(w, err)
}
