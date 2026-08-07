// Simplified content negotiation for the health endpoints, per AI.md
// PART 14 "API Structure" content-negotiation rules. The full client-type
// detection matrix (isOurCliClient, isTextBrowser, HTML2TextConverter) is
// deferred — see TODO.AI.md.
package httpserver

import (
	"net/http"
	"strings"
)

// httpToolUserAgents are substrings identifying non-interactive HTTP
// clients, per AI.md PART 14's "non-interactive API clients: text" rule.
var httpToolUserAgents = []string{"curl", "wget", "httpie", "python-requests", "go-http-client"}

// wantsText reports whether req should receive the text/plain rendering
// of a response instead of the JSON default: a ".txt" path suffix, an
// explicit "Accept: text/plain", or a recognized non-interactive HTTP
// client (curl/wget/etc., or an empty User-Agent).
func wantsText(req *http.Request) bool {
	if strings.HasSuffix(req.URL.Path, ".txt") {
		return true
	}
	accept := req.Header.Get("Accept")
	if strings.Contains(accept, "text/plain") && !strings.Contains(accept, "application/json") {
		return true
	}
	return isHTTPTool(req)
}

// isHTTPTool reports whether req's User-Agent identifies a non-interactive
// HTTP client, per AI.md PART 14.
func isHTTPTool(req *http.Request) bool {
	ua := strings.ToLower(req.Header.Get("User-Agent"))
	if ua == "" {
		return true
	}
	for _, tool := range httpToolUserAgents {
		if strings.Contains(ua, tool) {
			return true
		}
	}
	return false
}
