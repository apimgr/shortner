// Package notify implements AI.md PART 17 (Email & Notifications): the
// customizable email template system, SMTP auto-detection and connection
// testing, and the operator notification events themselves.
//
// The hard rule the whole package is built around is AI.md PART 17 "SMTP
// Requirement": ALL emails require a valid and working SMTP server. No
// SMTP = no emails. Nothing here queues, retries later, or logs a
// "would have sent" line — when SMTP is unavailable the send path is
// simply not entered.
package notify

import (
	"fmt"
	"sort"
	"strings"
)

// Template is one parsed email template, per AI.md PART 17 "Template
// Format": a `Subject:` first line, a `---` separator, and a plain-text
// body. Both halves may contain `{variable}` placeholders.
type Template struct {
	// Name is the template id (e.g. "backup_failed"), without extension.
	Name string
	// Subject is the text after the leading `Subject:` line, unrendered.
	Subject string
	// Body is everything after the `---` separator, unrendered.
	Body string
}

// separator is the three-dash line that divides subject from body.
const separator = "---"

// ParseTemplate reads the AI.md PART 17 "Template Format" wire format.
// Parsing is intentionally strict — a template that does not start with
// `Subject:` or that omits the separator is a config error the operator
// must see, not something to guess around.
func ParseTemplate(name, raw string) (Template, error) {
	// A UTF-8 BOM from a GUI editor would otherwise hide the Subject: line.
	raw = strings.TrimPrefix(raw, "\ufeff")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")

	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "Subject:") {
		return Template{}, fmt.Errorf("notify: template %s: first line must be \"Subject: ...\"", name)
	}
	subject := strings.TrimSpace(strings.TrimPrefix(lines[0], "Subject:"))

	sepIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == separator {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		return Template{}, fmt.Errorf("notify: template %s: missing %q separator line", name, separator)
	}

	body := strings.Join(lines[sepIdx+1:], "\n")
	return Template{Name: name, Subject: subject, Body: strings.TrimLeft(body, "\n")}, nil
}

// Render substitutes every `{variable}` in the subject and body. A
// placeholder with no matching value renders as empty rather than leaking
// the raw `{token}` into an operator's inbox — a missing optional value
// (no onion address, no Reply-To) is the common case, not an error.
func (t Template) Render(vars map[string]string) (subject, body string) {
	return substitute(t.Subject, vars), substitute(t.Body, vars)
}

// substitute replaces `{name}` occurrences in s. Text that is not a
// well-formed placeholder (an unmatched brace, a brace around a space) is
// left exactly as written.
func substitute(s string, vars map[string]string) string {
	var b strings.Builder
	b.Grow(len(s))
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			b.WriteString(s)
			return b.String()
		}
		close := strings.IndexByte(s[open:], '}')
		if close < 0 {
			b.WriteString(s)
			return b.String()
		}
		close += open
		name := s[open+1 : close]
		if !validVarName(name) {
			b.WriteString(s[:close+1])
			s = s[close+1:]
			continue
		}
		b.WriteString(s[:open])
		b.WriteString(vars[name])
		s = s[close+1:]
	}
}

// validVarName reports whether name is a plausible `{variable}` token:
// lowercase letters, digits, and underscores only.
func validVarName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// Placeholders returns every distinct `{variable}` used by the template,
// sorted, so validation and the preview sidebar can report on them.
func (t Template) Placeholders() []string {
	seen := map[string]bool{}
	collect(t.Subject, seen)
	collect(t.Body, seen)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// collect adds every well-formed placeholder in s to seen.
func collect(s string, seen map[string]bool) {
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return
		}
		close := strings.IndexByte(s[open:], '}')
		if close < 0 {
			return
		}
		close += open
		if name := s[open+1 : close]; validVarName(name) {
			seen[name] = true
		}
		s = s[close+1:]
	}
}

// maxSubjectLength is AI.md PART 17 "Template Validation" -> Warnings:
// "Very long subject line (>78 chars)".
const maxSubjectLength = 78

// Validation is the outcome of checking a template before it is saved,
// per AI.md PART 17 "Template Validation". Errors block the save;
// Warnings never do.
type Validation struct {
	Errors   []string
	Warnings []string
}

// OK reports whether the template may be saved.
func (v Validation) OK() bool { return len(v.Errors) == 0 }

// ValidateTemplate applies the AI.md PART 17 "Template Validation" table.
// known is the set of variables legal for this template (globals plus its
// own), which ValidateRaw derives from the template name.
func ValidateTemplate(t Template, known map[string]bool) Validation {
	var v Validation
	if strings.TrimSpace(t.Subject) == "" {
		v.Errors = append(v.Errors, "Subject cannot be empty")
	}
	if strings.TrimSpace(t.Body) == "" {
		v.Errors = append(v.Errors, "Body cannot be empty")
	}
	for _, name := range t.Placeholders() {
		if known[name] {
			continue
		}
		if suggestion := closestVariable(name, known); suggestion != "" {
			v.Errors = append(v.Errors, fmt.Sprintf("Unknown variable: {%s}. Did you mean {%s}?", name, suggestion))
			continue
		}
		v.Errors = append(v.Errors, fmt.Sprintf("Unknown variable: {%s}", name))
	}
	if len(t.Subject) > maxSubjectLength {
		v.Warnings = append(v.Warnings, fmt.Sprintf("Subject line is %d characters (recommended maximum is %d)", len(t.Subject), maxSubjectLength))
	}
	if t.Name == EventSecurityAlert && !strings.Contains(t.Body, "{app_url}") {
		v.Warnings = append(v.Warnings, "Security alerts should include {app_url} so the operator can identify the server")
	}
	return v
}

// ValidateRaw parses and validates a candidate template in one step,
// returning a parse failure as the "Invalid template syntax" error of
// AI.md PART 17 "Template Validation".
func ValidateRaw(name, raw string) Validation {
	t, err := ParseTemplate(name, raw)
	if err != nil {
		return Validation{Errors: []string{err.Error()}}
	}
	return ValidateTemplate(t, KnownVariables(name))
}

// closestVariable returns the known variable within edit distance 2 of
// name, so the spec's "Did you mean {fqdn}?" hint can be produced. An
// empty result means nothing was close enough to suggest.
func closestVariable(name string, known map[string]bool) string {
	best := ""
	bestDist := 3
	names := make([]string, 0, len(known))
	for k := range known {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if d := editDistance(name, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// min3 returns the smallest of three ints.
func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
