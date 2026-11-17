package main

import (
	"fmt"
	"os"

	apiGraphql "github.com/apimgr/shortner/src/graphql"
	"github.com/apimgr/shortner/src/swagger"
)

func main() {
	openAPI, err := swagger.Document("/")
	if err != nil {
		fail("generate OpenAPI document", err)
	}
	graphQL, err := apiGraphql.IntrospectionDocument()
	if err != nil {
		fail("generate GraphQL schema", err)
	}
	if err := os.WriteFile("openapi.generated.json", openAPI, 0o644); err != nil {
		fail("write OpenAPI document", err)
	}
	if err := os.WriteFile("../graphql/schema.generated.json", graphQL, 0o644); err != nil {
		fail("write GraphQL schema", err)
	}
}

func fail(action string, err error) {
	fmt.Fprintf(os.Stderr, "api-spec-gen: %s: %v\n", action, err)
	os.Exit(1)
}
