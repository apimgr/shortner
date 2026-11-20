package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadLocaleAndFlatten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "locale.json")
	if err := os.WriteFile(path, []byte(`{"meta":{"language":"en"},"label":"ready","count":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadLocale(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"meta.language": "en", "label": "ready", "count": "3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("loadLocale() = %#v, want %#v", got, want)
	}
	if _, err := loadLocale(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("loadLocale() accepted a missing file")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLocale(path); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("loadLocale() error = %v, want invalid JSON", err)
	}
}

func TestValidateValidLocale(t *testing.T) {
	reference := map[string]string{
		"meta.language":      "en",
		"meta.direction":     "ltr",
		"meta.name":          "English",
		"meta.native_name":   "English",
		"meta.version":       "1",
		"welcome":            "Hello {name}",
		"plurals.link.one":   "{count} link",
		"plurals.link.other": "{count} links",
	}
	locale := map[string]string{}
	for key, value := range reference {
		locale[key] = value
	}
	if problems := validate("en", reference, locale); len(problems) != 0 {
		t.Fatalf("validate() = %v, want no problems", problems)
	}
}

func TestValidateReportsMetadataKeysTokensAndPluralProblems(t *testing.T) {
	reference := map[string]string{
		"meta.language":      "en",
		"meta.direction":     "ltr",
		"meta.name":          "English",
		"meta.native_name":   "English",
		"meta.version":       "1",
		"welcome":            "Hello {name}",
		"empty":              "content",
		"plurals.link.one":   "{count} link",
		"plurals.link.other": "{count} links",
	}
	locale := map[string]string{
		"meta.language":        "wrong",
		"meta.direction":       "sideways",
		"meta.name":            " ",
		"meta.native_name":     " ",
		"meta.version":         " ",
		"welcome":              "Hi {other}",
		"empty":                "  ",
		"orphan":               "value",
		"plurals.link.one":     " ",
		"plurals.link.unknown": "value",
		"plurals.unknown.one":  "value",
		"plurals.malformed":    "value",
	}
	problems := strings.Join(validate("fr", reference, locale), "\n")
	for _, want := range []string{
		"meta.language is",
		"meta.direction must",
		"meta.name is missing or empty",
		"meta.native_name is missing or empty",
		"meta.version is missing or empty",
		"key welcome drops interpolation token(s) {name}",
		"key welcome adds unknown interpolation token(s) {other}",
		"empty value: empty",
		"orphaned key not in en.json: orphan",
		"missing required plural category: plurals.link.other",
		"empty plural value: plurals.link.one",
		"malformed plural key: plurals.malformed",
		"orphaned plural entry not in en.json: plurals.unknown.one",
		"unknown plural category: plurals.link.unknown",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("validate() omitted %q from:\n%s", want, problems)
		}
	}
	if got := missingTokens("{name} {name} {count}", "{name}"); !reflect.DeepEqual(got, []string{"{count}"}) {
		t.Errorf("missingTokens() = %v, want [{count}]", got)
	}
	if got := pluralEntries(reference); !reflect.DeepEqual(got, []string{"link"}) {
		t.Errorf("pluralEntries() = %v, want [link]", got)
	}
	if problems := validatePlurals("xx", reference, locale); len(problems) != 1 || !strings.Contains(problems[0], "no plural-category rules") {
		t.Errorf("validatePlurals(unknown) = %v", problems)
	}
}
