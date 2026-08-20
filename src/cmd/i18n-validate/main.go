// Command i18n-validate is the build-time translation check required by
// AI.md PART 30 ("Build-Time Validation"). It compares every locale file
// in a directory against en.json and fails the build when a translation
// would silently degrade at runtime.
//
// It reports, as errors:
//   - keys present in en.json but missing from another language
//   - orphaned keys present in another language but not in en.json
//   - empty string values in any language
//   - interpolation tokens ({var}) that do not match en.json for a key
//   - plural categories required by a language's CLDR rules that are absent
//
// Plural entries are the one place key sets are allowed to differ: Arabic
// needs six cardinal categories while Chinese and Japanese need only one,
// so plural subtrees are checked against the language's own requirements
// instead of against en.json's shape.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// defaultLanguage is the reference locale every other file is compared to.
const defaultLanguage = "en"

// pluralPrefix marks the one subtree whose leaf names are language-specific.
const pluralPrefix = "plurals."

// tokenPattern matches a literal {var} interpolation token.
var tokenPattern = regexp.MustCompile(`\{[a-z_][a-z0-9_]*\}`)

// requiredPluralCategories lists the CLDR cardinal categories each
// supported language must provide for every entry under "plurals".
// The table mirrors AI.md PART 30's Supported Languages table.
var requiredPluralCategories = map[string][]string{
	"en": {"one", "other"},
	"es": {"one", "other"},
	"zh": {"other"},
	"fr": {"one", "other"},
	"ar": {"zero", "one", "two", "few", "many", "other"},
	"de": {"one", "other"},
	"ja": {"other"},
}

// main validates every locale file in the directory given as the first
// argument, printing one line per problem and exiting non-zero if any
// problem was found.
func main() {
	dir := "src/common/i18n/locales"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	reference, err := loadLocale(filepath.Join(dir, defaultLanguage+".json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "i18n-validate: %v\n", err)
		os.Exit(1)
	}

	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "i18n-validate: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(entries)

	problems := 0
	checked := 0
	for _, path := range entries {
		lang := strings.TrimSuffix(filepath.Base(path), ".json")
		locale, err := loadLocale(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(path), err)
			problems++
			continue
		}
		checked++
		for _, problem := range validate(lang, reference, locale) {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filepath.Base(path), problem)
			problems++
		}
	}

	if problems > 0 {
		fmt.Fprintf(os.Stderr, "i18n-validate: %d problem(s) across %d file(s)\n", problems, checked)
		os.Exit(1)
	}
	fmt.Printf("i18n-validate: %d locale(s) valid, %d keys each\n", checked, len(reference))
}

// loadLocale reads a locale file and returns its flattened dotted-key map.
func loadLocale(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	flat := map[string]string{}
	flatten("", tree, flat)
	return flat, nil
}

// flatten walks a decoded locale tree and records every leaf under its
// dotted path.
func flatten(prefix string, tree map[string]any, out map[string]string) {
	for key, value := range tree {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		switch typed := value.(type) {
		case map[string]any:
			flatten(path, typed, out)
		case string:
			out[path] = typed
		default:
			out[path] = fmt.Sprint(typed)
		}
	}
}

// validate returns every problem found in locale when compared against
// the reference English catalog.
func validate(lang string, reference, locale map[string]string) []string {
	var problems []string

	if got := locale["meta.language"]; got != lang {
		problems = append(problems, fmt.Sprintf("meta.language is %q but the file is %s.json", got, lang))
	}
	if direction := locale["meta.direction"]; direction != "ltr" && direction != "rtl" {
		problems = append(problems, fmt.Sprintf("meta.direction must be \"ltr\" or \"rtl\", got %q", direction))
	}
	for _, field := range []string{"meta.name", "meta.native_name", "meta.version"} {
		if strings.TrimSpace(locale[field]) == "" {
			problems = append(problems, field+" is missing or empty")
		}
	}

	for _, key := range sortedKeys(reference) {
		if strings.HasPrefix(key, pluralPrefix) {
			continue
		}
		value, ok := locale[key]
		if !ok {
			problems = append(problems, "missing key: "+key)
			continue
		}
		if strings.TrimSpace(value) == "" {
			problems = append(problems, "empty value: "+key)
			continue
		}
		if missing := missingTokens(reference[key], value); len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("key %s drops interpolation token(s) %s", key, strings.Join(missing, " ")))
		}
		if extra := missingTokens(value, reference[key]); len(extra) > 0 {
			problems = append(problems, fmt.Sprintf("key %s adds unknown interpolation token(s) %s", key, strings.Join(extra, " ")))
		}
	}

	for _, key := range sortedKeys(locale) {
		if strings.HasPrefix(key, pluralPrefix) || strings.HasPrefix(key, "meta.") {
			continue
		}
		if _, ok := reference[key]; !ok {
			problems = append(problems, "orphaned key not in "+defaultLanguage+".json: "+key)
		}
	}

	problems = append(problems, validatePlurals(lang, reference, locale)...)
	return problems
}

// validatePlurals checks that every plural entry present in en.json also
// exists in locale with all the CLDR categories that language requires,
// and that no plural entry is empty or names an unknown category.
func validatePlurals(lang string, reference, locale map[string]string) []string {
	var problems []string

	required, known := requiredPluralCategories[lang]
	if !known {
		return []string{"language has no plural-category rules in i18n-validate; add it to requiredPluralCategories"}
	}
	allowed := map[string]bool{"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true}

	for _, entry := range pluralEntries(reference) {
		for _, category := range required {
			key := pluralPrefix + entry + "." + category
			value, ok := locale[key]
			if !ok {
				problems = append(problems, "missing required plural category: "+key)
				continue
			}
			if strings.TrimSpace(value) == "" {
				problems = append(problems, "empty plural value: "+key)
			}
		}
	}

	referenceEntries := map[string]bool{}
	for _, entry := range pluralEntries(reference) {
		referenceEntries[entry] = true
	}
	for _, key := range sortedKeys(locale) {
		if !strings.HasPrefix(key, pluralPrefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(key, pluralPrefix), ".")
		if len(parts) != 2 {
			problems = append(problems, "malformed plural key: "+key)
			continue
		}
		if !referenceEntries[parts[0]] {
			problems = append(problems, "orphaned plural entry not in "+defaultLanguage+".json: "+key)
			continue
		}
		if !allowed[parts[1]] {
			problems = append(problems, "unknown plural category: "+key)
		}
	}

	return problems
}

// pluralEntries returns the sorted names of the plural entries declared in
// a catalog (for example "clicks" from "plurals.clicks.one").
func pluralEntries(catalog map[string]string) []string {
	seen := map[string]bool{}
	for key := range catalog {
		if !strings.HasPrefix(key, pluralPrefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(key, pluralPrefix), ".")
		if len(parts) == 2 {
			seen[parts[0]] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// missingTokens returns the interpolation tokens present in want but
// absent from got.
func missingTokens(want, got string) []string {
	var missing []string
	seen := map[string]bool{}
	for _, token := range tokenPattern.FindAllString(want, -1) {
		if seen[token] || strings.Contains(got, token) {
			continue
		}
		seen[token] = true
		missing = append(missing, token)
	}
	sort.Strings(missing)
	return missing
}

// sortedKeys returns a catalog's keys in a stable order so validation
// output is deterministic.
func sortedKeys(catalog map[string]string) []string {
	keys := make([]string, 0, len(catalog))
	for key := range catalog {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
