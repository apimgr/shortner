// Package sanitize implements the footer custom-HTML sanitizer, per AI.md
// PART 16 "Footer Customization" -> "Custom HTML Validation": operator-
// supplied `web.footer.custom_html` is rendered through this sanitizer
// before it ever reaches a template, so a compromised/careless config value
// can never execute script or leak via javascript:/style-attribute
// injection.
package sanitize

import (
	"errors"

	"github.com/microcosm-cc/bluemonday"
)

// footerPolicy is the strict, formatting-only bluemonday policy from AI.md
// PART 16's SanitizeFooterHTML reference implementation.
func footerPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements("p", "br", "span", "div")
	p.AllowElements("strong", "b", "em", "i", "u", "s", "small")
	p.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowElements("ul", "ol", "li")

	p.AllowAttrs("href", "title", "target", "rel").OnElements("a")
	p.RequireNoReferrerOnLinks(true)

	p.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	p.AllowURLSchemes("https", "data")

	p.AllowAttrs("class", "id").Globally()

	return p
}

// SanitizeFooterHTML sanitizes custom footer HTML, per AI.md PART 16
// "Custom HTML Validation". "" and " " (the "disable branding" sentinel)
// pass through unchanged; everything else is run through the strict
// formatting-only policy.
func SanitizeFooterHTML(html string) string {
	if html == "" || html == " " {
		return html
	}
	return footerPolicy().Sanitize(html)
}

// ValidateFooterHTML sanitizes html and reports an error if the input was
// non-empty but the sanitizer stripped it down to nothing (i.e. it
// contained only disallowed elements), per AI.md PART 16
// "ValidateFooterHTML". The caller (config load/save) is responsible for
// logging the startup sanitization preview described in that same section.
func ValidateFooterHTML(html string) (string, error) {
	sanitized := SanitizeFooterHTML(html)
	if len(html) > 0 && html != " " && len(sanitized) == 0 {
		return "", errors.New("sanitize: custom footer HTML contained only disallowed elements")
	}
	return sanitized, nil
}
