package sanitize

import (
	"strings"
	"testing"
)

func TestRenderMarkdownSanitizesAndRendersCommonContent(t *testing.T) {
	input := "# Heading\n\n| Name | Count |\n| --- | ---: |\n| links | 2 |\n\n[external](https://outside.example/path) [internal](/help)\n\n```go\nfmt.Println(\"<script>\")\n```\n\n<script>alert(1)</script>"
	got := string(RenderMarkdown(input, "shortner.example"))
	for _, want := range []string{"<h1>Heading</h1>", "<table>", "<th>Name</th>", `rel="noopener noreferrer nofollow ugc"`, `href="/help"`, "<pre><code", "&lt;script&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Markdown %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "<script>") || strings.Contains(got, "javascript:") {
		t.Errorf("rendered Markdown contains active content: %q", got)
	}
}
