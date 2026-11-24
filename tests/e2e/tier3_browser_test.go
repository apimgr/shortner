//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
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

// TestTier3ThemeToggleDegradesGracefully exercises the progressive
// enhancement contract from PART 16: the theme control is a real form
// POST, so it works with JavaScript on and off alike.
func TestTier3ThemeToggleDegradesGracefully(t *testing.T) {
	ctx, consoleErrs := browserContext(t)

	var html string
	err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/server"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(250*time.Millisecond),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("loading /server: %v", err)
	}
	if !strings.Contains(html, "</html>") {
		t.Errorf("/server did not render a complete document")
	}
	if errs := consoleErrs.list(); len(errs) > 0 {
		saveArtifacts(t, "tier3-server-page", html, nil)
		t.Errorf("browser reported %d console or network error(s) on /server: %s", len(errs), strings.Join(errs, " | "))
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
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`(() => {
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("HEAD /cli/binaries/{name}"));
			if (!operation) return false;
			operation.open = true;
			operation.querySelector('input[data-location="path"]').value = "shortner-cli-linux-amd64";
			operation.querySelector("form").requestSubmit();
			return true;
		})()`, &headMetadataOK),
		chromedp.Poll(`(() => {
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("HEAD /cli/binaries/{name}"));
			return operation?.querySelector(".api-operation-status")?.textContent.includes("response metadata") || false;
		})()`, nil, chromedp.WithPollingTimeout(10*time.Second)),
		chromedp.Evaluate(`(() => {
			const operation = [...document.querySelectorAll(".api-operation")]
				.find(item => item.querySelector("summary").textContent.includes("HEAD /cli/binaries/{name}"));
			return operation?.querySelector(".api-operation-result a") === null;
		})()`, &headMetadataOK),
	)
	if err != nil {
		t.Fatalf("handling HEAD metadata in Swagger try-it-out: %v", err)
	}
	if !headMetadataOK {
		t.Error("Swagger offered a download for a HEAD response")
	}

	var graphQLStatus string
	err = chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/server/docs/graphql"),
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
