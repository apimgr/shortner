package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/swagger"
)

func TestGraphQLAPIReadUsesDocumentedReadRoute(t *testing.T) {
	apiRouter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/server/healthz" {
			t.Errorf("path = %s, want versioned health path", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header was not carried into the REST resolver")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q, want application/json", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	original := httptest.NewRequest(http.MethodPost, "/api/graphql", nil)
	original.Header.Set("Authorization", "Bearer test-token")
	result, err := graphQLAPIRead(apiRouter, original, "/api/v1/server/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := result.(map[string]any); !ok || got["ok"] != true {
		t.Errorf("result = %#v, want decoded JSON response", result)
	}
}

func TestGraphQLAPIReadRejectsUnsafePaths(t *testing.T) {
	for _, path := range []string{
		"https://evil.example/api/v1/server/healthz",
		"/api/v1/server/preferences/import?theme=dark",
		"/api/graphql",
		"/cli/binaries/shortner-cli-linux-amd64",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := graphQLAPIRead(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("unsafe path reached the API router")
			}), httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader("{}")), path)
			if err == nil {
				t.Fatal("graphQLAPIRead() error = nil, want rejection")
			}
		})
	}
}

func TestSwaggerPageListsAnnotatedOperationsWithoutJavaScript(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.cfg.Server.APIVersion = "v1"
	recorder := httptest.NewRecorder()
	fd.swaggerPage(recorder, htmlRequest(http.MethodGet, "/server/docs/swagger", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"GET /api/{api_version}/links",
		"POST /api/{api_version}/links",
		"GET /api/{api_version}/server/preferences/tokens",
		"GET /metrics",
		"data-api-version=\"v1\"",
		"/static/js/swagger-docs.js",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Swagger page does not include %q", want)
		}
	}
	for _, route := range swagger.RouteAnnotations() {
		want := strings.ToUpper(route.Method) + " " + route.Path
		if !strings.Contains(body, want) {
			t.Errorf("Swagger page operation inventory omits %q", want)
		}
	}
}

func TestGraphQLPageListsSchemaFieldsAndFormControls(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	recorder := httptest.NewRecorder()
	fd.graphqlPage(recorder, htmlRequest(http.MethodGet, "/server/docs/graphql", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"name=\"operationName\"",
		"name=\"variables\"",
		"id=\"graphql-token\"",
		"id=\"graphql-schema\"",
		"getAPIHealth",
		"linkStats",
		"REST-backed fields return JSON scalar values",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GraphQL page does not include %q", want)
		}
	}
}

func TestHelpPageAlwaysLinksToMCP(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	recorder := httptest.NewRecorder()
	fd.helpHandler(recorder, htmlRequest(http.MethodGet, "/server/help", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `href="/server/help/mcp"`) {
		t.Fatal("help page does not include the unconditional MCP help link")
	}
}

func TestOperatorContentSanitizesExpandedVariables(t *testing.T) {
	fd, _ := testFrontendDeps(t)
	fd.configDir = t.TempDir()
	htmlDir := filepath.Join(fd.configDir, "html")
	if err := os.Mkdir(htmlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(htmlDir, "footer.html"), []byte("<div>{project_name}</div>"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := fd.operatorHTML(htmlRequest(http.MethodGet, "/", nil), "footer", footerVariables{projectName: "<script>alert(1)</script>"})
	if strings.Contains(string(got), "<script") {
		t.Fatalf("expanded operator content contains an executable script: %s", got)
	}
}
