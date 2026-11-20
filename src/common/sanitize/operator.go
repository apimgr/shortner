package sanitize

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

const maxOperatorContentSize = 256 * 1024

// LoadOperatorContent reads the highest-priority safe operator content file
// for a named public-page extension.
func LoadOperatorContent(root, name, lang, host string) (template.HTML, string, string, error) {
	if root == "" || (name != "header" && name != "footer" && name != "about" && name != "privacy" && name != "terms") {
		return "", "", "", nil
	}
	exts := []string{"html"}
	if name != "header" && name != "footer" {
		exts = []string{"html", "md", "txt"}
	}
	for _, language := range []string{lang, ""} {
		for _, ext := range exts {
			filename := name
			if language != "" {
				filename += "." + language
			}
			filename += "." + ext
			path := filepath.Join(root, filename)
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return "", "", "", fmt.Errorf("inspect %s: %w", filename, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					return "", "", "", fmt.Errorf("resolve %s: %w", filename, err)
				}
				rel, err := filepath.Rel(root, resolved)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
					return "", "", "", fmt.Errorf("operator content symlink escapes html directory: %s", filename)
				}
				info, err = os.Stat(path)
				if err != nil {
					return "", "", "", err
				}
			}
			if !info.Mode().IsRegular() {
				continue
			}
			if info.Size() > maxOperatorContentSize {
				return "", "", "", fmt.Errorf("operator content %s exceeds 256 KiB", filename)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return "", "", "", fmt.Errorf("read %s: %w", filename, err)
			}
			raw := string(data)
			if strings.Contains(strings.ToLower(raw), "/server/contact") {
				return "", "", "", fmt.Errorf("operator content %s must not link to /server/contact", filename)
			}
			var rendered string
			switch ext {
			case "md":
				rendered = string(RenderMarkdown(raw, host))
			case "txt":
				paragraphs := strings.Split(strings.TrimSpace(raw), "\n\n")
				for i := range paragraphs {
					paragraphs[i] = "<p>" + template.HTMLEscapeString(strings.TrimSpace(paragraphs[i])) + "</p>"
				}
				rendered = strings.Join(paragraphs, "\n")
			default:
				rendered = raw
			}
			return SanitizeOperatorHTML(rendered, name), raw, ext, nil
		}
	}
	return "", "", "", nil
}

// SanitizeOperatorHTML applies the page-specific allow-list to rendered
// operator content. Call it after variable expansion so inserted values are
// covered by the same policy as the source file.
func SanitizeOperatorHTML(content, name string) template.HTML {
	policy := footerPolicy()
	if name != "header" && name != "footer" {
		policy.AllowElements("table", "thead", "tbody", "tr", "th", "td", "pre", "code", "blockquote", "hr", "dl", "dt", "dd", "sup", "sub")
	}
	return template.HTML(policy.Sanitize(content))
}

// OperatorContentSource reads the selected source file for a public-page API
// response without exposing rendered HTML.
func OperatorContentSource(root, name, lang string) (string, string, error) {
	_, raw, format, err := LoadOperatorContent(root, name, lang, "")
	return raw, format, err
}
