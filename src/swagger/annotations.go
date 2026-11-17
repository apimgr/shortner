package swagger

// Route is an API operation annotation consumed by the OpenAPI generator.
type Route struct {
	Path, Method, Summary, OperationID string
	Auth                               bool
	Example                            map[string]any
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
		{Path: "/api/graphql", Method: "post", Summary: "Execute a read-only GraphQL query (current-version alias)", OperationID: "executeGraphQLAlias"},
		{Path: "/cli/binaries/{name}", Method: "get", Summary: "Download a CLI binary", OperationID: "downloadCLIBinary"},
		{Path: "/cli/binaries/{name}", Method: "head", Summary: "Check CLI binary metadata", OperationID: "headCLIBinary"},
		{Path: "/api/{api_version}/server/healthz", Method: "get", Summary: "Health status", OperationID: "getHealth"},
		{Path: "/api/{api_version}/server/swagger", Method: "get", Summary: "OpenAPI specification", OperationID: "getOpenAPI"},
		{Path: "/api/{api_version}/server/graphql", Method: "post", Summary: "Execute a read-only GraphQL query", OperationID: "executeGraphQL", Example: map[string]any{"query": "{ links(limit: 10, offset: 0) { shortCode shortURL destinationURL } }"}},
		{Path: "/api/{api_version}/server/preferences", Method: "get", Summary: "Read preferences", OperationID: "getPreferences"},
		{Path: "/api/{api_version}/server/preferences", Method: "post", Summary: "Update preferences", OperationID: "updatePreferences", Example: map[string]any{"theme": "dark", "lang": "en"}},
		{Path: "/api/{api_version}/server/preferences/export", Method: "get", Summary: "Export preferences", OperationID: "exportPreferences"},
		{Path: "/api/{api_version}/server/preferences/import", Method: "get", Summary: "Import preferences", OperationID: "importPreferences"},
		{Path: "/api/{api_version}/server/auth/check", Method: "get", Summary: "Check API token authorization", OperationID: "checkAuthorization"},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "get", Summary: "List API tokens", OperationID: "listTokens", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "post", Summary: "Create API token", OperationID: "createToken", Auth: true, Example: map[string]any{"name": "automation"}},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/reveal", Method: "post", Summary: "Reveal primary token", OperationID: "revealPrimaryToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/rotate", Method: "post", Summary: "Rotate primary token", OperationID: "rotatePrimaryToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/reveal", Method: "post", Summary: "Reveal linked token", OperationID: "revealLinkedToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/rotate", Method: "post", Summary: "Rotate linked token", OperationID: "rotateLinkedToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "patch", Summary: "Update API token", OperationID: "updateToken", Auth: true},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "delete", Summary: "Revoke API token", OperationID: "revokeToken", Auth: true},
		{Path: "/api/{api_version}/server/security/policy", Method: "get", Summary: "Get security policy", OperationID: "getSecurityPolicy"},
		{Path: "/api/{api_version}/server/dpo", Method: "get", Summary: "Get data protection contact", OperationID: "getDPO"},
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
		{Path: "/api/{api_version}/server/reports/csp", Method: "post", Summary: "Receive CSP reports", OperationID: "reportCSP", Example: map[string]any{"csp-report": map[string]any{"document-uri": "https://example.com/", "violated-directive": "script-src"}}},
		{Path: "/api/{api_version}/server/reports/nel", Method: "post", Summary: "Receive NEL reports", OperationID: "reportNEL", Example: map[string]any{"type": "network-error", "url": "https://example.com/"}},
		{Path: "/api/{api_version}/server/reports/default", Method: "post", Summary: "Receive security reports", OperationID: "reportDefault", Example: map[string]any{"type": "csp-violation", "url": "https://example.com/"}},
		{Path: "/api/{api_version}/links", Method: "get", Summary: "List links", OperationID: "listLinks"},
		{Path: "/api/{api_version}/links", Method: "post", Summary: "Create a link", OperationID: "createLink", Example: map[string]any{"url": "https://example.com/article", "slug": "article"}},
		{Path: "/api/{api_version}/links/{slug}", Method: "get", Summary: "Get a link", OperationID: "getLink"},
		{Path: "/api/{api_version}/links/{slug}", Method: "patch", Summary: "Update a link", OperationID: "updateLink", Auth: true, Example: map[string]any{"url": "https://example.com/new-article"}},
		{Path: "/api/{api_version}/links/{slug}", Method: "delete", Summary: "Delete a link", OperationID: "deleteLink", Auth: true},
		{Path: "/api/{api_version}/links/{slug}/stats", Method: "get", Summary: "Get link statistics", OperationID: "getLinkStats"},
	}
}
