// Package routecontract defines the shared metadata contract for HTTP routes.
package routecontract

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Route describes one mounted HTTP operation and the metadata consumed by
// generated API surfaces.
type Route struct {
	Method      string
	Path        string
	OperationID string
	Summary     string
	Auth        string
	Listed      bool
	Internal    bool
	DebugOnly   bool
	Request     any
	Response    any
	Primary     string
	Message     string
	Binary      string
	Handler     http.HandlerFunc

	Example      map[string]any
	ContentTypes []string
	Examples     map[string]any
}

// ModelRef names a request or response model and carries the response
// behavior needed by API documentation and text clients. Fields lists the
// serialized JSON field order for responses whose model is represented by a
// named schema.
type ModelRef struct {
	Name        string         `json:"name,omitempty"`
	Fields      []string       `json:"fields,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Model       any            `json:"-"`
	Status      string         `json:"status,omitempty"`
	ContentType string         `json:"content_type,omitempty"`
	Envelope    bool           `json:"envelope,omitempty"`
}

// Auth values accepted by Route.Auth.
const (
	AuthNone     = "none"
	AuthOwner    = "owner"
	AuthOperator = "operator"
	AuthToken    = "token"
	AuthService  = "service"
)

// Registry is the validated, immutable-on-read set of route definitions.
// A Registry is safe for concurrent Add and Snapshot calls.
type Registry struct {
	mu           sync.RWMutex
	routes       []Route
	identities   map[string]struct{}
	operationIDs map[string]struct{}
}

// New returns an empty route registry.
func New() *Registry {
	return &Registry{
		identities:   make(map[string]struct{}),
		operationIDs: make(map[string]struct{}),
	}
}

// Add validates and stores one route definition.
func (r *Registry) Add(route Route) error {
	if r == nil {
		return errors.New("routecontract: nil registry")
	}
	route.Method = strings.ToUpper(strings.TrimSpace(route.Method))
	route.Path = strings.TrimSpace(route.Path)
	route.OperationID = strings.TrimSpace(route.OperationID)
	route.Summary = strings.TrimSpace(route.Summary)
	route.Auth = strings.ToLower(strings.TrimSpace(route.Auth))
	if route.Auth == "" {
		route.Auth = AuthNone
	}
	if model, ok := route.Request.(ModelRef); ok {
		if model.Model == nil {
			model.Model = ModelForName(model.Name)
		}
		if len(model.Fields) == 0 {
			model.Fields = FieldsForModel(model.Name)
		}
		model.Schema = modelSchemaRef(model)
		route.Request = model
	}
	if model, ok := route.Response.(ModelRef); ok {
		if model.Model == nil {
			model.Model = ModelForName(model.Name)
		}
		if len(model.Fields) == 0 {
			model.Fields = FieldsForModel(model.Name)
		}
		model.Schema = modelSchemaRef(model)
		route.Response = model
	}
	if route.Method == "" || route.Path == "" || route.OperationID == "" || route.Summary == "" {
		return errors.New("routecontract: method, path, operation ID, and summary are required")
	}
	if !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "?#") {
		return fmt.Errorf("routecontract: invalid path %q", route.Path)
	}
	switch route.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
	default:
		return fmt.Errorf("routecontract: unsupported method %q", route.Method)
	}
	switch route.Auth {
	case AuthNone, AuthOwner, AuthOperator, AuthToken, AuthService:
	default:
		return fmt.Errorf("routecontract: invalid auth class %q", route.Auth)
	}
	identity := route.Method + " " + route.Path
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.identities == nil {
		r.identities = make(map[string]struct{})
	}
	if r.operationIDs == nil {
		r.operationIDs = make(map[string]struct{})
	}
	if _, exists := r.identities[identity]; exists {
		return fmt.Errorf("routecontract: duplicate route %s", identity)
	}
	if _, exists := r.operationIDs[route.OperationID]; exists {
		return fmt.Errorf("routecontract: duplicate operation ID %q", route.OperationID)
	}
	r.identities[identity] = struct{}{}
	r.operationIDs[route.OperationID] = struct{}{}
	r.routes = append(r.routes, cloneRoute(route))
	return nil
}

func modelSchemaRef(model ModelRef) map[string]any {
	if model.Schema != nil || strings.TrimSpace(model.Name) == "" {
		return model.Schema
	}
	return map[string]any{"$ref": "#/components/schemas/" + model.Name}
}

// SetHandler binds the runtime handler to a previously registered route.
// Handler binding does not change route identity or generated metadata.
func (r *Registry) SetHandler(operationID string, handler http.HandlerFunc) error {
	if r == nil {
		return errors.New("routecontract: nil registry")
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || handler == nil {
		return errors.New("routecontract: operation ID and handler are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.routes {
		if r.routes[i].OperationID == operationID {
			r.routes[i].Handler = handler
			return nil
		}
	}
	return fmt.Errorf("routecontract: unknown operation ID %q", operationID)
}

// Snapshot returns a sorted deep copy of the registered route metadata.
func (r *Registry) Snapshot() []Route {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	routes := make([]Route, len(r.routes))
	for i := range r.routes {
		routes[i] = cloneRoute(r.routes[i])
	}
	r.mu.RUnlock()
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		if routes[i].Method != routes[j].Method {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].OperationID < routes[j].OperationID
	})
	return routes
}

func cloneRoute(route Route) Route {
	if model, ok := route.Request.(ModelRef); ok {
		model.Fields = append([]string(nil), model.Fields...)
		model.Schema = cloneMap(model.Schema)
		route.Request = model
	}
	if model, ok := route.Response.(ModelRef); ok {
		model.Fields = append([]string(nil), model.Fields...)
		model.Schema = cloneMap(model.Schema)
		route.Response = model
	}
	route.ContentTypes = append([]string(nil), route.ContentTypes...)
	route.Example = cloneMap(route.Example)
	route.Examples = cloneMap(route.Examples)
	return route
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	copy := make(map[string]any, len(values))
	for key, value := range values {
		copy[key] = cloneValue(value)
	}
	return copy
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneMap(value)
	case []any:
		copy := make([]any, len(value))
		for i := range value {
			copy[i] = cloneValue(value[i])
		}
		return copy
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}
