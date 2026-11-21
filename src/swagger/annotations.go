package swagger

import "strings"

// Route is an API operation annotation consumed by the OpenAPI generator.
type Route struct {
	Path, Method, Summary, OperationID string
	Auth                               bool
	Example                            map[string]any
	ContentTypes                       []string
	Examples                           map[string]any
}

// RouteAnnotations mirrors the versioned API route registrations in
// src/httpserver/server.go and links.go.
func RouteAnnotations() []Route {
	return []Route{
		{Path: "/healthz", Method: "get", Summary: "Health status", OperationID: "getRootHealth"},
		{Path: "/api/healthz", Method: "get", Summary: "Health status", OperationID: "getAPIHealth"},
		{Path: "/api/autodiscover", Method: "get", Summary: "Discover compatible CLI binaries and minimum version", OperationID: "getCLIAutodiscover"},
		{Path: "/api/autodiscover", Method: "head", Summary: "Check CLI autodiscovery metadata", OperationID: "headCLIAutodiscover"},
		{Path: "/api/swagger", Method: "get", Summary: "OpenAPI specification (current-version alias)", OperationID: "getOpenAPIAlias"},
		{Path: "/api/graphql", Method: "post", Summary: "Execute a read-only GraphQL query (current-version alias)", OperationID: "executeGraphQLAlias", Example: map[string]any{"query": "{ links(limit: 10, offset: 0) { shortCode shortURL destinationURL } getAPIHealth }"}},
		{Path: "/cli/binaries/{name}", Method: "get", Summary: "Download a CLI binary", OperationID: "downloadCLIBinary"},
		{Path: "/cli/binaries/{name}", Method: "head", Summary: "Check CLI binary metadata", OperationID: "headCLIBinary"},
		{Path: "/api/{api_version}/server/healthz", Method: "get", Summary: "Health status", OperationID: "getHealth"},
		{Path: "/api/{api_version}/server/about", Method: "get", Summary: "About page content", OperationID: "getAboutPage"},
		{Path: "/api/{api_version}/server/privacy", Method: "get", Summary: "Privacy page content", OperationID: "getPrivacyPage"},
		{Path: "/api/{api_version}/server/help", Method: "get", Summary: "Help page content", OperationID: "getHelpPage"},
		{Path: "/api/{api_version}/server/help/mcp", Method: "get", Summary: "MCP help sections", OperationID: "getMCPHelp"},
		{Path: "/api/{api_version}/server/terms", Method: "get", Summary: "Terms page content", OperationID: "getTermsPage"},
		{Path: "/api/{api_version}/server/security", Method: "get", Summary: "Security disclosure information", OperationID: "getSecurityInfo"},
		{Path: "/api/{api_version}/server/security/thanks", Method: "get", Summary: "Security acknowledgments", OperationID: "getSecurityThanks"},
		{Path: "/api/{api_version}/server/mcp", Method: "post", Summary: "MCP Streamable HTTP endpoint", OperationID: "handleMCP"},
		{Path: "/api/mcp", Method: "post", Summary: "MCP Streamable HTTP endpoint alias", OperationID: "handleMCPAlias"},
		{Path: "/mcp", Method: "post", Summary: "MCP Streamable HTTP endpoint alias", OperationID: "handleRootMCP"},
		{Path: "/api/{api_version}/server/swagger", Method: "get", Summary: "OpenAPI specification", OperationID: "getOpenAPI"},
		{Path: "/api/{api_version}/server/graphql", Method: "post", Summary: "Execute a read-only GraphQL query", OperationID: "executeGraphQL", Example: map[string]any{"query": "{ links(limit: 10, offset: 0) { shortCode shortURL destinationURL } }"}},
		{Path: "/api/{api_version}/server/preferences", Method: "get", Summary: "Read preferences", OperationID: "getPreferences"},
		{Path: "/api/{api_version}/server/preferences", Method: "post", Summary: "Update preferences", OperationID: "updatePreferences", Example: map[string]any{"theme": "dark", "lang": "en"}},
		{Path: "/api/{api_version}/server/preferences/export", Method: "get", Summary: "Export preferences", OperationID: "exportPreferences"},
		{Path: "/api/{api_version}/server/preferences/import", Method: "get", Summary: "Import preferences", OperationID: "importPreferences"},
		{Path: "/api/{api_version}/server/auth/check", Method: "get", Summary: "Check API token authorization", OperationID: "checkAuthorization", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "get", Summary: "List API tokens", OperationID: "listTokens", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "post", Summary: "Create API token", OperationID: "createToken", Auth: true, Example: map[string]any{"name": "automation"}},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/reveal", Method: "post", Summary: "Reveal primary token", OperationID: "revealPrimaryToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/rotate", Method: "post", Summary: "Rotate primary token", OperationID: "rotatePrimaryToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/reveal", Method: "post", Summary: "Reveal linked token", OperationID: "revealLinkedToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/rotate", Method: "post", Summary: "Rotate linked token", OperationID: "rotateLinkedToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "patch", Summary: "Update API token", OperationID: "updateToken", Auth: true, Example: map[string]any{"name": "automation", "expiry": "30d"}},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "delete", Summary: "Revoke API token", OperationID: "revokeToken", Auth: true},
		{Path: "/api/{api_version}/server/security/policy", Method: "get", Summary: "Get security policy", OperationID: "getSecurityPolicy"},
		{Path: "/api/{api_version}/server/dpo", Method: "get", Summary: "Get data protection contact", OperationID: "getDPO"},
		{Path: "/api/{api_version}/server/contact", Method: "get", Summary: "Describe the private report form", OperationID: "getContactForm"},
		{Path: "/api/{api_version}/server/contact", Method: "post", Summary: "Submit or confirm a report", OperationID: "submitContactForm"},
		{Path: "/api/{api_version}/server/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getMetrics"},
		{Path: "/api/{api_version}/server/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getPrometheusMetrics"},
		{Path: "/api/{api_version}/server/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getGrafanaMetrics"},
		{Path: "/api/{api_version}/server/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getLokiMetrics"},
		{Path: "/server/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getServerMetrics"},
		{Path: "/server/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getServerPrometheusMetrics"},
		{Path: "/server/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getServerGrafanaMetrics"},
		{Path: "/server/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getServerLokiMetrics"},
		{Path: "/api/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getAPIMetrics"},
		{Path: "/api/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getAPIPrometheusMetrics"},
		{Path: "/api/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getAPIGrafanaMetrics"},
		{Path: "/api/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getAPILokiMetrics"},
		{Path: "/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getRootMetrics"},
		{Path: "/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getRootPrometheusMetrics"},
		{Path: "/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getRootGrafanaMetrics"},
		{Path: "/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getRootLokiMetrics"},
		{Path: "/api/{api_version}/server/reports/csp", Method: "post", Summary: "Receive CSP reports", OperationID: "reportCSP", ContentTypes: reportContentTypes(), Examples: map[string]any{"application/csp-report": map[string]any{"csp-report": map[string]any{"document-uri": "https://example.com/", "violated-directive": "script-src"}}, "application/reports+json": []any{map[string]any{"type": "csp-violation", "url": "https://example.com/", "body": map[string]any{"effectiveDirective": "script-src"}}}}},
		{Path: "/api/{api_version}/server/reports/nel", Method: "post", Summary: "Receive NEL reports", OperationID: "reportNEL", ContentTypes: reportContentTypes(), Examples: map[string]any{"application/reports+json": []any{map[string]any{"type": "network-error", "url": "https://example.com/", "body": map[string]any{"type": "tcp.reset"}}}}},
		{Path: "/api/{api_version}/server/reports/default", Method: "post", Summary: "Receive security reports", OperationID: "reportDefault", ContentTypes: reportContentTypes(), Examples: map[string]any{"application/reports+json": []any{map[string]any{"type": "csp-violation", "url": "https://example.com/", "body": map[string]any{"effectiveDirective": "script-src"}}}}},
		{Path: "/api/{api_version}/links", Method: "get", Summary: "List links", OperationID: "listLinks"},
		{Path: "/api/{api_version}/links", Method: "post", Summary: "Create a link", OperationID: "createLink", Example: map[string]any{"url": "https://example.com/article", "slug": "article"}},
		{Path: "/api/{api_version}/links/{slug}", Method: "get", Summary: "Get a link", OperationID: "getLink"},
		{Path: "/api/{api_version}/links/{slug}", Method: "patch", Summary: "Update a link", OperationID: "updateLink", Auth: true, Example: map[string]any{"url": "https://example.com/new-article"}},
		{Path: "/api/{api_version}/links/{slug}", Method: "delete", Summary: "Delete a link", OperationID: "deleteLink", Auth: true},
		{Path: "/api/{api_version}/links/{slug}/stats", Method: "get", Summary: "Get link statistics", OperationID: "getLinkStats"},
		{Path: "/{slug}", Method: "get", Summary: "Redirect to a short link", OperationID: "redirectShortLink"},
		{Path: "/{slug}/stats", Method: "get", Summary: "Read link statistics", OperationID: "getLinkStatsPage"},
	}
}

func reportContentTypes() []string {
	return []string{"application/csp-report", "application/reports+json"}
}

// IsReadPath reports whether path is a documented GET endpoint that is safe
// to expose through the query-only GraphQL API. The preferences import route
// is deliberately excluded because its GET handler writes preference cookies.
func IsReadPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for _, route := range RouteAnnotations() {
		if route.Method != "get" || route.OperationID == "importPreferences" || strings.HasPrefix(route.Path, "/cli/") {
			continue
		}
		routeParts := strings.Split(strings.Trim(route.Path, "/"), "/")
		if len(parts) != len(routeParts) {
			continue
		}
		matches := true
		for i, part := range routeParts {
			if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
				if parts[i] == "" {
					matches = false
					break
				}
				continue
			}
			if parts[i] != part {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
