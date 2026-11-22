package config

import "github.com/apimgr/shortner/src/common/i18n"

// I18N holds `server.i18n`, per AI.md PART 30 "Configuration". It selects
// which of the embedded locales the WebUI offers and which language the
// server falls back to when a request expresses no preference.
type I18N struct {
	// DefaultLanguage is used when a request expresses no preference, and
	// is also the config-file source of the CLI's own output language.
	DefaultLanguage string `yaml:"default_language"`
	// AvailableLanguages is the subset of embedded locales the language
	// selector offers. An empty list means every embedded locale.
	AvailableLanguages []string `yaml:"available_languages"`
}

// DefaultI18N returns AI.md PART 30's configuration block verbatim: every
// supported language available, English default and fallback, a `lang`
// cookie that lives for one year.
func DefaultI18N() I18N {
	return I18N{
		DefaultLanguage:    "en",
		AvailableLanguages: i18n.Codes(),
	}
}

// Language returns the configured default language, resolved against the
// embedded locales so an unsupported value silently becomes English.
func (c I18N) Language() string {
	return i18n.Resolve(c.DefaultLanguage)
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
	for _, lang := range c.AvailableLanguages {
		if !i18n.IsSupported(lang) {
			warnings = append(warnings, "server.i18n.available_languages: '"+lang+
				"' is not an embedded locale and will not be offered")
		}
	}
	return warnings
}
