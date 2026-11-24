package graphql

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	graphqlgo "github.com/graphql-go/graphql"
)

//go:embed schema.generated.json
var embeddedSchemaDocument []byte

const schemaIntrospectionQuery = `{
	__schema {
		queryType { name }
		types {
			kind name description
			fields {
				name description
				args { name description defaultValue type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } }
				type { kind name ofType { kind name ofType { kind name ofType { kind name } } } }
			}
			inputFields { name description defaultValue type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } }
			enumValues { name description }
			interfaces { kind name }
			possibleTypes { kind name }
		}
		directives {
			name description locations
			args { name description defaultValue type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } }
		}
	}
}`

// QueryDocumentation describes one field from the generated GraphQL query type.
type QueryDocumentation struct {
	Name    string
	Summary string
	Args    []string
}

type schemaTypeRef struct {
	Kind   string         `json:"kind"`
	Name   string         `json:"name"`
	OfType *schemaTypeRef `json:"ofType"`
}

type schemaField struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Args        []struct {
		Name string        `json:"name"`
		Type schemaTypeRef `json:"type"`
	} `json:"args"`
}

type schemaIntrospection struct {
	Data struct {
		Schema struct {
			QueryType struct {
				Name string `json:"name"`
			} `json:"queryType"`
			Types []struct {
				Name   string        `json:"name"`
				Fields []schemaField `json:"fields"`
			} `json:"types"`
		} `json:"__schema"`
	} `json:"data"`
}

// IntrospectionDocument generates a deterministic JSON schema from the executable API.
func IntrospectionDocument(languages ...string) ([]byte, error) {
	schema, err := Schema(languages...)
	if err != nil {
		return nil, err
	}
	result := graphqlgo.Do(graphqlgo.Params{Schema: schema, RequestString: schemaIntrospectionQuery})
	if len(result.Errors) != 0 {
		return nil, fmt.Errorf("GraphQL schema introspection failed: %s", result.Errors[0].Message)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, err
	}
	encoded, err = json.MarshalIndent(sortNamedArrays(value), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// SchemaDocument returns the build-generated GraphQL introspection document embedded in the binary.
func SchemaDocument(languages ...string) []byte {
	if len(languages) > 0 && languages[0] != "en" {
		if document, err := IntrospectionDocument(languages[0]); err == nil {
			return document
		}
	}
	return append([]byte(nil), embeddedSchemaDocument...)
}

// DocumentQueries reads the query inventory from the embedded schema document.
func DocumentQueries(languages ...string) ([]QueryDocumentation, error) {
	data := embeddedSchemaDocument
	if len(languages) > 0 && languages[0] != "en" {
		var err error
		data, err = IntrospectionDocument(languages[0])
		if err != nil {
			return nil, err
		}
	}
	var document schemaIntrospection
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode embedded GraphQL schema: %w", err)
	}
	queryType := document.Data.Schema.QueryType.Name
	for _, object := range document.Data.Schema.Types {
		if object.Name != queryType {
			continue
		}
		queries := make([]QueryDocumentation, 0, len(object.Fields))
		for _, field := range object.Fields {
			query := QueryDocumentation{Name: field.Name, Summary: field.Description}
			for _, arg := range field.Args {
				query.Args = append(query.Args, arg.Name+": "+schemaTypeName(arg.Type))
			}
			sort.Strings(query.Args)
			queries = append(queries, query)
		}
		sort.Slice(queries, func(i, j int) bool { return queries[i].Name < queries[j].Name })
		return queries, nil
	}
	return nil, fmt.Errorf("embedded GraphQL schema has no query type %q", queryType)
}

func schemaTypeName(ref schemaTypeRef) string {
	switch ref.Kind {
	case "NON_NULL":
		if ref.OfType != nil {
			return schemaTypeName(*ref.OfType) + "!"
		}
	case "LIST":
		if ref.OfType != nil {
			return "[" + schemaTypeName(*ref.OfType) + "]"
		}
	}
	return ref.Name
}

func sortNamedArrays(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = sortNamedArrays(item)
		}
	case []any:
		for i, item := range typed {
			typed[i] = sortNamedArrays(item)
		}
		if len(typed) > 1 {
			allNamed := true
			for _, item := range typed {
				object, ok := item.(map[string]any)
				if !ok {
					allNamed = false
					break
				}
				if _, ok := object["name"].(string); !ok {
					allNamed = false
					break
				}
			}
			if allNamed {
				sort.Slice(typed, func(i, j int) bool {
					return typed[i].(map[string]any)["name"].(string) < typed[j].(map[string]any)["name"].(string)
				})
			}
		}
	}
	return value
}
