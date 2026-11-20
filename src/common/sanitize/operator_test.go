package sanitize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOperatorContentPrefersLanguageAndSanitizes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "about.md"), []byte("# Default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "about.fr.md"), []byte("# Bonjour\n\n<script>alert(1)</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, raw, format, err := LoadOperatorContent(dir, "about", "fr", "example.test")
	if err != nil {
		t.Fatal(err)
	}
	if format != "md" || !strings.Contains(string(got), "Bonjour") || strings.Contains(string(got), "<script") || !strings.Contains(raw, "Bonjour") {
		t.Fatalf("loaded content = %q, raw %q, format %q", got, raw, format)
	}
}

func TestLoadOperatorContentRejectsContactLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terms.html")
	if err := os.WriteFile(path, []byte(`<a href="/server/contact">Contact</a>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadOperatorContent(dir, "terms", "en", ""); err == nil {
		t.Fatal("contact form link should be rejected")
	}
}

func TestLoadOperatorContentEnforcesSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "footer.html")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxOperatorContentSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := LoadOperatorContent(dir, "footer", "en", ""); err == nil {
		t.Fatal("oversized operator content should be rejected")
	}
}
