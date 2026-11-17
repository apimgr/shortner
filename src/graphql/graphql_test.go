package graphql

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testResolver struct {
	readPath string
}

func (testResolver) Link(string) (*Link, error)           { return nil, nil }
func (testResolver) Links(int, int) ([]Link, error)       { return nil, nil }
func (testResolver) LinkStats(string) (*LinkStats, error) { return nil, nil }
func (r *testResolver) APIRead(path string) (any, error) {
	r.readPath = path
	return map[string]any{"ok": true, "data": map[string]any{"status": "healthy"}}, nil
}

func TestSchemaMirrorsDocumentedReadRoutesOnly(t *testing.T) {
	schema, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	fields := schema.QueryType().Fields()
	for _, name := range []string{"getAPIHealth", "getHealth", "listLinks", "getLink", "getMetrics"} {
		if fields[name] == nil {
			t.Errorf("GraphQL query field %q is missing", name)
		}
	}
	for _, name := range []string{"createLink", "updateLink", "deleteLink", "importPreferences", "downloadCLIBinary"} {
		if fields[name] != nil {
			t.Errorf("GraphQL query field %q must not be exposed", name)
		}
	}
}

func TestHandlerRunsReadOnlyRESTRoute(t *testing.T) {
	resolver := &testResolver{}
	handler := Handler(func(*http.Request) Resolver { return resolver })
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"{ getHealth(api_version: \"v1\") }"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if resolver.readPath != "/api/v1/server/healthz" {
		t.Errorf("read path = %q, want /api/v1/server/healthz", resolver.readPath)
	}
	var result struct {
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if got := result.Data["getHealth"]["ok"]; got != true {
		t.Errorf("GraphQL REST result = %v, want ok=true", result.Data["getHealth"])
	}
}

func TestHandlerRejectsExcessiveQueryComplexity(t *testing.T) {
	fields := make([]string, maxQueryFields+1)
	for i := range fields {
		fields[i] = fmt.Sprintf("f%d: link(slug: \"abc\") { shortCode }", i)
	}
	query := "{" + strings.Join(fields, " ") + "}"
	if !exceedsQueryComplexity(query) {
		t.Fatal("exceedsQueryComplexity() = false, want true")
	}
	handler := Handler(func(*http.Request) Resolver { return &testResolver{} })
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":`+strconvQuote(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "complexity limit") {
		t.Errorf("response %q does not explain complexity limit", rec.Body.String())
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
