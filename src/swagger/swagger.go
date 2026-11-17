// Package swagger builds the OpenAPI document from the API route contract.
package swagger

import (
	"encoding/json"
	"net/http"
	"strings"
)

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
	response := map[string]any{"description": "Successful response", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "additionalProperties": true}}}}
	errorResponse := map[string]any{"description": "Request failed", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/APIError"}}}}
	paths := map[string]map[string]operation{}
	for _, route := range RouteAnnotations() {
		responses := map[string]any{"200": response, "400": errorResponse, "401": errorResponse, "403": errorResponse, "404": errorResponse, "500": errorResponse}
		if route.Method == "post" || route.Method == "patch" || route.Method == "put" {
			responses["201"] = response
		}
		if route.Method == "delete" {
			responses["204"] = map[string]any{"description": "Deleted successfully"}
		}
		op := operation{Summary: route.Summary, OperationID: route.OperationID, Tags: []string{operationTag(route.Path)}, Responses: responses}
		for _, part := range strings.Split(route.Path, "/") {
			if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
				name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
				op.Parameters = append(op.Parameters, map[string]any{"name": name, "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
			}
		}
		if route.Method == "post" || route.Method == "patch" || route.Method == "put" {
			media := map[string]any{"schema": map[string]any{"type": "object", "additionalProperties": true}}
			if route.Example != nil {
				media["example"] = route.Example
			}
			op.RequestBody = map[string]any{"required": true, "content": map[string]any{"application/json": media}}
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
				"APIError": map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{"code": map[string]string{"type": "string"}, "message": map[string]string{"type": "string"}, "details": map[string]any{"type": "object", "additionalProperties": true}}},
			},
		},
	}
	return json.Marshal(doc)
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
		body, err := Document(serverURL(r))
		if err != nil {
			http.Error(w, "unable to generate OpenAPI document", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	})
}
