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
		{"/server/privacy", []string{"<body", "</html>"}},
		{"/server/help", []string{"<body", "</html>"}},
		{"/server/help/mcp", []string{"MCP", "</html>"}},
		{"/server/terms", []string{"<body", "</html>"}},
		{"/server/contact?type=privacy", []string{`name="csrf_token"`, `name="form_token"`, `method="POST"`, "</html>"}},
		{"/server/ccpa", []string{"<body", "</html>"}},
		{"/server/security", []string{"<body", "</html>"}},
		{"/server/security/policy", []string{"<body", "</html>"}},
		{"/server/security/thanks", []string{"<body", "</html>"}},
		{"/server/dpo", []string{"<body", "</html>"}},
		{"/server/healthz", []string{"<body", "</html>"}},
		{"/server/preferences", []string{`name="theme"`, `id="preferences-lang"`, "</html>"}},
		{"/server/preferences/tokens", []string{`action="/server/preferences/token/restore"`, `name="token"`, "</html>"}},
		{"/server/preferences/sync", []string{"name=\"code\"", "action=\"/server/preferences/sync\"", "type=\"submit\""}},
		{"/server/docs/swagger", []string{"GET /api/{api_version}/links", `id="openapi-document"`}},
		{"/server/docs/graphql", []string{"linkStats", `name="variables"`, `name="operationName"`, `id="graphql-schema"`, `"__schema"`}},
		{"/tier2notfound", []string{`class="page-title"`, "Not found", "</html>"}},
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

// TestTier2NativeCreateResolveAndStats proves the create form and resource
// journey work when scripts are disabled in Chromium.
func TestTier2NativeCreateResolveAndStats(t *testing.T) {
	ctx, _ := browserContext(t)
	var html string
	err := chromedp.Run(ctx,
		emulation.SetScriptExecutionDisabled(true),
		chromedp.Navigate(browserBaseURL+"/"),
		chromedp.WaitVisible(`input[name="url"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="url"]`, "https://example.com/tier2-feature", chromedp.ByQuery),
		chromedp.SendKeys(`input[name="slug"]`, "tier2feature", chromedp.ByQuery),
		chromedp.Click(`form[data-action="create-link-form"] button[type="submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#home-success-title`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("creating a custom link without JavaScript: %v", err)
	}
	if !strings.Contains(html, "tier2feature") || !strings.Contains(html, "Success!") {
		saveArtifacts(t, "tier2-create", html, nil)
		t.Fatalf("no-JavaScript create form did not render the created link")
	}
	if err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/list"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("opening link list without JavaScript: %v", err)
	}
	if !strings.Contains(html, "tier2feature") || !strings.Contains(html, "https://example.com/tier2-feature") {
		t.Errorf("no-JavaScript list does not show the new link")
	}
	if err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/tier2feature/stats"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("opening click statistics without JavaScript: %v", err)
	}
	if !strings.Contains(html, "tier2feature") || !strings.Contains(html, "https://example.com/tier2-feature") || !strings.Contains(html, "No clicks recorded yet.") {
		t.Errorf("no-JavaScript stats page omitted its resource data")
	}
}

// TestTier2ServerRenderedSlugErrors verifies that rejected form input is
// explained in the initial response with JavaScript disabled.
func TestTier2ServerRenderedSlugErrors(t *testing.T) {
	ctx, _ := browserContext(t)
	var html string
	err := chromedp.Run(ctx,
		emulation.SetScriptExecutionDisabled(true),
		chromedp.Navigate(browserBaseURL+"/"),
		chromedp.WaitVisible(`input[name="url"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="url"]`, "https://example.com/rejected", chromedp.ByQuery),
		chromedp.SendKeys(`input[name="slug"]`, "server", chromedp.ByQuery),
		chromedp.Click(`form[data-action="create-link-form"] button[type="submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[role="alert"]`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("rendering invalid slug response with JavaScript disabled: %v", err)
	}
	if !strings.Contains(strings.ToLower(html), "reserved") || !strings.Contains(html, `name="slug"`) {
		saveArtifacts(t, "tier2-invalid-slug", html, nil)
		t.Errorf("reserved slug error was not rendered with the form")
	}
}
