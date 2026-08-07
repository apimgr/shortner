package security

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
)

// shortCodeAlphabet is the 62-character alphanumeric alphabet used for
// auto-generated short codes, per IDEA.md "Business rules": "Short codes:
// 6-char alphanumeric, auto-generated (62^6 keyspace)".
const shortCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// ShortCodeLength is the fixed length of an auto-generated short code.
const ShortCodeLength = 6

// GenerateShortCode returns a random 6-character alphanumeric short code
// drawn from the 62-character keyspace. Callers are responsible for
// retrying on a uniqueness collision (see src/db CreateLinkAutoCode).
func GenerateShortCode() (string, error) {
	buf := make([]byte, ShortCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("security: generate short code: %w", err)
	}
	out := make([]byte, ShortCodeLength)
	for i, v := range buf {
		out[i] = shortCodeAlphabet[int(v)%len(shortCodeAlphabet)]
	}
	return string(out), nil
}

// slugPattern matches a valid custom slug: 3-20 chars, alphanumeric plus
// hyphens, per IDEA.md "Business rules": "Custom slugs: 3-20 chars,
// alphanumeric + hyphens".
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9-]{3,20}$`)

// ValidateSlugFormat reports whether slug matches the custom-slug format
// rule (length and character set only — reserved-name checking is
// separate, see IsReservedSlug).
func ValidateSlugFormat(slug string) bool {
	return slugPattern.MatchString(slug)
}

// reservedSlugs is a minimal built-in reserved-name list covering routes
// this codebase already defines or will imminently define (health check,
// static assets, API prefix, well-known). IDEA.md "Business rules" and
// AI.md PART 11 "API Token Model" both point custom-slug validation at
// "the reserved-names list (PART 16 -> Reserved Names)" — that PART does
// not exist yet in this codebase (see TODO.AI.md). This list is a working
// subset, not the final one; IsReservedSlug must be re-pointed at the
// full PART 16 list once it lands, without changing this function's
// signature.
var reservedSlugs = map[string]bool{
	"api": true, "static": true, "health": true, "healthz": true,
	"admin": true, "login": true, "logout": true, "stats": true,
	"about": true, "list": true, "domains": true, "server": true,
	"well-known": true, "assets": true, "favicon.ico": true,
	"robots.txt": true, "sitemap.xml": true, "security.txt": true,
}

// IsReservedSlug reports whether slug (case-insensitive) collides with a
// reserved name and must be rejected as a custom slug. See reservedSlugs
// doc comment for scope/limitations.
func IsReservedSlug(slug string) bool {
	return reservedSlugs[strings.ToLower(slug)]
}
