// Package config — boolean parsing. See AI.md PART 5 "Boolean Handling":
// the single boolean-parsing entry point required for env vars, config
// file values, CLI flags, API request parameters, form inputs, and query
// string parameters everywhere in this application.
package config

import (
	"fmt"
	"strings"
)

// truthyValues holds the case-insensitive strings that parse to true.
var truthyValues = map[string]bool{
	"1": true, "y": true, "t": true,
	"yes": true, "true": true, "on": true, "ok": true,
	"enable": true, "enabled": true,
	"yep": true, "yup": true, "yeah": true,
	"aye": true, "si": true, "oui": true, "da": true, "hai": true,
	"affirmative": true, "accept": true, "allow": true, "grant": true,
	"sure": true, "totally": true,
}

// falsyValues holds the case-insensitive strings that parse to false.
var falsyValues = map[string]bool{
	"0": true, "n": true, "f": true,
	"no": true, "false": true, "off": true,
	"disable": true, "disabled": true,
	"nope": true, "nah": true, "nay": true,
	"nein": true, "non": true, "niet": true, "iie": true, "lie": true,
	"negative": true, "reject": true, "block": true, "revoke": true,
	"deny": true, "never": true, "noway": true,
}

// ParseBool parses a string into a boolean using the truthy/falsy value
// tables. Returns the parsed value and nil on success. Returns false and
// an error for invalid values. Empty string returns defaultVal.
func ParseBool(s string, defaultVal bool) (bool, error) {
	s = strings.TrimSpace(strings.ToLower(s))

	if s == "" {
		return defaultVal, nil
	}
	if truthyValues[s] {
		return true, nil
	}
	if falsyValues[s] {
		return false, nil
	}

	return false, fmt.Errorf("invalid boolean value: %q", s)
}

// MustParseBool parses a string into a boolean, panicking on an invalid
// value. Use only during initialization, where an invalid config value
// should halt startup.
func MustParseBool(s string, defaultVal bool) bool {
	val, err := ParseBool(s, defaultVal)
	if err != nil {
		panic(err)
	}
	return val
}

// IsTruthy reports whether s is a truthy value. Returns false for empty,
// invalid, or falsy values — never an error.
func IsTruthy(s string) bool {
	return truthyValues[strings.TrimSpace(strings.ToLower(s))]
}

// IsFalsy reports whether s is a falsy value. Returns false for empty,
// invalid, or truthy values — never an error.
func IsFalsy(s string) bool {
	return falsyValues[strings.TrimSpace(strings.ToLower(s))]
}
