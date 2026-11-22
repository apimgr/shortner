package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMainGeneratesOpenAPIAndGraphQLDocuments(t *testing.T) {
	root := t.TempDir()
	swaggerDir := filepath.Join(root, "swagger")
	graphqlDir := filepath.Join(root, "graphql")
	if err := os.MkdirAll(swaggerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(graphqlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	if err := os.Chdir(swaggerDir); err != nil {
		t.Fatal(err)
	}

	main()

	for _, path := range []string{
		filepath.Join(swaggerDir, "openapi.generated.json"),
		filepath.Join(graphqlDir, "schema.generated.json"),
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read generated document %s: %v", path, err)
		}
		var document map[string]any
		if err := json.Unmarshal(body, &document); err != nil {
			t.Errorf("generated document %s is invalid JSON: %v", path, err)
		}
	}
}
