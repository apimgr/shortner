package sanitize

import "testing"

func TestSanitizeFooterHTML_PassthroughSentinels(t *testing.T) {
	if got := SanitizeFooterHTML(""); got != "" {
		t.Errorf("empty input: got %q, want empty", got)
	}
	if got := SanitizeFooterHTML(" "); got != " " {
		t.Errorf("space sentinel: got %q, want %q", got, " ")
	}
}

func TestSanitizeFooterHTML_AllowsFormatting(t *testing.T) {
	in := `<p>Powered by <strong>Shortner</strong> - <a href="https://example.com">home</a></p>`
	got := SanitizeFooterHTML(in)
	if got == "" {
		t.Fatalf("sanitized output is empty for valid input %q", in)
	}
	for _, want := range []string{"<p>", "<strong>", "<a href=", "example.com"} {
		if !contains(got, want) {
			t.Errorf("sanitized output %q missing %q", got, want)
		}
	}
}

func TestSanitizeFooterHTML_StripsScript(t *testing.T) {
	in := `<p>hi</p><script>alert(1)</script>`
	got := SanitizeFooterHTML(in)
	if contains(got, "<script") || contains(got, "alert(1)") {
		t.Errorf("sanitized output %q still contains script content", got)
	}
}

func TestSanitizeFooterHTML_StripsJavascriptURL(t *testing.T) {
	in := `<a href="javascript:alert(1)">click</a>`
	got := SanitizeFooterHTML(in)
	if contains(got, "javascript:") {
		t.Errorf("sanitized output %q still contains a javascript: URL", got)
	}
}

func TestValidateFooterHTML_ErrorsOnFullyStripped(t *testing.T) {
	_, err := ValidateFooterHTML(`<script>alert(1)</script>`)
	if err == nil {
		t.Fatal("expected an error for input that sanitizes to nothing, got nil")
	}
}

func TestValidateFooterHTML_OKOnValid(t *testing.T) {
	out, err := ValidateFooterHTML(`<p>Hello</p>`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty sanitized output")
	}
}

func TestValidateFooterHTML_EmptyIsOK(t *testing.T) {
	out, err := ValidateFooterHTML("")
	if err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if out != "" {
		t.Errorf("got %q, want empty", out)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
