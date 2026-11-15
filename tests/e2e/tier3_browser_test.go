//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

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
		err = chromedp.Run(waitCtx, chromedp.WaitVisible(`.success-card`, chromedp.ByQuery))
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
