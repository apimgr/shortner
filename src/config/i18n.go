package config

import (
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/common/i18n"
)

// I18N holds `server.i18n`, per AI.md PART 30 "Configuration". It selects
// which of the embedded locales the WebUI offers and which language the
// server falls back to when a request expresses no preference.
type I18N struct {
	// Enabled turns language negotiation on. When false the server renders
	// every page in DefaultLanguage and hides the language selector.
	Enabled bool `yaml:"enabled"`
	// DefaultLanguage is used when a request expresses no preference, and
	// is also the config-file source of the CLI's own output language.
	DefaultLanguage string `yaml:"default_language"`
	// AvailableLanguages is the subset of embedded locales the language
	// selector offers. An empty list means every embedded locale.
	AvailableLanguages []string `yaml:"available_languages"`
	// FallbackLanguage is used for keys missing from the active language.
	FallbackLanguage string `yaml:"fallback_language"`
	// CookieName is the cookie that persists an explicit `?lang=` choice.
	CookieName string `yaml:"cookie_name"`
	// CookieMaxAge is the cookie lifetime as a duration string ("365d").
	CookieMaxAge string `yaml:"cookie_max_age"`
}

// DefaultI18N returns AI.md PART 30's configuration block verbatim: every
// supported language available, English default and fallback, a `lang`
// cookie that lives for one year.
func DefaultI18N() I18N {
	return I18N{
		Enabled:            true,
		DefaultLanguage:    "en",
		AvailableLanguages: i18n.Codes(),
		FallbackLanguage:   "en",
		CookieName:         "lang",
		CookieMaxAge:       "365d",
	}
}

// Language returns the configured default language, resolved against the
// embedded locales so an unsupported value silently becomes English.
func (c I18N) Language() string {
	return i18n.Resolve(c.DefaultLanguage)
}

// Cookie returns the effective language cookie name.
func (c I18N) Cookie() string {
	if c.CookieName == "" {
		return i18n.CookieName
	}
	return c.CookieName
}

// MaxAgeSeconds parses CookieMaxAge into seconds, accepting both a plain
// day count with the "d" suffix the spec's example uses and any Go
// duration string. An unparseable or non-positive value falls back to the
// one-year default rather than producing a session cookie by accident.
func (c I18N) MaxAgeSeconds() int {
	v := strings.TrimSpace(c.CookieMaxAge)
	if v == "" {
		return i18n.CookieMaxAge
	}
	if strings.HasSuffix(v, "d") {
		if days, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && days > 0 {
			return days * 24 * 60 * 60
		}
		return i18n.CookieMaxAge
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return int(d.Seconds())
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return secs
	}
	return i18n.CookieMaxAge
}

// validateI18N reports configuration that would silently degrade the user
// experience. Nothing here is fatal — AI.md PART 30 requires an
// unsupported language to fall back to English rather than error.
func validateI18N(c I18N) []string {
	var warnings []string
	if c.DefaultLanguage != "" && !i18n.IsSupported(c.DefaultLanguage) {
		warnings = append(warnings, "server.i18n.default_language: '"+c.DefaultLanguage+
			"' is not an embedded locale, using 'en'")
	}
	if c.FallbackLanguage != "" && !i18n.IsSupported(c.FallbackLanguage) {
		warnings = append(warnings, "server.i18n.fallback_language: '"+c.FallbackLanguage+
			"' is not an embedded locale, using 'en'")
	}
	for _, lang := range c.AvailableLanguages {
		if !i18n.IsSupported(lang) {
			warnings = append(warnings, "server.i18n.available_languages: '"+lang+
				"' is not an embedded locale and will not be offered")
		}
	}
	return warnings
}
