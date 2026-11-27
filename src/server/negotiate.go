// Content negotiation for API and frontend routes, per AI.md PARTS 14 and 16.
package server

import (
	"net/http"
	"strings"

	"github.com/apimgr/shortner/src/common/httputil"
)

// wantsText reports whether req should receive the text/plain rendering
// of a response instead of the JSON default: a ".txt" path suffix, an
// explicit "Accept: text/plain", or a recognized non-interactive HTTP
// client (curl/wget/etc., or an empty User-Agent).
func wantsText(req *http.Request) bool {
	if strings.HasSuffix(originalRequestPath(req), ".txt") {
		return true
	}
	// An explicit Accept always beats User-Agent sniffing, per AI.md PART
	// 14: `Accept: application/json` must return JSON even to curl, and
	// PART 28's content-negotiation matrix tests exactly that.
	accept := req.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return false
	}
	if strings.Contains(accept, "text/html") {
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
	return httputil.IsHTTPTool(req)
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
	case strings.HasSuffix(originalRequestPath(req), ".json"):
		return clientJSON
	case strings.HasSuffix(originalRequestPath(req), ".txt"):
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

	if httputil.IsOurCLIClient(req) {
		return clientJSON
	}
	if httputil.IsTextBrowser(req) {
		return clientHTML
	}
	if isHTTPTool(req) {
		return clientText
	}
	return clientHTML
}
