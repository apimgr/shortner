package i18n

import (
	"os"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// CLILanguage resolves the output language of a command-line invocation
// using AI.md PART 30's priority table: the --lang flag, then the config
// file's `lang` value, then LC_ALL, then LANG, then English. "auto" in the
// config means "keep looking", and any unsupported value silently falls
// back to English rather than erroring.
func CLILanguage(flagLang, configLang string) string {
	if code := validate(flagLang); code != "" {
		return code
	}
	if strings.ToLower(strings.TrimSpace(configLang)) != "auto" {
		if code := validate(configLang); code != "" {
			return code
		}
	}
	for _, env := range []string{"LC_ALL", "LANG"} {
		if code := validate(os.Getenv(env)); code != "" {
			return code
		}
	}
	return DefaultLanguage
}

// validate returns the normalized code when value names a supported
// language, and "" otherwise so the caller can fall through to the next
// source in the priority chain.
func validate(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	code := Normalize(value)
	if IsSupported(code) {
		return code
	}
	return ""
}

// FormatNumber renders n with lang's own grouping and decimal separators,
// per AI.md PART 30 "Date/Time/Number Formatting".
func FormatNumber(n float64, lang string) string {
	return message.NewPrinter(language.Make(Resolve(lang))).Sprintf("%.2f", n)
}

// FormatInt renders an integer with lang's own digit grouping.
func FormatInt(n int64, lang string) string {
	return message.NewPrinter(language.Make(Resolve(lang))).Sprintf("%d", n)
}
