// Package urlutil builds properly encoded API URLs. Every user-supplied
// value that reaches a URL MUST pass through this package — AI.md PART 32
// "URL Encoding": "ALL user input in URLs MUST be encoded. NEVER construct
// URLs with raw user input."
package urlutil

import (
	"net/url"
	"strings"
)

// BuildAPIURL constructs a properly encoded API URL from a base URL, a path
// template containing {name} placeholders, the path parameters that fill
// them, and optional query parameters. Path parameters are escaped with
// url.PathEscape and query values with url.Values.Encode. Returns "" when
// baseURL cannot be parsed.
func BuildAPIURL(baseURL, path string, pathParams map[string]string, queryParams map[string]string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}

	encodedPath := path
	for key, value := range pathParams {
		placeholder := "{" + key + "}"
		encodedPath = strings.ReplaceAll(encodedPath, placeholder, EncodePathSegment(value))
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + encodedPath
	// Path was assembled from already-escaped segments; clearing RawPath
	// keeps url.String from re-deriving (and double-encoding) it.
	u.RawPath = ""

	if len(queryParams) > 0 {
		q := u.Query()
		for key, value := range queryParams {
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()
	}

	return u.String()
}

// EncodePathSegment encodes a single path segment — slugs, resource IDs,
// filenames. url.PathEscape leaves "+" untouched, which a server may decode
// as a space, so it is escaped explicitly per AI.md PART 32's
// "Characters That MUST Be Encoded" table.
func EncodePathSegment(segment string) string {
	return strings.ReplaceAll(url.PathEscape(segment), "+", "%2B")
}

// EncodeQueryValue encodes a query parameter value — search terms, filter
// values, pagination.
func EncodeQueryValue(value string) string {
	return url.QueryEscape(value)
}

// BuildQueryString builds an encoded query string from a parameter map.
func BuildQueryString(params map[string]string) string {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	return values.Encode()
}
