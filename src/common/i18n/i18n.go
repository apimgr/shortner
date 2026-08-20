// Package i18n implements AI.md PART 30 "Internationalization (i18n)": the
// single, shared translation catalog every binary embeds via go:embed.
// Lookups never fail — an unsupported language falls back to English, a
// missing key falls back to the English value, and a key missing from
// English too is returned verbatim so the bug is visible instead of silent.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

// localeFS embeds src/common/i18n/locales/*.json (the path is relative to
// this package), per AI.md PART 30 "Translation File Location & Embedding":
// no external locale files are read at runtime.
//
//go:embed locales/*.json
var localeFS embed.FS

// DefaultLanguage is the fallback used for an unsupported language, a
// missing key, or no detectable preference at all.
const DefaultLanguage = "en"

// Meta describes one locale file's `meta` block.
type Meta struct {
	Language   string `json:"language"`
	Name       string `json:"name"`
	NativeName string `json:"native_name"`
	Direction  string `json:"direction"`
	Version    string `json:"version"`
}

// Language is one selectable language, as rendered by the language
// selector in the page footer.
type Language struct {
	Code       string
	Name       string
	NativeName string
	Direction  string
}

// catalog holds every locale file flattened to dot-separated keys, e.g.
// "common.save" and "plurals.clicks.one".
var (
	catalog = map[string]map[string]string{}
	metas   = map[string]Meta{}
	codes   []string
)

// languageOrder is the display order of the AI.md PART 30 "Supported
// Languages" table. Any locale file not named here still loads; it is
// simply appended alphabetically after the known set.
var languageOrder = []string{"en", "es", "zh", "fr", "ar", "de", "ja"}

func init() {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		panic("i18n: locales directory missing from the embedded filesystem: " + err.Error())
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(e.Name(), ".json")
		raw, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			panic("i18n: cannot read embedded locale " + e.Name() + ": " + err.Error())
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			panic("i18n: locale " + e.Name() + " is not valid JSON: " + err.Error())
		}
		flat := map[string]string{}
		flatten("", doc, flat)
		catalog[code] = flat
		metas[code] = metaFrom(code, flat)
		codes = append(codes, code)
	}
	if _, ok := catalog[DefaultLanguage]; !ok {
		panic("i18n: the default locale " + DefaultLanguage + ".json is missing")
	}
	sortCodes()
}

// flatten walks a decoded locale document, joining nested object keys with
// "." and stringifying every leaf value.
func flatten(prefix string, node map[string]any, out map[string]string) {
	for k, v := range node {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flatten(key, val, out)
		case string:
			out[key] = val
		default:
			out[key] = fmt.Sprint(val)
		}
	}
}

// metaFrom builds the Meta for code from its already-flattened keys,
// filling in safe defaults for anything the file omits.
func metaFrom(code string, flat map[string]string) Meta {
	m := Meta{
		Language:   flat["meta.language"],
		Name:       flat["meta.name"],
		NativeName: flat["meta.native_name"],
		Direction:  flat["meta.direction"],
		Version:    flat["meta.version"],
	}
	if m.Language == "" {
		m.Language = code
	}
	if m.NativeName == "" {
		m.NativeName = m.Name
	}
	if m.NativeName == "" {
		m.NativeName = code
	}
	if m.Direction != "rtl" {
		m.Direction = "ltr"
	}
	return m
}

// sortCodes orders the loaded language codes by the PART 30 table first,
// then alphabetically for anything added later.
func sortCodes() {
	rank := map[string]int{}
	for i, c := range languageOrder {
		rank[c] = i
	}
	sort.Slice(codes, func(i, j int) bool {
		ri, oki := rank[codes[i]]
		rj, okj := rank[codes[j]]
		switch {
		case oki && okj:
			return ri < rj
		case oki:
			return true
		case okj:
			return false
		default:
			return codes[i] < codes[j]
		}
	})
}

// IsSupported reports whether lang has an embedded locale file. Matching is
// case-insensitive and ignores any region subtag ("es-MX" matches "es").
func IsSupported(lang string) bool {
	_, ok := catalog[Normalize(lang)]
	return ok
}

// Normalize lowercases lang and strips any region/script subtag, so
// "es-MX", "es_MX", and "ES" all normalize to "es".
func Normalize(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_."); i > 0 {
		lang = lang[:i]
	}
	return lang
}

// Resolve returns lang if it is supported, otherwise DefaultLanguage. It
// never errors — AI.md PART 30 requires an unsupported language to fall
// back to English silently.
func Resolve(lang string) string {
	lang = Normalize(lang)
	if _, ok := catalog[lang]; ok {
		return lang
	}
	return DefaultLanguage
}

// Codes returns every supported language code, in PART 30 table order.
func Codes() []string {
	out := make([]string, len(codes))
	copy(out, codes)
	return out
}

// Languages returns every supported language with its display metadata,
// for the language selector.
func Languages() []Language {
	out := make([]Language, 0, len(codes))
	for _, c := range codes {
		m := metas[c]
		out = append(out, Language{Code: c, Name: m.Name, NativeName: m.NativeName, Direction: m.Direction})
	}
	return out
}

// LanguagesFor returns the supported subset of the operator's configured
// available_languages list, in PART 30 table order. An empty or fully
// unsupported list yields every embedded language, so a misconfiguration
// can never leave the selector empty.
func LanguagesFor(available []string) []Language {
	if len(available) == 0 {
		return Languages()
	}
	allowed := map[string]bool{}
	for _, a := range available {
		if code := Normalize(a); catalog[code] != nil {
			allowed[code] = true
		}
	}
	if len(allowed) == 0 {
		return Languages()
	}
	out := make([]Language, 0, len(allowed))
	for _, l := range Languages() {
		if allowed[l.Code] {
			out = append(out, l)
		}
	}
	return out
}

// MetaFor returns the meta block of lang (English's when unsupported).
func MetaFor(lang string) Meta {
	return metas[Resolve(lang)]
}

// Direction returns "rtl" for a right-to-left language and "ltr"
// otherwise, read from the locale file's meta.direction.
func Direction(lang string) string {
	return MetaFor(lang).Direction
}

// Translate returns the translated string for key. An unsupported language
// uses English; a key missing from the active language falls back to the
// English value; a key missing from English too is returned verbatim.
func Translate(lang, key string) string {
	if v, ok := catalog[Resolve(lang)][key]; ok && v != "" {
		return v
	}
	if v, ok := catalog[DefaultLanguage][key]; ok && v != "" {
		return v
	}
	return key
}

// Has reports whether key exists in lang (or, failing that, in English).
func Has(lang, key string) bool {
	if _, ok := catalog[Resolve(lang)][key]; ok {
		return true
	}
	_, ok := catalog[DefaultLanguage][key]
	return ok
}

// TranslateFormat replaces named {token} placeholders in the translated
// string with the supplied values. Interpolation is LITERAL string
// replacement — a translation is never used as a fmt format string, so a
// stray '%' can never corrupt output. Tokens with no supplied value stay
// visible as-is (a visible bug beats a silent one); extra args are ignored.
func TranslateFormat(lang, key string, args map[string]string) string {
	s := Translate(lang, key)
	for k, v := range args {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// TranslatePlural selects the CLDR plural form for count in lang and
// returns that nested value with {count} substituted. Languages without a
// matching form fall back to "other", then to English.
//
// A count of zero prefers an explicit "zero" form when the catalog defines
// one, even in languages whose CLDR rules have no zero category (English
// "No clicks" instead of "0 clicks"). This mirrors the locale files AI.md
// PART 30 ships, which carry "zero" entries for such languages; when no
// "zero" entry exists the plain CLDR category is used.
func TranslatePlural(lang, key string, count int) string {
	lang = Resolve(lang)
	form := pluralForm(lang, count)
	if count == 0 && form != "zero" {
		if v, ok := catalog[lang][key+".zero"]; ok && v != "" {
			return strings.ReplaceAll(v, "{count}", fmt.Sprint(count))
		}
	}
	value := ""
	for _, candidate := range []string{form, "other"} {
		if v, ok := catalog[lang][key+"."+candidate]; ok && v != "" {
			value = v
			break
		}
		if v, ok := catalog[DefaultLanguage][key+"."+candidate]; ok && v != "" {
			value = v
			break
		}
	}
	if value == "" {
		return key
	}
	return strings.ReplaceAll(value, "{count}", fmt.Sprint(count))
}

// pluralForm returns the CLDR cardinal category name ("zero", "one",
// "two", "few", "many", "other") for count in lang.
func pluralForm(lang string, count int) string {
	tag := language.Make(lang)
	switch plural.Cardinal.MatchPlural(tag, count, 0, 0, 0, 0) {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	default:
		return "other"
	}
}

// RawLocale returns the embedded JSON bytes for lang, exactly as shipped,
// for the `/locales/{lang}.json` route the WebUI's JavaScript fetches. An
// unsupported language returns the English file rather than an error.
func RawLocale(lang string) ([]byte, error) {
	return localeFS.ReadFile("locales/" + Resolve(lang) + ".json")
}
