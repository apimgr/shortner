package swagger

// Route is an API operation annotation consumed by the OpenAPI generator.
type Route struct{ Path, Method, Summary, OperationID string }

// RouteAnnotations mirrors the versioned API route registrations in
// src/httpserver/server.go and links.go.
func RouteAnnotations() []Route {
	return []Route{
		{Path: "/api/{api_version}/server/healthz", Method: "get", Summary: "Health status", OperationID: "getHealth"},
		{Path: "/api/{api_version}/server/swagger", Method: "get", Summary: "OpenAPI specification", OperationID: "getOpenAPI"},
		{Path: "/api/{api_version}/server/graphql", Method: "post", Summary: "Execute a read-only GraphQL query", OperationID: "executeGraphQL"},
		{Path: "/api/{api_version}/server/preferences", Method: "get", Summary: "Read preferences", OperationID: "getPreferences"},
		{Path: "/api/{api_version}/server/preferences", Method: "post", Summary: "Update preferences", OperationID: "updatePreferences"},
		{Path: "/api/{api_version}/server/preferences/export", Method: "get", Summary: "Export preferences", OperationID: "exportPreferences"},
		{Path: "/api/{api_version}/server/preferences/import", Method: "get", Summary: "Import preferences", OperationID: "importPreferences"},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "get", Summary: "List API tokens", OperationID: "listTokens"},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "post", Summary: "Create API token", OperationID: "createToken"},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/reveal", Method: "post", Summary: "Reveal primary token", OperationID: "revealPrimaryToken"},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/rotate", Method: "post", Summary: "Rotate primary token", OperationID: "rotatePrimaryToken"},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/reveal", Method: "post", Summary: "Reveal linked token", OperationID: "revealLinkedToken"},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/rotate", Method: "post", Summary: "Rotate linked token", OperationID: "rotateLinkedToken"},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "patch", Summary: "Update API token", OperationID: "updateToken"},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "delete", Summary: "Revoke API token", OperationID: "revokeToken"},
		{Path: "/api/{api_version}/server/security/policy", Method: "get", Summary: "Get security policy", OperationID: "getSecurityPolicy"},
		{Path: "/api/{api_version}/server/dpo", Method: "get", Summary: "Get data protection contact", OperationID: "getDPO"},
		{Path: "/api/{api_version}/server/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getMetrics"},
		{Path: "/api/{api_version}/server/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getPrometheusMetrics"},
		{Path: "/api/{api_version}/server/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getGrafanaMetrics"},
		{Path: "/api/{api_version}/server/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getLokiMetrics"},
		{Path: "/api/{api_version}/server/reports/csp", Method: "post", Summary: "Receive CSP reports", OperationID: "reportCSP"},
		{Path: "/api/{api_version}/server/reports/nel", Method: "post", Summary: "Receive NEL reports", OperationID: "reportNEL"},
		{Path: "/api/{api_version}/server/reports/default", Method: "post", Summary: "Receive security reports", OperationID: "reportDefault"},
		{Path: "/api/{api_version}/links", Method: "get", Summary: "List links", OperationID: "listLinks"},
		{Path: "/api/{api_version}/links", Method: "post", Summary: "Create a link", OperationID: "createLink"},
		{Path: "/api/{api_version}/links/{slug}", Method: "get", Summary: "Get a link", OperationID: "getLink"},
		{Path: "/api/{api_version}/links/{slug}", Method: "patch", Summary: "Update a link", OperationID: "updateLink"},
		{Path: "/api/{api_version}/links/{slug}", Method: "delete", Summary: "Delete a link", OperationID: "deleteLink"},
		{Path: "/api/{api_version}/links/{slug}/stats", Method: "get", Summary: "Get link statistics", OperationID: "getLinkStats"},
	}
}
