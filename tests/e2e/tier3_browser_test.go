//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestTier3FullBrowser is PART 28's Tier 3: a real browser with JavaScript
// enabled drives the primary user journey end to end, and the run is only
// a pass if the console stayed completely free of errors.
func TestTier3FullBrowser(t *testing.T) {
	ctx, consoleErrs := browserContext(t)

	var html string
	var diagnostics string
	var screenshot []byte
	stage := "navigate to homepage"
	err := chromedp.Run(ctx, chromedp.Navigate(browserBaseURL+"/"))
	if err == nil {
		stage = "wait for URL field"
		err = chromedp.Run(ctx, chromedp.WaitVisible(`input[name="url"]`, chromedp.ByQuery))
	}
	if err == nil {
		stage = "enter destination URL"
		err = chromedp.Run(ctx, chromedp.SendKeys(`input[name="url"]`, "https://example.com/tier3", chromedp.ByQuery))
	}
	if err == nil {
		stage = "submit link form"
		err = chromedp.Run(ctx, chromedp.Click(`form[data-action="create-link-form"] button[type="submit"]`, chromedp.ByQuery))
	}
	if err == nil {
		stage = "wait for server-rendered success card"
		waitCtx, cancelWait := context.WithTimeout(ctx, 10*time.Second)
		err = chromedp.Run(waitCtx, chromedp.WaitVisible(`#home-success-title`, chromedp.ByQuery))
		cancelWait()
	}
	if err != nil {
		_ = chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery), chromedp.Evaluate(`JSON.stringify({url: location.href, styles: [...document.styleSheets].map(s => s.href), resources: performance.getEntriesByType("resource").map(e => e.name), forms: [...document.forms].map(f => ({action: f.action, valid: f.checkValidity()})), value: document.querySelector('input[name="url"]')?.value})`, &diagnostics), chromedp.CaptureScreenshot(&screenshot))
	}
	if err == nil {
		stage = "capture success page"
		err = chromedp.Run(ctx, chromedp.OuterHTML("html", &html, chromedp.ByQuery), chromedp.CaptureScreenshot(&screenshot))
	}
	if err != nil {
		saveArtifacts(t, "tier3-journey", html, screenshot)
		t.Fatalf("browser journey failed while trying to %s: %v; browser state: %s", stage, err, diagnostics)
	}

	if !strings.Contains(html, "Success!") {
		saveArtifacts(t, "tier3-journey", html, screenshot)
		t.Errorf("success card did not render")
	}
	if !strings.Contains(html, browserBaseURL+"/") {
		saveArtifacts(t, "tier3-journey", html, screenshot)
		t.Errorf("success card did not show a short URL on this host")
	}

	if errs := consoleErrs.list(); len(errs) > 0 {
		saveArtifacts(t, "tier3-journey", html, screenshot)
		t.Errorf("browser reported %d console or network error(s): %s", len(errs), strings.Join(errs, " | "))
	}
}

// TestTier3CustomSlugListAndAnalytics checks the browser-enhanced user
// journey while keeping destination navigation inside the test process.
func TestTier3CustomSlugListAndAnalytics(t *testing.T) {
	ctx, browserErrs := browserContext(t)
	var html string
	const slug = "tier3feature"
	err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/"),
		chromedp.WaitVisible(`input[name="url"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="url"]`, "https://example.com/tier3-feature", chromedp.ByQuery),
		chromedp.SendKeys(`input[name="slug"]`, slug, chromedp.ByQuery),
		chromedp.Click(`form[data-action="create-link-form"] button[type="submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#home-success-title`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("creating custom link in browser: %v", err)
	}
	if !strings.Contains(html, browserBaseURL+"/"+slug) {
		t.Fatalf("custom URL missing from browser success page")
	}

	redirect, _ := get(t, newClient(t), "/"+slug)
	if redirect.StatusCode != 302 || redirect.Header.Get("Location") != "https://example.com/tier3-feature" {
		t.Errorf("vanity redirect = %d to %q", redirect.StatusCode, redirect.Header.Get("Location"))
	}
	if err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/list"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("loading public link list: %v", err)
	}
	if !strings.Contains(html, slug) || !strings.Contains(html, "https://example.com/tier3-feature") {
		t.Errorf("browser list omitted the created link")
	}
	if err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/"+slug+"/stats"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("loading public analytics page: %v", err)
	}
	if !strings.Contains(html, slug) || !strings.Contains(html, "https://example.com/tier3-feature") || !strings.Contains(html, `class="info-value">1</span>`) {
		saveArtifacts(t, "tier3-stats", html, nil)
		t.Errorf("analytics page does not show the recorded redirect")
	}
	if errs := browserErrs.list(); len(errs) > 0 {
		t.Errorf("browser reported console or network errors during resource journey: %s", strings.Join(errs, " | "))
	}
}

// TestTier3ThemeToggleDegradesGracefully exercises the real preferences
// form and confirms dark and light themes change computed browser styles.
func TestTier3ThemeToggleDegradesGracefully(t *testing.T) {
	ctx, consoleErrs := browserContext(t)

	var html string
	var darkBackground string
	var lightBackground string
	err := chromedp.Run(ctx, network.SetCookie("theme", "light").WithURL(browserBaseURL).WithPath("/"))
	if err == nil {
		err = chromedp.Run(ctx,
			chromedp.Navigate(browserBaseURL+"/server/preferences"),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.Poll(`document.documentElement.classList.contains("theme-light")`, nil, chromedp.WithPollingTimeout(10*time.Second)),
			chromedp.Evaluate(`getComputedStyle(document.body).backgroundColor`, &lightBackground),
		)
	}
	if err == nil {
		err = chromedp.Run(ctx,
			network.SetCookie("theme", "dark").WithURL(browserBaseURL).WithPath("/"),
			chromedp.Navigate(browserBaseURL+"/server/preferences"),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.Poll(`document.documentElement.classList.contains("theme-dark")`, nil, chromedp.WithPollingTimeout(10*time.Second)),
			chromedp.Evaluate(`getComputedStyle(document.body).backgroundColor`, &darkBackground),
			chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		)
	}
	if err != nil {
		t.Fatalf("submitting server-rendered theme preferences: %v", err)
	}
	if !strings.Contains(html, `class="theme-dark"`) {
		t.Errorf("preferences form did not render the selected dark theme")
	}
	if darkBackground == "" || lightBackground == "" || darkBackground == lightBackground {
		t.Errorf("theme preference did not change computed background: dark=%q light=%q", darkBackground, lightBackground)
	}
	if errs := consoleErrs.list(); len(errs) > 0 {
		saveArtifacts(t, "tier3-theme-preferences", html, nil)
		t.Errorf("browser reported %d console or network error(s) on preferences: %s", len(errs), strings.Join(errs, " | "))
	}
}

// TestTier3MobileAndLanguagePreferences checks mobile layout and confirms
// selecting a language changes server-rendered strings in the browser.
func TestTier3MobileAndLanguagePreferences(t *testing.T) {
	ctx, browserErrs := browserContext(t)
	var noOverflow bool
	var englishHeading string
	var spanishHeading string
	var html string
	err := chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(375, 812, 1, false),
		chromedp.Navigate(browserBaseURL+"/server/preferences?lang=en"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth <= window.innerWidth`, &noOverflow),
		chromedp.Text("h1.page-title", &englishHeading, chromedp.ByQuery),
	)
	if err == nil {
		err = chromedp.Run(ctx,
			chromedp.Navigate(browserBaseURL+"/server/preferences?lang=es"),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.Text("h1.page-title", &spanishHeading, chromedp.ByQuery),
			chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		)
	}
	if err != nil {
		t.Fatalf("checking mobile and language preferences: %v", err)
	}
	if !noOverflow {
		t.Error("preferences page has horizontal overflow at 375px")
	}
	if englishHeading == "" || spanishHeading == "" || englishHeading == spanishHeading || !strings.Contains(html, `lang="es"`) {
		t.Errorf("language selection did not change rendered strings: en=%q es=%q", englishHeading, spanishHeading)
	}
	if errs := browserErrs.list(); len(errs) > 0 {
		t.Errorf("browser reported console or network errors during responsive/i18n test: %s", strings.Join(errs, " | "))
	}
}

// TestTier3ServerPageCrawl follows the known public server-page surfaces and
// checks that each renders real document content in Chromium.
func TestTier3ServerPageCrawl(t *testing.T) {
	ctx, browserErrs := browserContext(t)
	paths := []string{
		"/", "/list", "/server/about", "/server/privacy", "/server/help", "/server/help/mcp",
		"/server/terms", "/server/contact", "/server/contact?type=privacy", "/server/ccpa", "/server/security",
		"/server/security/policy", "/server/security/thanks", "/server/dpo", "/server/healthz",
		"/server/preferences", "/server/preferences/tokens", "/server/preferences/sync",
		"/server/docs/swagger", "/server/docs/graphql",
	}
	for _, path := range paths {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			var title string
			var pageTitle string
			err := chromedp.Run(ctx,
				chromedp.Navigate(browserBaseURL+path),
				chromedp.WaitReady("body", chromedp.ByQuery),
				chromedp.Title(&title),
				chromedp.Text("h1.page-title", &pageTitle, chromedp.ByQuery),
			)
			if err != nil {
				t.Fatalf("loading %s: %v", path, err)
			}
			if title == "" || pageTitle == "" {
				t.Errorf("%s did not render a document title and page heading", path)
			}
		})
	}
	notFound, notFoundBody := get(t, newClient(t), "/tier3notfound")
	if notFound.StatusCode != http.StatusNotFound || !strings.Contains(notFoundBody, `class="page-title"`) {
		t.Errorf("missing resource page = %d, want themed 404", notFound.StatusCode)
	}
	if errs := browserErrs.list(); len(errs) > 0 {
		t.Errorf("browser reported console or network errors while crawling server pages: %s", strings.Join(errs, " | "))
	}
}

func TestTier3ToastAccessibilityStackingAndDismissal(t *testing.T) {
	ctx, browserErrs := browserContext(t)
	var checks map[string]bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/"),
		chromedp.WaitVisible("#toast-container", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const firstId = window.showToast("A successful action", "success", 0);
			const first = document.querySelector('[data-toast-id="' + firstId + '"]');
			const accessible = first && first.getAttribute("role") === "status" &&
				first.querySelector(".toast-message").textContent === "A successful action" &&
				first.querySelector(".toast-close").getAttribute("aria-label") !== "";
			first.querySelector(".toast-close").click();
			const closeWorks = !document.querySelector(".toast");
			const ids = [];
			for (let i = 0; i < 6; i++) ids.push(window.showToast("queued " + i, "success", 0));
			const stacked = document.querySelectorAll("#toast-container .toast").length === 5;
			window.dismissToast(ids[0]);
			const queuePromoted = document.querySelectorAll("#toast-container .toast").length === 5 &&
				document.querySelector("#toast-container .toast-message").textContent === "queued 5";
			window.dismissAllToasts();
			return {accessible, closeWorks, stacked, queuePromoted, cleared: !document.querySelector(".toast")};
		})()`, &checks),
	)
	if err != nil {
		t.Fatalf("running toast checks in browser: %v", err)
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("toast check %q failed", name)
		}
	}
	if errs := browserErrs.list(); len(errs) > 0 {
		t.Errorf("browser reported %d console or network error(s) on toast page: %s", len(errs), strings.Join(errs, " | "))
	}
}

func TestTier3APIDocsInteractions(t *testing.T) {
	ctx, browserErrs := browserContext(t)
	var submitted bool
	var result string
	var noHorizontalOverflow bool
	var binaryDownloadOK bool
	var headMetadataOK bool
	err := chromedp.Run(ctx,
		emulation.SetDeviceMetricsOverride(375, 812, 1, false),
		chromedp.Navigate(browserBaseURL+"/server/docs/swagger"),
		chromedp.WaitVisible(".api-operation", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("GET /api/healthz"));
			if (!operation) return false;
			operation.open = true;
			operation.querySelector("form").requestSubmit();
			return true;
		})()`, &submitted),
		chromedp.Poll(`(() => {
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("GET /api/healthz"));
			return operation?.querySelector(".api-operation-status")?.textContent.includes("HTTP 200") || false;
		})()`, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(`(() => [...document.querySelectorAll(".api-operation")]
			.find(item => item.querySelector("summary").textContent.includes("GET /api/healthz"))
			?.querySelector(".api-operation-result")?.textContent || "")()`, &result),
		chromedp.Evaluate(`document.documentElement.scrollWidth <= window.innerWidth`, &noHorizontalOverflow),
	)
	if err != nil {
		t.Fatalf("running Swagger try-it-out at mobile width: %v", err)
	}
	if !submitted {
		t.Fatal("the API health operation was not submitted")
	}
	if !strings.Contains(result, "status") {
		t.Errorf("Swagger health response = %q, want health JSON", result)
	}
	if !noHorizontalOverflow {
		t.Error("Swagger page overflows a 375px viewport")
	}
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
			const nativeFetch = window.fetch.bind(window);
			window.fetch = (input, init) => String(input).includes("/cli/binaries/")
				? Promise.resolve(new Response(init?.method === "HEAD" ? null : new Uint8Array([0, 255, 16, 128]), {
					status: 200,
					headers: {
						"Content-Type": "application/octet-stream",
						"Content-Disposition": 'attachment; filename="shortner-cli-linux-amd64"'
					}
				}))
				: nativeFetch(input, init);
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("GET /cli/binaries/{name}"));
			if (!operation) return false;
			operation.open = true;
			operation.querySelector('input[data-location="path"]').value = "shortner-cli-linux-amd64";
			operation.querySelector("form").requestSubmit();
			return true;
		})()`, &binaryDownloadOK),
		chromedp.Poll(`(() => {
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("GET /cli/binaries/{name}"));
			const link = operation?.querySelector(".api-operation-result a");
			return link?.download === "shortner-cli-linux-amd64" || false;
		})()`, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(`(() => {
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("GET /cli/binaries/{name}"));
			const link = operation?.querySelector(".api-operation-result a");
			return link?.download === "shortner-cli-linux-amd64" && link.href.startsWith("blob:");
		})()`, &binaryDownloadOK),
	)
	if err != nil {
		t.Fatalf("handling a binary response in Swagger try-it-out: %v", err)
	}
	if !binaryDownloadOK {
		t.Error("Swagger did not preserve the binary response as a downloadable file")
	}
	err = chromedp.Run(ctx, chromedp.Evaluate(`(() => {
		const spec = JSON.parse(document.getElementById("openapi-document").textContent);
		return Boolean(spec.paths["/cli/binaries/{name}"]?.head);
	})()`, &headMetadataOK))
	if err != nil || !headMetadataOK {
		t.Errorf("Swagger OpenAPI document did not expose the HEAD binary metadata operation: %v", err)
	}

	var graphQLStatus string
	err = chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/server/docs/graphql?lang=en"),
		chromedp.WaitVisible("#graphql-query", chromedp.ByQuery),
		chromedp.Click(`form[data-graphql-url] button[type="submit"]`, chromedp.ByQuery),
		chromedp.Poll(`document.getElementById("graphql-status")?.textContent === "Query complete."`, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Text("#graphql-result", &result, chromedp.ByQuery),
	)
	if err != nil {
		_ = chromedp.Run(ctx,
			chromedp.Text("#graphql-status", &graphQLStatus, chromedp.ByQuery),
			chromedp.Text("#graphql-result", &result, chromedp.ByQuery),
		)
		t.Fatalf("running GraphQL query from the docs page: %v; status %q; response %q; browser errors %q", err, graphQLStatus, result, browserErrs.list())
	}
	if !strings.Contains(result, `"getAPIHealth"`) || !strings.Contains(result, `"links"`) {
		t.Errorf("GraphQL docs response = %q, want link and health results", result)
	}
	if errs := browserErrs.list(); len(errs) > 0 {
		t.Errorf("browser reported %d console or network error(s) on API docs pages: %s", len(errs), strings.Join(errs, " | "))
	}
}
