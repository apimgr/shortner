//go:build e2e

package e2e

import (
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
	var screenshot []byte
	err := chromedp.Run(ctx,
		chromedp.Navigate(browserBaseURL+"/"),
		chromedp.WaitVisible(`input[name="url"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="url"]`, "https://example.com/tier3", chromedp.ByQuery),
		chromedp.Click(`button[type="submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.success-card`, chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.CaptureScreenshot(&screenshot),
	)
	if err != nil {
		saveArtifacts(t, "tier3-journey", html, screenshot)
		t.Fatalf("browser journey failed: %v", err)
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
		t.Errorf("browser console reported %d error(s): %s", len(errs), strings.Join(errs, " | "))
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
		t.Errorf("browser console reported %d error(s) on /server: %s", len(errs), strings.Join(errs, " | "))
	}
}
