// Package swagger builds the OpenAPI document from the API route contract.
package swagger

import (
	"encoding/json"
	"net/http"
)

type operation struct {
	Summary     string         `json:"summary"`
	OperationID string         `json:"operationId"`
	Responses   map[string]any `json:"responses"`
}

// Document generates an OpenAPI document for the implemented REST API.
// The route table is deliberately the sole inventory used to build paths.
func Document(serverURL string) ([]byte, error) {
	response := map[string]any{"description": "Successful response"}
	paths := map[string]map[string]operation{}
	for _, route := range RouteAnnotations() {
		paths[route.Path] = map[string]operation{route.Method: {Summary: route.Summary, OperationID: route.OperationID, Responses: map[string]any{"200": response, "400": map[string]any{"description": "Invalid request"}}}}
	}
	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]string{"title": "Shortner API", "version": "1.0.0", "description": "REST API for creating, resolving, and managing shortened links."},
		"servers": []map[string]string{{"url": serverURL}},
		"paths":   paths,
	}
	return json.Marshal(doc)
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
