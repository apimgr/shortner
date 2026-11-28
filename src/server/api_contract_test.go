package server

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/swagger"
)

func TestRegisteredAPIRoutesMatchGeneratedContract(t *testing.T) {
	cfg := config.Default("unused")
	server := New(Options{Config: cfg, Metrics: metrics.New(cfg.Server.Metrics)})
	registered := make(map[string]struct{}, len(server.apiRoutes))
	for _, route := range server.apiRoutes {
		registered[route.Method+" "+strings.TrimSuffix(route.Path, "/")] = struct{}{}
	}
	annotated := make(map[string]struct{})
	registeredSet := registeredRouteSet(t)
	for _, route := range swagger.RouteAnnotations() {
		key := strings.ToLower(route.Method) + " " + route.Path
		if _, registered := registeredSet[key]; registered &&
			(isPublicAPIRoute(route.Path) || route.Path == "/{slug}" || route.Path == "/{slug}/stats") {
			annotated[key] = struct{}{}
		}
	}
	for route := range registered {
		if _, ok := annotated[route]; !ok {
			t.Errorf("registered API route %q is missing from generated API annotations", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("generated API annotation %q has no router registration", route)
		}
	}
}

func TestIDEAAPIRoutesExistInGeneratedContract(t *testing.T) {
	data, err := os.ReadFile("../../IDEA.md")
	if err != nil {
		t.Fatal(err)
	}
	contract, err := routesFromDocument(string(data), true)
	if err != nil {
		t.Fatal(err)
	}
	annotated := make(map[string]struct{})
	for _, route := range swagger.RouteAnnotations() {
		annotated[strings.ToLower(route.Method)+" "+route.Path] = struct{}{}
	}
	if len(contract) == 0 {
		t.Fatal("IDEA.md defines no API route contracts")
	}
	for route := range contract {
		if _, ok := annotated[route]; !ok {
			t.Errorf("IDEA.md API contract %q is missing from generated API annotations", route)
		}
		if _, ok := registeredRouteSet(t)[route]; !ok {
			t.Errorf("IDEA.md API contract %q has no router registration", route)
		}
	}
}

func TestTemplateAndIDEARoutesMatchRouterAndOpenAPI(t *testing.T) {
	aiData, err := os.ReadFile("../../AI.md")
	if err != nil {
		t.Fatal(err)
	}
	ideaData, err := os.ReadFile("../../IDEA.md")
	if err != nil {
		t.Fatal(err)
	}
	templateRoutes, err := routesFromDocument(string(aiData), false)
	if err != nil {
		t.Fatal(err)
	}
	projectRoutes, err := routesFromDocument(string(ideaData), true)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{}, len(templateRoutes)+len(projectRoutes))
	for route := range templateRoutes {
		expected[route] = struct{}{}
	}
	for route := range projectRoutes {
		expected[route] = struct{}{}
	}
	registered := registeredRouteSet(t)
	annotated := make(map[string]struct{})
	for _, route := range swagger.RouteAnnotations() {
		key := strings.ToLower(route.Method) + " " + route.Path
		if _, exists := registered[key]; exists &&
			(isPublicAPIRoute(route.Path) || route.Path == "/{slug}" || route.Path == "/{slug}/stats") {
			annotated[key] = struct{}{}
		}
	}
	for route := range expected {
		if _, ok := registered[route]; !ok {
			t.Errorf("contract route %q is missing from router registrations", route)
		}
		if _, ok := annotated[route]; !ok {
			t.Errorf("contract route %q is missing from generated OpenAPI annotations", route)
		}
	}
	for route := range annotated {
		if _, ok := registered[route]; !ok {
			t.Errorf("OpenAPI route %q has no router registration", route)
		}
	}
}

func registeredRouteSet(t *testing.T) map[string]struct{} {
	t.Helper()
	cfg := config.Default("unused")
	server := New(Options{Config: cfg, Metrics: metrics.New(cfg.Server.Metrics)})
	routes := make(map[string]struct{}, len(server.apiRoutes))
	for _, route := range server.apiRoutes {
		routes[strings.ToLower(route.Method)+" "+strings.TrimSuffix(route.Path, "/")] = struct{}{}
	}
	return routes
}

// routesFromDocument extracts explicit public route contracts from tables.
// AI.md includes many documentation/example tables, so only tables whose
// header names both a route/endpoint and method are considered.
func routesFromDocument(document string, includeNative bool) (map[string]struct{}, error) {
	lines := strings.Split(document, "\n")
	start, end := -1, len(lines)
	section := "### /server/ API Endpoints"
	if includeNative {
		section = "### API routes"
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == section {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("document has no %q section", section)
	}
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "### ") {
			end = i
			break
		}
	}
	routes := make(map[string]struct{})
	pathIndex, methodIndex := -1, -1
	for _, line := range lines[start:end] {
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			pathIndex, methodIndex = -1, -1
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(cells[0]), "---") {
			continue
		}
		headerPath, headerMethod := -1, -1
		for i := 1; i < len(cells); i++ {
			header := strings.ToLower(strings.TrimSpace(cells[i]))
			if header == "route" || header == "endpoint" || header == "alias" || header == "path" {
				headerPath = i
			}
			if header == "method" {
				headerMethod = i
			}
		}
		if headerPath >= 0 && headerMethod >= 0 {
			pathIndex, methodIndex = headerPath, headerMethod
			continue
		}
		if pathIndex < 0 {
			continue
		}
		if methodIndex < 0 {
			continue
		}
		if pathIndex >= len(cells) || methodIndex >= len(cells) {
			pathIndex, methodIndex = -1, -1
			continue
		}
		path := strings.Trim(strings.TrimSpace(cells[pathIndex]), "` ")
		if path == "" || strings.Contains(path, "{official_site}") || strings.Contains(path, "...") || strings.Contains(path, "…") {
			continue
		}
		if strings.Contains(path, "/cli/binaries/") {
			path = "/cli/binaries/{name}"
		}
		if strings.Contains(path, "/api/{api_version}/{resource}") || strings.Contains(path, "*") {
			continue
		}
		if !isPublicAPIRoute(path) && !includeNative {
			continue
		}
		if !isHTTPMethodCell(cells[methodIndex]) {
			continue
		}
		methods := strings.Split(strings.TrimSpace(cells[methodIndex]), ",")
		paths := []string{path}
		if strings.HasSuffix(path, "/server/reports/{name}") {
			paths = []string{
				strings.TrimSuffix(path, "{name}") + "csp",
				strings.TrimSuffix(path, "{name}") + "nel",
				strings.TrimSuffix(path, "{name}") + "default",
			}
		}
		if strings.Contains(path, "[/{service}]") {
			base := strings.TrimSuffix(path, "[/{service}]")
			paths = []string{base, base + "/prometheus", base + "/grafana", base + "/loki"}
		}
		for _, method := range methods {
			method = strings.ToLower(strings.Trim(method, " `"))
			for _, routePath := range paths {
				routes[method+" "+strings.TrimSuffix(routePath, "/")] = struct{}{}
			}
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("document contains no explicit public routes")
	}
	return routes, nil
}

func isPublicAPIRoute(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/cli/") ||
		path == "/healthz" || path == "/metrics" || strings.HasPrefix(path, "/metrics/") ||
		path == "/server/metrics" || strings.HasPrefix(path, "/server/metrics/")
}

func isHTTPMethodCell(cell string) bool {
	allowed := map[string]struct{}{"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}, "HEAD": {}, "OPTIONS": {}}
	for _, method := range strings.Split(strings.TrimSpace(cell), ",") {
		method = strings.ToUpper(strings.Trim(method, " `"))
		if _, ok := allowed[method]; !ok {
			return false
		}
	}
	return strings.TrimSpace(cell) != ""
}
