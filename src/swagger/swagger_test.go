package swagger

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestDocumentIncludesRegisteredAliasesAndContractDetails(t *testing.T) {
	body, err := Document("https://shortner.example")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths      map[string]map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas         map[string]any `json:"schemas"`
			SecuritySchemes map[string]any `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	for path, method := range map[string]string{
		"/api/healthz":                              "get",
		"/api/autodiscover":                         "get",
		"/api/{api_version}/server/auth/check":      "get",
		"/api/{api_version}/server/metrics/grafana": "get",
		"/api/{api_version}/links/{slug}":           "patch",
		"/cli/binaries/{name}":                      "get",
	} {
		if _, ok := document.Paths[path][method]; !ok {
			t.Errorf("OpenAPI document missing %s %s", method, path)
		}
	}
	if _, ok := document.Paths["/api/{api_version}/links/{slug}"]["patch"]["parameters"]; !ok {
		t.Error("path parameter is not documented")
	}
	if _, ok := document.Paths["/api/{api_version}/links"]["post"]["requestBody"]; !ok {
		t.Error("request body is not documented")
	}
	if _, ok := document.Paths["/api/{api_version}/server/preferences/tokens"]["get"]["security"]; !ok {
		t.Error("token authorization is not documented")
	}
	if _, ok := document.Components.Schemas["APIError"]; !ok {
		t.Error("API error schema is not documented")
	}
	if len(document.Components.SecuritySchemes) != 2 {
		t.Errorf("security schemes = %d, want bearer and API key", len(document.Components.SecuritySchemes))
	}
	if err := checkSchemaReferences(body); err != nil {
		t.Fatal(err)
	}
	links := document.Paths["/api/{api_version}/links"]
	createResponses := links["post"]["responses"].(map[string]any)
	if _, ok := createResponses["201"]; !ok {
		t.Fatal("link creation must document its actual 201 status")
	}
	if _, ok := createResponses["200"]; ok {
		t.Fatal("link creation must not document a 200 success status")
	}
	createBody := links["post"]["requestBody"].(map[string]any)
	createSchema := createBody["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if createSchema["$ref"] != "#/components/schemas/CreateLinkRequest" {
		t.Errorf("create schema ref = %v", createSchema["$ref"])
	}
	listParameters := links["get"]["parameters"].([]any)
	queryParameters := map[string]bool{}
	for _, parameter := range listParameters {
		item := parameter.(map[string]any)
		if item["in"] == "query" {
			queryParameters[item["name"].(string)] = true
		}
	}
	if !queryParameters["page"] || !queryParameters["limit"] {
		t.Errorf("list query parameters = %v, want page and limit", queryParameters)
	}
	patchResponses := document.Paths["/api/{api_version}/links/{slug}"]["patch"]["responses"].(map[string]any)
	if _, ok := patchResponses["201"]; ok {
		t.Fatal("link update must not document a 201 status")
	}
	patchSchema := patchResponses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if patchSchema["$ref"] != "#/components/schemas/UpdateLinkResponse" {
		t.Errorf("patch response schema ref = %v", patchSchema["$ref"])
	}
	reportResponses := document.Paths["/api/{api_version}/server/reports/csp"]["post"]["responses"].(map[string]any)
	if _, ok := reportResponses["204"]; !ok {
		t.Fatal("report endpoint must document its actual 204 status")
	}
	if _, ok := reportResponses["201"]; ok {
		t.Fatal("report endpoint must not document 201")
	}
}

func checkSchemaReferences(body []byte) error {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return err
	}
	var schemas map[string]any
	if document, ok := root.(map[string]any); ok {
		components, _ := document["components"].(map[string]any)
		schemas, _ = components["schemas"].(map[string]any)
	}
	var visit func(any) error
	visit = func(value any) error {
		switch item := value.(type) {
		case map[string]any:
			if ref, ok := item["$ref"].(string); ok {
				const prefix = "#/components/schemas/"
				if len(ref) < len(prefix) || ref[:len(prefix)] != prefix {
					return fmt.Errorf("unsupported schema reference %q", ref)
				}
				name := ref[len(prefix):]
				if _, ok := schemas[name]; !ok {
					return fmt.Errorf("schema reference %q is undefined", ref)
				}
			}
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range item {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(root)
}
