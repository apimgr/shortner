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

// reservedSlugs is the full reserved-name list from AI.md PART 16
// "Reserved Names (MUST block from registration)", plus this project's own
// routes referenced elsewhere in the spec (IDEA.md "Business rules",
// frontend-rules.md "Nested sub-resource pattern"): "health", "admin",
// "login", "logout", "list" (nav item), and "domains" (nav item, even
// though this project has no per-user custom domains — see
// src/httpserver/frontend.go's nav-item decision comment).
var reservedSlugs = map[string]bool{
	// System routes
	"api": true, "server": true, "static": true, "assets": true,
	"healthz": true, "metrics": true, "webhook": true, "webhooks": true,

	// Common paths
	"search": true, "explore": true, "discover": true, "trending": true,
	"help": true, "support": true, "docs": true, "documentation": true,
	"about": true, "contact": true, "terms": true, "privacy": true,
	"legal": true, "security": true,

	// Technical
	"graphql": true, "swagger": true, "rest": true, "rpc": true,
	"ws": true, "websocket": true,
	"cdn": true, "media": true, "uploads": true, "files": true, "images": true,
	".well-known": true, "robots.txt": true, "sitemap.xml": true, "favicon.ico": true,

	// Project-specific: this project's own routes/nav items
	"health": true, "admin": true, "login": true, "logout": true,
	"list": true, "domains": true, "stats": true, "well-known": true,
	"security.txt": true,
}

// IsReservedSlug reports whether slug (case-insensitive) collides with a
// reserved name and must be rejected as a custom slug. See reservedSlugs
// doc comment for scope/limitations.
func IsReservedSlug(slug string) bool {
	return reservedSlugs[strings.ToLower(slug)]
}
