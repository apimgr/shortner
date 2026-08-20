package httpserver

import (
	"context"
	"fmt"
	"net/http"
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
		if cfg.Enabled {
			if chosen := i18n.QueryLang(r); chosen != "" && allowedLanguage(cfg, chosen) {
				i18n.SetLanguageCookie(w, r, cfg.Cookie(), chosen, cfg.MaxAgeSeconds())
				lang = chosen
			} else {
				detected := i18n.LangFromRequest(r, cfg.Cookie(), cfg.DefaultLanguage)
				if allowedLanguage(cfg, detected) {
					lang = detected
				}
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// sendError writes an API error envelope whose message is translated into
// the request's language, per AI.md PART 30 "API Response Translation".
// The machine-readable `error` code never changes — only the human-facing
// `message` does.
func sendError(w http.ResponseWriter, r *http.Request, err *apperr.AppError) {
	lang := langFromContext(r)
	key := err.TranslationKey()
	if i18n.Has(lang, key) {
		err = err.WithMessage(i18n.Translate(lang, key))
	}
	apperr.SendError(w, err)
}
