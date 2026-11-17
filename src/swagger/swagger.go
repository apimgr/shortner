// Package swagger builds the OpenAPI document from the API route contract.
package swagger

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed openapi.generated.json
var embeddedDocument []byte

type operation struct {
	Summary     string                `json:"summary"`
	OperationID string                `json:"operationId"`
	Tags        []string              `json:"tags,omitempty"`
	Parameters  []any                 `json:"parameters,omitempty"`
	RequestBody any                   `json:"requestBody,omitempty"`
	Security    []map[string][]string `json:"security,omitempty"`
	Responses   map[string]any        `json:"responses"`
}

// Document generates an OpenAPI document for the implemented REST API.
// The route table is deliberately the sole inventory used to build paths.
func Document(serverURL string) ([]byte, error) {
	errorResponse := map[string]any{"description": "Request failed", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/APIError"}}}}
	paths := map[string]map[string]operation{}
	for _, route := range RouteAnnotations() {
		status, contentType, schema := responseContract(route)
		responses := map[string]any{status: successResponse(contentType, schema), "400": errorResponse, "401": errorResponse, "403": errorResponse, "404": errorResponse, "500": errorResponse}
		if route.OperationID == "reportCSP" || route.OperationID == "reportNEL" || route.OperationID == "reportDefault" {
			responses = map[string]any{"204": map[string]any{"description": "Report accepted"}}
		}
		op := operation{Summary: route.Summary, OperationID: route.OperationID, Tags: []string{operationTag(route.Path)}, Responses: responses}
		for _, part := range strings.Split(route.Path, "/") {
			if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
				name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
				op.Parameters = append(op.Parameters, map[string]any{"name": name, "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
			}
		}
		if route.OperationID == "listLinks" {
			op.Parameters = append(op.Parameters,
				map[string]any{"name": "page", "in": "query", "required": false, "description": "1-based page number (default 1).", "schema": map[string]any{"type": "integer", "minimum": 1}},
				map[string]any{"name": "limit", "in": "query", "required": false, "description": "Results per page, from 1 to 250 (default 250).", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 250}},
			)
		}
		if route.OperationID == "importPreferences" {
			op.Parameters = append(op.Parameters,
				map[string]any{"name": "theme", "in": "query", "required": false, "schema": stringSchema()},
				map[string]any{"name": "lang", "in": "query", "required": false, "schema": stringSchema()},
				map[string]any{"name": "code", "in": "query", "required": false, "description": "Base64url encoded preference set.", "schema": stringSchema()},
			)
		}
		if requestSchema := requestSchema(route.OperationID); requestSchema != "" {
			contentTypes := route.ContentTypes
			if len(contentTypes) == 0 {
				contentTypes = []string{"application/json"}
			}
			content := make(map[string]any, len(contentTypes))
			for _, contentType := range contentTypes {
				media := map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + requestSchema}}
				if example, ok := route.Examples[contentType]; ok {
					media["example"] = example
				} else if route.Example != nil {
					media["example"] = route.Example
				}
				content[contentType] = media
			}
			op.RequestBody = map[string]any{"required": true, "content": content}
		}
		if route.Auth {
			op.Security = []map[string][]string{{"BearerAuth": {}}, {"APIKeyAuth": {}}}
		}
		if paths[route.Path] == nil {
			paths[route.Path] = map[string]operation{}
		}
		paths[route.Path][route.Method] = op
	}
	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]string{"title": "Shortner API", "version": "1.0.0", "description": "REST API for creating, resolving, and managing shortened links."},
		"servers": []map[string]string{{"url": serverURL}},
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"BearerAuth": map[string]string{"type": "http", "scheme": "bearer", "description": "Server or resource-owner API token."},
				"APIKeyAuth": map[string]string{"type": "apiKey", "in": "header", "name": "X-API-Key", "description": "API token; the server also accepts the documented token headers."},
			},
			"schemas": map[string]any{
				"APIError":                   map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{"code": map[string]string{"type": "string"}, "message": map[string]string{"type": "string"}, "details": map[string]any{"type": "object", "additionalProperties": true}}},
				"APIEnvelope":                objectSchema(map[string]any{"ok": map[string]string{"type": "boolean"}, "data": map[string]any{}, "error": map[string]string{"type": "string"}, "message": map[string]string{"type": "string"}}, "ok"),
				"Link":                       objectSchema(map[string]any{"short_code": stringSchema(), "short_url": stringSchema(), "destination_url": stringSchema(), "created_at": dateTimeSchema(), "expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "click_count": map[string]string{"type": "integer", "format": "int64"}}, "short_code", "short_url", "destination_url", "created_at", "click_count"),
				"CreateLinkData":             objectSchema(map[string]any{"short_code": stringSchema(), "short_url": stringSchema(), "destination_url": stringSchema(), "created_at": dateTimeSchema(), "expires_at": map[string]any{"type": "string", "format": "date-time", "nullable": true}, "click_count": map[string]string{"type": "integer", "format": "int64"}, "owner_token": stringSchema()}, "short_code", "short_url", "destination_url", "created_at", "click_count"),
				"LinkList":                   objectSchema(map[string]any{"data": map[string]any{"type": "array", "items": refSchema("Link")}, "pagination": refSchema("Pagination")}, "data", "pagination"),
				"Pagination":                 objectSchema(map[string]any{"page": integerSchema(), "limit": integerSchema(), "total": map[string]string{"type": "integer", "format": "int64"}, "pages": integerSchema()}, "page", "limit", "total", "pages"),
				"LinkStats":                  objectSchema(map[string]any{"short_code": stringSchema(), "total_clicks": map[string]string{"type": "integer", "format": "int64"}, "referrers": map[string]any{"type": "object", "additionalProperties": integerSchema()}, "time_series": map[string]any{"type": "array", "items": objectSchema(map[string]any{"date": stringSchema(), "count": integerSchema()}, "date", "count")}, "recent": map[string]any{"type": "array", "items": objectSchema(map[string]any{"timestamp": dateTimeSchema(), "ip": stringSchema(), "referrer": stringSchema(), "country": stringSchema(), "region": stringSchema()}, "timestamp", "ip")}}, "short_code", "total_clicks", "referrers", "time_series", "recent"),
				"LinkStatsResponse":          envelopeSchema(refSchema("LinkStats")),
				"CreateLinkRequest":          objectSchema(map[string]any{"url": map[string]string{"type": "string", "format": "uri"}, "slug": stringSchema(), "expires_at": dateTimeSchema()}, "url"),
				"UpdateLinkRequest":          objectSchema(map[string]any{"url": map[string]string{"type": "string", "format": "uri"}, "expires_at": map[string]string{"type": "string", "format": "date-time", "description": "An empty string clears the expiration."}}),
				"CreateLinkResponse":         envelopeSchema(refSchema("CreateLinkData")),
				"GetLinkResponse":            envelopeSchema(refSchema("Link")),
				"UpdateLinkResponse":         envelopeSchema(objectSchema(map[string]any{"id": stringSchema(), "message": stringSchema(), "link": refSchema("Link")}, "id", "message", "link")),
				"DeleteLinkResponse":         envelopeSchema(objectSchema(map[string]any{"id": stringSchema(), "message": stringSchema()}, "id", "message")),
				"Health":                     objectSchema(map[string]any{"project": objectSchema(map[string]any{"name": stringSchema(), "tagline": stringSchema(), "description": stringSchema()}, "name", "tagline", "description"), "status": stringSchema(), "pending_restart": map[string]string{"type": "boolean"}, "restart_reason": map[string]any{"type": "array", "items": stringSchema()}, "version": stringSchema(), "go_version": stringSchema(), "build": objectSchema(map[string]any{"commit": stringSchema(), "date": dateTimeSchema()}, "commit", "date"), "uptime": stringSchema(), "mode": stringSchema(), "timestamp": dateTimeSchema(), "features": map[string]any{"type": "object", "properties": map[string]any{"tor": map[string]any{"type": "object"}, "i2p": map[string]any{"type": "object"}, "geoip": map[string]string{"type": "boolean"}}}, "checks": map[string]any{"type": "object", "additionalProperties": stringSchema()}, "stats": objectSchema(map[string]any{"requests_total": map[string]string{"type": "integer", "format": "int64"}, "requests_24h": map[string]string{"type": "integer", "format": "int64"}, "active_connections": integerSchema()}, "requests_total", "requests_24h", "active_connections")}, "project", "status", "version", "go_version", "build", "uptime", "mode", "timestamp", "features", "checks", "stats"),
				"CLIAutodiscover":            objectSchema(map[string]any{"project_name": stringSchema(), "server_version": stringSchema(), "cli_versions": map[string]any{"type": "object", "additionalProperties": refSchema("CLIBinaryVersion")}, "cli_min_version": stringSchema()}, "project_name", "server_version", "cli_versions", "cli_min_version"),
				"CLIBinaryVersion":           objectSchema(map[string]any{"version": stringSchema(), "sha256": map[string]string{"type": "string", "pattern": "^[a-f0-9]{64}$"}}, "version", "sha256"),
				"PreferenceSnapshot":         objectSchema(map[string]any{"theme": stringSchema(), "lang": stringSchema(), "prefs": map[string]any{"type": "object", "additionalProperties": stringSchema()}, "url": stringSchema(), "code": stringSchema()}, "theme", "lang", "prefs"),
				"PreferenceSnapshotResponse": envelopeSchema(refSchema("PreferenceSnapshot")),
				"PreferencesRequest":         objectSchema(map[string]any{"theme": stringSchema(), "lang": stringSchema(), "prefs": map[string]any{"type": "object", "additionalProperties": stringSchema()}}),
				"TokenCollection":            objectSchema(map[string]any{"tokens": map[string]any{"type": "array", "items": refSchema("TokenCard")}}, "tokens"),
				"TokenCard":                  objectSchema(map[string]any{"id": map[string]string{"type": "integer", "format": "int64"}, "name": stringSchema(), "masked": stringSchema(), "created": dateTimeSchema(), "last_used": stringSchema(), "expires": stringSchema(), "status": stringSchema(), "revealed": stringSchema()}, "id", "name", "masked", "created", "last_used", "expires", "status"),
				"TokenMutation":              objectSchema(map[string]any{"token": stringSchema(), "data": refSchema("TokenCard"), "revoked": map[string]string{"type": "boolean"}, "updated": map[string]string{"type": "boolean"}}),
				"PreferenceUpdate":           objectSchema(map[string]any{"theme": stringSchema(), "lang": stringSchema(), "prefs": map[string]any{"type": "object", "additionalProperties": stringSchema()}, "applied": map[string]any{"type": "array", "items": stringSchema()}, "ignored": map[string]any{"type": "array", "items": stringSchema()}}, "theme", "lang", "prefs", "applied", "ignored"),
				"PreferenceUpdateResponse":   envelopeSchema(refSchema("PreferenceUpdate")),
				"PreferenceImportResponse":   envelopeSchema(objectSchema(map[string]any{"theme": stringSchema(), "lang": stringSchema(), "prefs": map[string]any{"type": "object", "additionalProperties": stringSchema()}, "applied": map[string]any{"type": "array", "items": stringSchema()}, "ignored": map[string]any{"type": "array", "items": stringSchema()}}, "theme", "lang", "prefs", "applied", "ignored")),
				"AuthorizationResponse":      envelopeSchema(objectSchema(map[string]any{"kind": stringSchema()}, "kind")),
				"TokenCollectionResponse":    envelopeSchema(refSchema("TokenCollection")),
				"TokenMutationResponse":      envelopeSchema(refSchema("TokenMutation")),
				"TokenValueResponse":         envelopeSchema(objectSchema(map[string]any{"token": stringSchema()}, "token")),
				"TokenStatusResponse":        envelopeSchema(objectSchema(map[string]any{"revoked": map[string]string{"type": "boolean"}, "updated": map[string]string{"type": "boolean"}})),
				"SecurityPolicyResponse":     envelopeSchema(objectSchema(map[string]any{"policy": stringSchema()}, "policy")),
				"DPOResponse":                envelopeSchema(objectSchema(map[string]any{"name": stringSchema(), "email": stringSchema(), "privacy_url": map[string]string{"type": "string", "format": "uri"}}, "name", "email", "privacy_url")),
				"GraphQLRequest":             objectSchema(map[string]any{"query": stringSchema(), "operationName": stringSchema(), "variables": map[string]any{"type": "object", "additionalProperties": true}}, "query"),
				"GraphQLResponse":            objectSchema(map[string]any{"data": map[string]any{"type": "object", "additionalProperties": true}, "errors": map[string]any{"type": "array", "items": objectSchema(map[string]any{"message": stringSchema(), "locations": map[string]any{"type": "array", "items": objectSchema(map[string]any{"line": integerSchema(), "column": integerSchema()}, "line", "column")}, "path": map[string]any{"type": "array", "items": map[string]any{}}}, "message")}}, "data"),
				"TokenCreateRequest":         objectSchema(map[string]any{"name": stringSchema(), "expiry": map[string]any{"type": "string", "enum": []string{"never", "7d", "30d", "90d", "1y", "custom"}}, "expires_on": map[string]any{"type": "string", "format": "date", "description": "Required when expiry is custom."}}),
				"TokenUpdateRequest":         objectSchema(map[string]any{"name": stringSchema(), "expiry": map[string]any{"type": "string", "enum": []string{"never", "7d", "30d", "90d", "1y", "custom"}}, "expires_on": map[string]any{"type": "string", "format": "date", "description": "Required when expiry is custom."}}),
				"OpenAPIDocument":            objectSchema(map[string]any{"openapi": stringSchema(), "info": objectSchema(map[string]any{"title": stringSchema(), "version": stringSchema(), "description": stringSchema()}, "title", "version"), "servers": map[string]any{"type": "array", "items": objectSchema(map[string]any{"url": stringSchema()}, "url")}, "paths": map[string]any{"type": "object", "additionalProperties": true}, "components": map[string]any{"type": "object", "additionalProperties": true}}, "openapi", "info", "servers", "paths"),
				"ReportPayload":              map[string]any{"oneOf": []any{objectSchema(map[string]any{"csp-report": objectSchema(map[string]any{"document-uri": stringSchema(), "violated-directive": stringSchema(), "effective-directive": stringSchema(), "blocked-uri": stringSchema(), "disposition": stringSchema(), "status-code": integerSchema()})}), map[string]any{"type": "array", "items": objectSchema(map[string]any{"type": stringSchema(), "url": stringSchema(), "body": map[string]any{"type": "object", "nullable": true, "additionalProperties": true}}, "type", "url")}}},
			},
		},
	}
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func responseContract(route Route) (status, contentType, schema string) {
	status, contentType, schema = "200", "application/json", "APIEnvelope"
	switch route.OperationID {
	case "getHealth", "getAPIHealth", "getRootHealth":
		schema = "Health"
	case "getCLIAutodiscover":
		schema = "CLIAutodiscover"
	case "headCLIAutodiscover", "headCLIBinary":
		contentType, schema = "", ""
	case "downloadCLIBinary":
		contentType, schema = "application/octet-stream", ""
	case "getOpenAPI", "getOpenAPIAlias":
		schema = "OpenAPIDocument"
	case "listLinks":
		schema = "LinkList"
	case "updateLink":
		schema = "UpdateLinkResponse"
	case "getLink":
		schema = "GetLinkResponse"
	case "createLink":
		status, schema = "201", "CreateLinkResponse"
	case "getLinkStats":
		schema = "LinkStatsResponse"
	case "deleteLink":
		schema = "DeleteLinkResponse"
	case "getSecurityPolicy":
		schema = "SecurityPolicyResponse"
	case "getDPO":
		schema = "DPOResponse"
	case "getPreferences":
		schema = "PreferenceSnapshotResponse"
	case "updatePreferences":
		schema = "PreferenceUpdateResponse"
	case "exportPreferences":
		schema = "PreferenceSnapshotResponse"
	case "importPreferences":
		schema = "PreferenceImportResponse"
	case "checkAuthorization":
		schema = "AuthorizationResponse"
	case "listTokens":
		schema = "TokenCollectionResponse"
	case "createToken":
		schema = "TokenMutationResponse"
	case "revealPrimaryToken", "rotatePrimaryToken", "revealLinkedToken", "rotateLinkedToken":
		schema = "TokenValueResponse"
	case "updateToken", "revokeToken":
		schema = "TokenStatusResponse"
	case "getMetrics", "getPrometheusMetrics", "getGrafanaMetrics", "getLokiMetrics", "getServerMetrics", "getServerPrometheusMetrics", "getServerGrafanaMetrics", "getServerLokiMetrics", "getAPIMetrics", "getAPIPrometheusMetrics", "getAPIGrafanaMetrics", "getAPILokiMetrics", "getRootMetrics", "getRootPrometheusMetrics", "getRootGrafanaMetrics", "getRootLokiMetrics":
		contentType, schema = "text/plain", ""
	case "executeGraphQL", "executeGraphQLAlias":
		schema = "GraphQLResponse"
	}
	return
}

func envelopeSchema(data any) map[string]any {
	return objectSchema(map[string]any{"ok": map[string]string{"type": "boolean"}, "data": data, "error": stringSchema(), "message": stringSchema()}, "ok")
}

func requestSchema(operationID string) string {
	switch operationID {
	case "createLink":
		return "CreateLinkRequest"
	case "updateLink":
		return "UpdateLinkRequest"
	case "updatePreferences":
		return "PreferencesRequest"
	case "executeGraphQL", "executeGraphQLAlias":
		return "GraphQLRequest"
	case "createToken":
		return "TokenCreateRequest"
	case "updateToken":
		return "TokenUpdateRequest"
	case "reportCSP", "reportNEL", "reportDefault":
		return "ReportPayload"
	default:
		return ""
	}
}

func successResponse(contentType, schema string) map[string]any {
	response := map[string]any{"description": "Successful response"}
	if contentType == "" {
		return response
	}
	media := map[string]any{}
	if schema != "" {
		media["schema"] = refSchema(schema)
	}
	response["content"] = map[string]any{contentType: media}
	return response
}

func refSchema(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}
func stringSchema() map[string]string { return map[string]string{"type": "string"} }
func integerSchema() map[string]string {
	return map[string]string{"type": "integer", "format": "int32"}
}
func dateTimeSchema() map[string]string {
	return map[string]string{"type": "string", "format": "date-time"}
}
func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func operationTag(path string) string {
	if strings.Contains(path, "/links") {
		return "Links"
	}
	if strings.Contains(path, "/metrics") {
		return "Metrics"
	}
	if strings.Contains(path, "/preferences/tokens") {
		return "Tokens"
	}
	if strings.Contains(path, "/preferences") {
		return "Preferences"
	}
	if strings.Contains(path, "autodiscover") || strings.Contains(path, "/cli/binaries/") {
		return "CLI Updates"
	}
	if strings.Contains(path, "/reports/") {
		return "Security Reports"
	}
	return "Server"
}

// Handler serves the generated JSON specification directly.
func Handler(serverURL func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var document map[string]any
		if err := json.Unmarshal(embeddedDocument, &document); err != nil {
			http.Error(w, "OpenAPI document unavailable", http.StatusInternalServerError)
			return
		}
		document["servers"] = []any{map[string]string{"url": serverURL(r)}}
		body, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			http.Error(w, "unable to encode OpenAPI document", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(append(body, '\n'))
	})
}
