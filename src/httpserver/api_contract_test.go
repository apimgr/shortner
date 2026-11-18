package httpserver

import (
	"os"
	"regexp"
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
	for _, route := range swagger.RouteAnnotations() {
		if strings.HasPrefix(route.Path, "/api/") || strings.HasPrefix(route.Path, "/cli/") {
			annotated[strings.ToLower(route.Method)+" "+route.Path] = struct{}{}
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
	pattern := regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD)\s+(/api/[^\s\x60),;]+)`)
	contract := make(map[string]struct{})
	for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
		contract[strings.ToLower(match[1])+" "+strings.TrimSuffix(match[2], "/")] = struct{}{}
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
	}
}
