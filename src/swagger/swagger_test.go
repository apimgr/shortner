package swagger

import (
	"encoding/json"
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
}
