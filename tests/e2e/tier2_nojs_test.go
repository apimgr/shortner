//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// TestTier2NoJavaScript is PART 28's Tier 2: with script execution turned
// off at the browser level, every core page must still render completely
// and every core control must still be present and submittable.
//
// The assertions are structural (DOM queries via CDP's DOM domain, which
// needs no script engine) rather than interaction-based: chromedp's
// visibility and click helpers evaluate JavaScript in the page, which is
// exactly what this tier disables. The no-JS *submission* path is proven
// end to end by Tier 1, which never involves a browser at all.
func TestTier2NoJavaScript(t *testing.T) {
	pages := []struct {
		path  string
		wants []string
	}{
		{"/", []string{`name="url"`, `name="csrf_token"`, `method="POST"`, `action="/"`, `type="submit"`}},
		{"/list", []string{"<body", "</html>"}},
		{"/server/about", []string{"<body", "</html>"}},
		{"/server/healthz", []string{"<body", "</html>"}},
	}

	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			ctx, _ := browserContext(t)

			var html string
			err := chromedp.Run(ctx,
				chromedp.ActionFunc(func(ctx context.Context) error {
					return emulation.SetScriptExecutionDisabled(true).Do(ctx)
				}),
				chromedp.Navigate(browserBaseURL+page.path),
				chromedp.OuterHTML("html", &html, chromedp.ByQuery),
			)
			if err != nil {
				t.Fatalf("loading %s with JavaScript disabled: %v", page.path, err)
			}

			for _, want := range page.wants {
				if !strings.Contains(html, want) {
					saveArtifacts(t, "tier2"+strings.ReplaceAll(page.path, "/", "-"), html, nil)
					t.Errorf("%s without JavaScript is missing %q", page.path, want)
				}
			}
			if strings.Contains(html, "{{") {
				t.Errorf("%s leaked an unrendered template action", page.path)
			}
		})
	}
}
