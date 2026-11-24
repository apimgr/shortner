// Package httputil provides client detection and HTML-to-text rendering for
// server-rendered frontend responses, per AI.md PART 16.
package httputil

import (
	"net/http"
	"strings"
)

var textBrowserAgents = []string{
	"lynx/", "w3m/", "links ", "links/", "elinks/", "browsh/", "carbonyl/", "netsurf",
}

var httpToolAgents = []string{
	"curl/", "wget/", "httpie/", "xh/", "hurl/", "libcurl/",
	"python-requests/", "python-urllib/", "python-httpx/", "aiohttp/", "urllib3/",
	"go-http-client/", "go-resty/", "fasthttp", "reqwest/", "ureq/", "hyper/",
	"axios/", "node-fetch/", "undici", "superagent/", "deno/", "bun/",
	"okhttp/", "apache-httpclient/", "java/", "guzzlehttp/", "libwww-perl/", "lwp-trivial/", "faraday", "dart/", "windowspowershell/",
	"postmanruntime/", "insomnia/", "bruno-runtime/", "k6/",
}

// IsOurCLIClient identifies the interactive companion client, which handles
// its own presentation and expects JSON.
func IsOurCLIClient(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("User-Agent"), "shortner-cli/")
}

// IsTextBrowser identifies an interactive text browser. These clients use
// ordinary server-rendered HTML and do not depend on JavaScript.
func IsTextBrowser(r *http.Request) bool {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	for _, agent := range textBrowserAgents {
		if strings.Contains(ua, agent) {
			return true
		}
	}
	return false
}

// IsHTTPTool identifies a non-interactive HTTP client that should receive
// readable plain text for frontend pages.
func IsHTTPTool(r *http.Request) bool {
	if IsOurCLIClient(r) || IsTextBrowser(r) {
		return false
	}
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	if ua == "" {
		return true
	}
	for _, agent := range httpToolAgents {
		if strings.Contains(ua, agent) {
			return true
		}
	}
	return false
}

// IsNonInteractiveClient reports whether the request cannot interact with
// server-rendered HTML. Text browsers and the companion CLI are interactive.
func IsNonInteractiveClient(r *http.Request) bool { return IsHTTPTool(r) }
