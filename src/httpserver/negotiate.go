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
	// An explicit Accept always beats User-Agent sniffing, per AI.md PART
	// 14: `Accept: application/json` must return JSON even to curl, and
	// PART 28's content-negotiation matrix tests exactly that.
	accept := req.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return false
	}
	if strings.Contains(accept, "text/plain") {
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

// clientType is the three-way content-negotiation outcome for a frontend
// route, per AI.md PART 16 "Smart Content Detection".
type clientType int

const (
	clientHTML clientType = iota
	clientText
	clientJSON
)

// detectClientType classifies req for frontend routes, per AI.md PART 16:
// a ".json"/".txt" path suffix or explicit Accept header wins outright;
// otherwise a recognized non-interactive HTTP client (curl/wget/etc., or an
// empty User-Agent) gets text; a browser (anything else, including an
// Accept header containing "text/html") gets HTML.
func detectClientType(req *http.Request) clientType {
	switch {
	case strings.HasSuffix(req.URL.Path, ".json"):
		return clientJSON
	case strings.HasSuffix(req.URL.Path, ".txt"):
		return clientText
	}

	accept := req.Header.Get("Accept")
	switch {
	case strings.Contains(accept, "application/json"):
		return clientJSON
	case strings.Contains(accept, "text/html"):
		return clientHTML
	case strings.Contains(accept, "text/plain"):
		return clientText
	}

	if isHTTPTool(req) {
		return clientText
	}
	return clientHTML
}
