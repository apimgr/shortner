package swagger

import (
	"strings"

	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/routecontract"
)

// Route is the shared route contract used by registration and generated API
// documentation surfaces.
type Route = routecontract.Route

// RouteAnnotations mirrors the versioned API route registrations in
// src/server/server.go and links.go.
func RouteAnnotations(languages ...string) []Route {
	return RouteAnnotationsForRegistry(RouteRegistry(), languages...)
}

// RouteAnnotationsForRegistry returns translated metadata from the supplied
// runtime registry. It does not rebuild a second route table.
func RouteAnnotationsForRegistry(registry *routecontract.Registry, languages ...string) []Route {
	if registry == nil {
		registry = RouteRegistry()
	}
	routes := registry.Snapshot()
	lang := "en"
	if len(languages) > 0 {
		lang = languages[0]
	}
	for i := range routes {
		routes[i].Method = strings.ToLower(routes[i].Method)
		category := summaryCategory(routes[i].OperationID)
		key := "swagger.summary." + category
		if i18n.Has(lang, key) {
			routes[i].Summary = i18n.Translate(lang, key)
		}
	}
	return routes
}

// RouteRegistry returns the shared metadata registry for the server's API
// operations. Runtime handlers are attached by the server during assembly.
func RouteRegistry() *routecontract.Registry {
	registry := routecontract.New()
	routes := []Route{
		{Path: "/healthz", Method: "get", Summary: "Health status", OperationID: "getRootHealth", Response: routecontract.ModelRef{Name: "Health", ContentType: "application/json"}},
		{Path: "/api/healthz", Method: "get", Summary: "Health status", OperationID: "getAPIHealth", Response: routecontract.ModelRef{Name: "Health", ContentType: "application/json"}},
		{Path: "/api/autodiscover", Method: "get", Summary: "Discover compatible CLI binaries and minimum version", OperationID: "getCLIAutodiscover", Response: routecontract.ModelRef{Name: "CLIAutodiscover", ContentType: "application/json"}},
		{Path: "/api/autodiscover", Method: "head", Summary: "Check CLI autodiscovery metadata", OperationID: "headCLIAutodiscover", Response: routecontract.ModelRef{ContentType: ""}},
		{Path: "/api/swagger", Method: "get", Summary: "OpenAPI specification (current-version alias)", OperationID: "getOpenAPIAlias", Response: routecontract.ModelRef{Name: "OpenAPIDocument", ContentType: "application/json"}},
		{Path: "/api/graphql", Method: "post", Summary: "Execute a read-only GraphQL query (current-version alias)", OperationID: "executeGraphQLAlias", Request: routecontract.ModelRef{Name: "GraphQLRequest", ContentType: "application/json"}, Response: routecontract.ModelRef{Name: "GraphQLResponse", ContentType: "application/json"}, Message: "OK", Example: map[string]any{"query": "{ links(limit: 10, offset: 0) { shortCode shortURL destinationURL } getAPIHealth }"}},
		{Path: "/cli/binaries/{name}", Method: "get", Summary: "Download a CLI binary", OperationID: "downloadCLIBinary", Response: routecontract.ModelRef{ContentType: "application/octet-stream"}},
		{Path: "/cli/binaries/{name}", Method: "head", Summary: "Check CLI binary metadata", OperationID: "headCLIBinary", Response: routecontract.ModelRef{ContentType: ""}},
		{Path: "/api/{api_version}/server/healthz", Method: "get", Summary: "Health status", OperationID: "getHealth", Response: routecontract.ModelRef{Name: "Health", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/about", Method: "get", Summary: "About page content", OperationID: "getAboutPage", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/privacy", Method: "get", Summary: "Privacy page content", OperationID: "getPrivacyPage", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/help", Method: "get", Summary: "Help page content", OperationID: "getHelpPage", Response: routecontract.ModelRef{Name: "HelpPageResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/help/mcp", Method: "get", Summary: "MCP help sections", OperationID: "getMCPHelp", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/terms", Method: "get", Summary: "Terms page content", OperationID: "getTermsPage", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/security", Method: "get", Summary: "Security disclosure information", OperationID: "getSecurityInfo", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/security/thanks", Method: "get", Summary: "Security acknowledgments", OperationID: "getSecurityThanks", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/mcp", Method: "post", Summary: "MCP Streamable HTTP endpoint", OperationID: "handleMCP", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}, Message: "OK"},
		{Path: "/api/mcp", Method: "post", Summary: "MCP Streamable HTTP endpoint alias", OperationID: "handleMCPAlias", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}, Message: "OK"},
		{Path: "/mcp", Method: "post", Summary: "MCP Streamable HTTP endpoint alias", OperationID: "handleRootMCP", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}, Message: "OK"},
		{Path: "/api/{api_version}/server/swagger", Method: "get", Summary: "OpenAPI specification", OperationID: "getOpenAPI", Response: routecontract.ModelRef{Name: "OpenAPIDocument", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/graphql", Method: "post", Summary: "Execute a read-only GraphQL query", OperationID: "executeGraphQL", Request: routecontract.ModelRef{Name: "GraphQLRequest", ContentType: "application/json"}, Response: routecontract.ModelRef{Name: "GraphQLResponse", ContentType: "application/json"}, Message: "OK", Example: map[string]any{"query": "{ links(limit: 10, offset: 0) { shortCode shortURL destinationURL } }"}},
		{Path: "/api/{api_version}/server/graphql", Method: "get", Summary: "Execute a read-only GraphQL query over GET", OperationID: "executeGraphQLGet", Response: routecontract.ModelRef{Name: "GraphQLResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/preferences", Method: "get", Summary: "Read preferences", OperationID: "getPreferences", Response: routecontract.ModelRef{Name: "PreferenceSnapshotResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/preferences", Method: "post", Summary: "Update preferences", OperationID: "updatePreferences", Request: routecontract.ModelRef{Name: "PreferencesRequest", ContentType: "application/json"}, Response: routecontract.ModelRef{Name: "PreferenceUpdateResponse", ContentType: "application/json"}, Message: "OK", Example: map[string]any{"theme": "dark", "lang": "en"}},
		{Path: "/api/{api_version}/server/preferences/sync", Method: "get", Summary: "Export preferences for cross-device sync", OperationID: "syncPreferences", Response: routecontract.ModelRef{Name: "PreferenceSnapshotResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/preferences/sync/import", Method: "get", Summary: "Import shared preferences", OperationID: "importSyncedPreferences", Response: routecontract.ModelRef{Name: "PreferenceImportResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/auth/check", Method: "get", Summary: "Check API token authorization", OperationID: "checkAuthorization", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "AuthorizationResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "get", Summary: "List API tokens", OperationID: "listTokens", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "TokenCollectionResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/preferences/tokens", Method: "post", Summary: "Create linked API token", OperationID: "createToken", Auth: routecontract.AuthToken, Request: routecontract.ModelRef{Name: "TokenCreateRequest", ContentType: "application/json"}, Response: routecontract.ModelRef{Name: "LinkedTokenValueResponse", Status: "201", ContentType: "application/json"}, Example: map[string]any{"name": "automation", "scope": "read-write", "expires": "30d"}},
		{Path: "/api/{api_version}/server/preferences/tokens/primary", Method: "post", Summary: "Issue a primary owner token", OperationID: "issuePrimaryToken", Response: routecontract.ModelRef{Name: "PrimaryTokenIssueResponse", Status: "201", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/reveal", Method: "post", Summary: "Reveal primary token", OperationID: "revealPrimaryToken", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "PrimaryTokenValueResponse", ContentType: "application/json"}, Message: "OK"},
		{Path: "/api/{api_version}/server/preferences/tokens/primary/rotate", Method: "post", Summary: "Rotate primary token", OperationID: "rotatePrimaryToken", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "PrimaryTokenValueResponse", ContentType: "application/json"}, Message: "OK"},
		{Path: "/api/{api_version}/server/preferences/tokens/primary", Method: "delete", Summary: "Revoke primary token and delete its owner", OperationID: "revokePrimaryToken", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "PrimaryTokenRevocationResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/reveal", Method: "post", Summary: "Reveal linked token", OperationID: "revealLinkedToken", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "LinkedTokenValueResponse", ContentType: "application/json"}, Message: "OK"},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}/rotate", Method: "post", Summary: "Rotate linked token", OperationID: "rotateLinkedToken", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "LinkedTokenValueResponse", ContentType: "application/json"}, Message: "OK"},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "patch", Summary: "Update API token", OperationID: "updateToken", Auth: routecontract.AuthToken, Request: routecontract.ModelRef{Name: "TokenUpdateRequest", ContentType: "application/json"}, Response: routecontract.ModelRef{Name: "TokenCardResponse", ContentType: "application/json"}, Example: map[string]any{"name": "automation", "expires": "30d"}},
		{Path: "/api/{api_version}/server/preferences/tokens/{id}", Method: "delete", Summary: "Revoke API token", OperationID: "revokeToken", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "RevokedTokenResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/security/policy", Method: "get", Summary: "Get security policy", OperationID: "getSecurityPolicy", Response: routecontract.ModelRef{Name: "SecurityPolicyResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/dpo", Method: "get", Summary: "Get data protection contact", OperationID: "getDPO", Response: routecontract.ModelRef{Name: "DPOResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/contact", Method: "get", Summary: "Describe the private report form", OperationID: "getContactForm", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/contact", Method: "post", Summary: "Submit or confirm a report", OperationID: "submitContactForm", Response: routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/api/{api_version}/server/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getPrometheusMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/api/{api_version}/server/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getGrafanaMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getLokiMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/server/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getServerMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/server/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getServerPrometheusMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/server/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getServerGrafanaMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/server/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getServerLokiMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/api/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getAPIMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/api/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getAPIPrometheusMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/api/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getAPIGrafanaMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/api/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getAPILokiMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/metrics", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getRootMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/metrics/prometheus", Method: "get", Summary: "Get Prometheus metrics", OperationID: "getRootPrometheusMetrics", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/metrics/grafana", Method: "get", Summary: "Get Grafana metrics", OperationID: "getRootGrafanaMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/metrics/loki", Method: "get", Summary: "Get Loki metrics", OperationID: "getRootLokiMetrics", Response: routecontract.ModelRef{ContentType: "application/json"}},
		{Path: "/api/{api_version}/server/reports/csp", Method: "post", Summary: "Receive CSP reports", OperationID: "reportCSP", Request: routecontract.ModelRef{Name: "ReportPayload"}, Response: routecontract.ModelRef{Status: "204", ContentType: ""}, ContentTypes: reportContentTypes(), Examples: map[string]any{"application/csp-report": map[string]any{"csp-report": map[string]any{"document-uri": "https://example.com/", "violated-directive": "script-src"}}, "application/reports+json": []any{map[string]any{"type": "csp-violation", "url": "https://example.com/", "body": map[string]any{"effectiveDirective": "script-src"}}}}},
		{Path: "/api/{api_version}/server/reports/nel", Method: "post", Summary: "Receive NEL reports", OperationID: "reportNEL", Request: routecontract.ModelRef{Name: "ReportPayload"}, Response: routecontract.ModelRef{Status: "204", ContentType: ""}, ContentTypes: reportContentTypes(), Examples: map[string]any{"application/reports+json": []any{map[string]any{"type": "network-error", "url": "https://example.com/", "body": map[string]any{"type": "tcp.reset"}}}}},
		{Path: "/api/{api_version}/server/reports/default", Method: "post", Summary: "Receive security reports", OperationID: "reportDefault", Request: routecontract.ModelRef{Name: "ReportPayload"}, Response: routecontract.ModelRef{Status: "204", ContentType: ""}, ContentTypes: reportContentTypes(), Examples: map[string]any{"application/reports+json": []any{map[string]any{"type": "csp-violation", "url": "https://example.com/", "body": map[string]any{"effectiveDirective": "script-src"}}}}},
		{Path: "/api/{api_version}/links", Method: "get", Summary: "List links", OperationID: "listLinks", Response: routecontract.ModelRef{Name: "LinkList", ContentType: "application/json"}},
		{Path: "/api/{api_version}/links", Method: "post", Summary: "Create a link", OperationID: "createLink", Request: routecontract.ModelRef{Name: "CreateLinkRequest", ContentType: "application/json"}, Response: routecontract.ModelRef{Name: "CreateLinkResponse", Status: "201", ContentType: "application/json"}, Primary: "short_url", Example: map[string]any{"url": "https://example.com/article", "slug": "article"}},
		{Path: "/api/{api_version}/links/{slug}", Method: "get", Summary: "Get a link", OperationID: "getLink", Response: routecontract.ModelRef{Name: "GetLinkResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/links/{slug}", Method: "patch", Summary: "Update a link", OperationID: "updateLink", Auth: routecontract.AuthToken, Request: routecontract.ModelRef{Name: "UpdateLinkRequest", ContentType: "application/json"}, Response: routecontract.ModelRef{Name: "UpdateLinkResponse", ContentType: "application/json"}, Example: map[string]any{"url": "https://example.com/new-article"}},
		{Path: "/api/{api_version}/links/{slug}", Method: "delete", Summary: "Delete a link", OperationID: "deleteLink", Auth: routecontract.AuthToken, Response: routecontract.ModelRef{Name: "DeleteLinkResponse", ContentType: "application/json"}},
		{Path: "/api/{api_version}/links/{slug}/stats", Method: "get", Summary: "Get link statistics", OperationID: "getLinkStats", Response: routecontract.ModelRef{Name: "LinkStatsResponse", ContentType: "application/json"}},
		{Path: "/{slug}", Method: "get", Summary: "Redirect to a short link", OperationID: "redirectShortLink", Response: routecontract.ModelRef{ContentType: "text/plain"}},
		{Path: "/{slug}/stats", Method: "get", Summary: "Read link statistics", OperationID: "getLinkStatsPage", Response: routecontract.ModelRef{Name: "LinkStatsResponse", ContentType: "application/json"}},
	}
	for i := range routes {
		if strings.Contains(routes[i].Path, "/metrics") || routes[i].Path == "/metrics" {
			routes[i].Internal = true
			routes[i].Auth = routecontract.AuthService
		}
		if routes[i].OperationID == "downloadCLIBinary" || routes[i].OperationID == "headCLIBinary" {
			routes[i].Binary = "application/octet-stream"
		}
		if routes[i].Response == nil {
			routes[i].Response = routecontract.ModelRef{Name: "APIEnvelope", ContentType: "application/json"}
		}
		routes[i].Listed = !routes[i].Internal && routes[i].Auth != routecontract.AuthOperator && routes[i].Auth != routecontract.AuthService
		if err := registry.Add(routes[i]); err != nil {
			panic(err)
		}
	}
	return registry
}

func summaryCategory(id string) string {
	switch id {
	case "getRootHealth", "getAPIHealth", "getHealth":
		return "health"
	case "getCLIAutodiscover", "headCLIAutodiscover":
		return "cli_discovery"
	case "downloadCLIBinary", "headCLIBinary":
		return "cli_download"
	case "getAboutPage":
		return "about"
	case "getPrivacyPage":
		return "privacy"
	case "getHelpPage":
		return "help"
	case "getMCPHelp":
		return "mcp_help"
	case "getTermsPage":
		return "terms"
	case "getSecurityInfo", "getSecurityThanks":
		return "security"
	case "handleMCP", "handleMCPAlias", "handleRootMCP":
		return "mcp"
	case "getOpenAPI", "getOpenAPIAlias":
		return "openapi"
	case "executeGraphQL", "executeGraphQLAlias", "executeGraphQLGet":
		return "graphql"
	case "getPreferences":
		return "preferences_read"
	case "updatePreferences":
		return "preferences_update"
	case "syncPreferences":
		return "preferences_sync"
	case "importSyncedPreferences":
		return "preferences_import"
	case "checkAuthorization":
		return "authorization"
	case "listTokens":
		return "tokens_list"
	case "createToken":
		return "tokens_create"
	case "issuePrimaryToken":
		return "tokens_issue"
	case "revealPrimaryToken", "revealLinkedToken":
		return "tokens_reveal"
	case "rotatePrimaryToken", "rotateLinkedToken":
		return "tokens_rotate"
	case "revokePrimaryToken", "revokeToken":
		return "tokens_revoke"
	case "updateToken":
		return "tokens_update"
	case "getSecurityPolicy":
		return "security_policy"
	case "getDPO":
		return "dpo"
	case "getContactForm":
		return "contact_read"
	case "submitContactForm":
		return "contact_submit"
	case "listLinks":
		return "links_list"
	case "createLink":
		return "links_create"
	case "getLink":
		return "links_get"
	case "updateLink":
		return "links_update"
	case "deleteLink":
		return "links_delete"
	case "getLinkStats", "getLinkStatsPage":
		return "links_stats"
	default:
		return "metrics"
	}
}

func reportContentTypes() []string {
	return []string{"application/csp-report", "application/reports+json"}
}

// IsReadPath reports whether path is a documented GET endpoint that is safe
// to expose through the query-only GraphQL API. The preferences import route
// is deliberately excluded because its GET handler writes preference cookies.
func IsReadPath(path string) bool {
	return IsReadPathInRegistry(RouteRegistry(), path)
}

// IsReadPathInRegistry reports whether path is a documented read-only API
// route in the supplied registry.
func IsReadPathInRegistry(registry *routecontract.Registry, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for _, route := range RouteAnnotationsForRegistry(registry) {
		if route.Method != "get" || route.Internal || route.OperationID == "importSyncedPreferences" || strings.HasPrefix(route.Path, "/cli/") {
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
