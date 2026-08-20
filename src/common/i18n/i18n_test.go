package i18n

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsSupported(t *testing.T) {
	for _, lang := range []string{"en", "es", "zh", "fr", "ar", "de", "ja", "EN", " fr ", "es-MX"} {
		if !IsSupported(lang) {
			t.Errorf("IsSupported(%q) = false, want true", lang)
		}
	}
	for _, lang := range []string{"", "kl", "not-a-language"} {
		if IsSupported(lang) {
			t.Errorf("IsSupported(%q) = true, want false", lang)
		}
	}
}

func TestTranslate(t *testing.T) {
	if got := Translate("en", "common.close"); got == "" || got == "common.close" {
		t.Fatalf("Translate(en, common.close) = %q, want a real translation", got)
	}
	// Every supported language must carry its own value for a core key.
	for _, lang := range Codes() {
		if got := Translate(lang, "common.close"); got == "" {
			t.Errorf("Translate(%q, common.close) is empty", lang)
		}
	}
}

// TestTranslateFallsBackToEnglish covers both fallback paths AI.md PART 30
// requires: an unsupported language silently uses English, and a key that
// exists nowhere is returned verbatim instead of erroring.
func TestTranslateFallsBackToEnglish(t *testing.T) {
	if got, want := Translate("kl", "common.close"), Translate("en", "common.close"); got != want {
		t.Errorf("Translate(kl, common.close) = %q, want English %q", got, want)
	}
	if got := Translate("es", "no.such.key.anywhere"); got != "no.such.key.anywhere" {
		t.Errorf("Translate(es, missing) = %q, want the key itself", got)
	}
}

func TestTranslateFormat(t *testing.T) {
	got := TranslateFormat("en", "cli.running_pid", map[string]string{
		"project_name": "shortner",
		"pid":          "1234",
	})
	if !strings.Contains(got, "shortner") || !strings.Contains(got, "1234") {
		t.Errorf("TranslateFormat() = %q, want both values substituted", got)
	}
	if strings.Contains(got, "{") {
		t.Errorf("TranslateFormat() = %q, want no leftover placeholders", got)
	}
}

// TestTranslateFormatIsLiteral pins the rule that interpolation is plain
// string replacement: a value containing a percent verb must survive
// untouched, since a translation is never used as a fmt format string.
func TestTranslateFormatIsLiteral(t *testing.T) {
	got := TranslateFormat("en", "cli.not_running", map[string]string{
		"project_name": "100%s %d %%",
	})
	if !strings.Contains(got, "100%s %d %%") {
		t.Errorf("TranslateFormat() = %q, want the %% characters preserved verbatim", got)
	}
	if strings.Contains(got, "MISSING") || strings.Contains(got, "!(") {
		t.Errorf("TranslateFormat() = %q, want no fmt error verbs", got)
	}
}

// TestTranslateFormatKeepsUnsuppliedTokens keeps an unfilled placeholder
// visible rather than blanking it, so the bug is obvious in output.
func TestTranslateFormatKeepsUnsuppliedTokens(t *testing.T) {
	got := TranslateFormat("en", "cli.running_pid", map[string]string{"pid": "7"})
	if !strings.Contains(got, "{project_name}") {
		t.Errorf("TranslateFormat() = %q, want the unsupplied token left visible", got)
	}
}

func TestTranslatePlural(t *testing.T) {
	tests := []struct {
		lang  string
		count int
		want  string
	}{
		{"en", 0, "No clicks"},
		{"en", 1, "1 click"},
		{"en", 5, "5 clicks"},
	}
	for _, tt := range tests {
		if got := TranslatePlural(tt.lang, "plurals.clicks", tt.count); got != tt.want {
			t.Errorf("TranslatePlural(%q, %d) = %q, want %q", tt.lang, tt.count, got, tt.want)
		}
	}

	// Languages with a single cardinal category still resolve, via "other".
	for _, lang := range []string{"zh", "ja"} {
		for _, count := range []int{0, 1, 2, 11} {
			got := TranslatePlural(lang, "plurals.links", count)
			if got == "" || got == "plurals.links" {
				t.Errorf("TranslatePlural(%q, %d) = %q, want a real form", lang, count, got)
			}
		}
	}

	// Arabic exercises all six categories without falling through to the key.
	for _, count := range []int{0, 1, 2, 3, 11, 100} {
		got := TranslatePlural("ar", "plurals.days", count)
		if got == "" || got == "plurals.days" {
			t.Errorf("TranslatePlural(ar, %d) = %q, want a real form", count, got)
		}
	}

	if got := TranslatePlural("en", "plurals.nope", 2); got != "plurals.nope" {
		t.Errorf("TranslatePlural(en, missing) = %q, want the key itself", got)
	}
}

// TestPluralCountSubstitution verifies {count} is replaced in every
// language's plural forms, not only English.
func TestPluralCountSubstitution(t *testing.T) {
	for _, lang := range Codes() {
		got := TranslatePlural(lang, "plurals.results", 7)
		if strings.Contains(got, "{count}") {
			t.Errorf("TranslatePlural(%q, 7) = %q, want {count} substituted", lang, got)
		}
	}
}

func TestDirection(t *testing.T) {
	if got := Direction("ar"); got != "rtl" {
		t.Errorf("Direction(ar) = %q, want rtl", got)
	}
	for _, lang := range []string{"en", "es", "zh", "fr", "de", "ja", "kl"} {
		if got := Direction(lang); got != "ltr" {
			t.Errorf("Direction(%q) = %q, want ltr", lang, got)
		}
	}
}

func TestLangFromRequest(t *testing.T) {
	tests := []struct {
		name  string
		build func() *http.Request
		want  string
	}{
		{
			name:  "query param wins over cookie and header",
			build: func() *http.Request { return httptest.NewRequest(http.MethodGet, "/?lang=fr", nil) },
			want:  "fr",
		},
		{
			name: "cookie beats Accept-Language",
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.AddCookie(&http.Cookie{Name: "lang", Value: "de"})
				r.Header.Set("Accept-Language", "es")
				return r
			},
			want: "de",
		},
		{
			name: "Accept-Language is used when nothing else is set",
			build: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("Accept-Language", "es-MX,es;q=0.9,en;q=0.5")
				return r
			},
			want: "es",
		},
		{
			name:  "no signal falls back to the default",
			build: func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
			want:  "en",
		},
		{
			name:  "unsupported query language falls back to the default",
			build: func() *http.Request { return httptest.NewRequest(http.MethodGet, "/?lang=kl", nil) },
			want:  "en",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LangFromRequest(tt.build(), "lang", "en"); got != tt.want {
				t.Fatalf("LangFromRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetLanguageCookie(t *testing.T) {
	w := httptest.NewRecorder()
	SetLanguageCookie(w, httptest.NewRequest(http.MethodGet, "/", nil), "lang", "fr", 3600)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != "lang" || c.Value != "fr" || c.Path != "/" || c.MaxAge != 3600 {
		t.Errorf("cookie = %+v, want lang=fr path=/ max-age=3600", c)
	}
}

func TestCLILanguage(t *testing.T) {
	tests := []struct {
		name       string
		flagLang   string
		configLang string
		env        map[string]string
		want       string
	}{
		{"flag wins over everything", "fr", "de", map[string]string{"LANG": "ja_JP.UTF-8"}, "fr"},
		{"config used when no flag", "", "de", map[string]string{"LANG": "ja_JP.UTF-8"}, "de"},
		{"config auto falls through to env", "", "auto", map[string]string{"LANG": "ja_JP.UTF-8"}, "ja"},
		{"LC_ALL beats LANG", "", "", map[string]string{"LC_ALL": "es_ES.UTF-8", "LANG": "ja_JP.UTF-8"}, "es"},
		{"nothing set means English", "", "", nil, "en"},
		{"unsupported flag falls back to English", "kl", "", nil, "en"},
		{"C locale is not a language", "", "", map[string]string{"LANG": "C"}, "en"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LC_ALL", "")
			t.Setenv("LANG", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := CLILanguage(tt.flagLang, tt.configLang); got != tt.want {
				t.Errorf("CLILanguage(%q, %q) = %q, want %q", tt.flagLang, tt.configLang, got, tt.want)
			}
		})
	}
}

// TestRawLocale checks the bytes served by the /locales/{lang}.json route:
// they must be the shipped file, and an unsupported language must still
// return valid JSON (the English file) rather than an error.
func TestRawLocale(t *testing.T) {
	for _, lang := range append(Codes(), "kl") {
		raw, err := RawLocale(lang)
		if err != nil {
			t.Fatalf("RawLocale(%q) error: %v", lang, err)
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatalf("RawLocale(%q) is not valid JSON: %v", lang, err)
		}
		if _, ok := tree["meta"]; !ok {
			t.Errorf("RawLocale(%q) has no meta block", lang)
		}
	}
}

// TestLanguagesMetadata verifies every shipped locale advertises the
// metadata the language selector renders.
func TestLanguagesMetadata(t *testing.T) {
	langs := Languages()
	if len(langs) != len(Codes()) {
		t.Fatalf("Languages() returned %d entries, want %d", len(langs), len(Codes()))
	}
	for _, l := range langs {
		if l.Code == "" || l.Name == "" || l.NativeName == "" {
			t.Errorf("language %+v has empty metadata", l)
		}
	}
}
