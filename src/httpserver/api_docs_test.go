package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
