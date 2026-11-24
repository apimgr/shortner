// Package graphql implements the read-only GraphQL view of the REST API.
package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/visitor"
)

// Link is the read-only public link representation exposed by GraphQL.
type Link struct {
	ShortCode      string  `json:"shortCode"`
	ShortURL       string  `json:"shortURL"`
	DestinationURL string  `json:"destinationURL"`
	CreatedAt      string  `json:"createdAt"`
	ExpiresAt      *string `json:"expiresAt"`
	ClickCount     int64   `json:"clickCount"`
}

// LinkStats mirrors the public analytics response for a short link.
type LinkStats struct {
	ShortCode   string      `json:"shortCode"`
	TotalClicks int64       `json:"totalClicks"`
	Referrers   []Referrer  `json:"referrers"`
	TimeSeries  []DayCount  `json:"timeSeries"`
	Recent      []ClickInfo `json:"recent"`
}

// Referrer is one referrer count in link analytics.
type Referrer struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// DayCount is one UTC date and its click count.
type DayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// ClickInfo is an anonymized recent click record.
type ClickInfo struct {
	Timestamp string  `json:"timestamp"`
	IP        string  `json:"ip"`
	Referrer  *string `json:"referrer"`
	Country   *string `json:"country"`
	Region    *string `json:"region"`
}

// Resolver supplies only read operations; GraphQL cannot mutate data.
type Resolver interface {
	Link(slug string) (*Link, error)
	Links(limit, offset int) ([]Link, error)
	LinkStats(slug string) (*LinkStats, error)
	APIRead(path string) (any, error)
}

// ResolverFactory binds request-scoped context and origin data to operations.
type ResolverFactory func(*http.Request) Resolver

type resolverKey struct{}

func contextWithResolver(r *http.Request, resolver Resolver) context.Context {
	return context.WithValue(r.Context(), resolverKey{}, resolver)
}

type request struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
	OperationName string         `json:"operationName"`
}

const maxQueryFields = 100

// Handler accepts GraphQL JSON or form POST requests and query-string GET
// requests, returning GraphQL's data/errors response shape. Mutations are
// rejected by the query-only schema.
func Handler(resolve ResolverFactory) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := i18n.LangFromRequest(r, i18n.CookieName, i18n.DefaultLanguage)
		schema, err := Schema(lang)
		if err != nil {
			http.Error(w, "GraphQL schema unavailable", http.StatusInternalServerError)
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req request
		if r.Method == http.MethodGet {
			values, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil {
				http.Error(w, "invalid GraphQL query parameters", http.StatusBadRequest)
				return
			}
			req.Query = values.Get("query")
			req.OperationName = values.Get("operationName")
			if variables := values.Get("variables"); variables != "" {
				if err := json.Unmarshal([]byte(variables), &req.Variables); err != nil {
					http.Error(w, "invalid GraphQL variables", http.StatusBadRequest)
					return
				}
			}
			if req.Query == "" {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = fmt.Fprintln(w, "Read-only GraphQL endpoint. Supply query and optionally variables and operationName.")
				return
			}
		} else {
			defer r.Body.Close()
			if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
				if err := r.ParseForm(); err != nil {
					http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
					return
				}
				req.Query = r.FormValue("query")
				req.OperationName = r.FormValue("operationName")
				if variables := r.FormValue("variables"); variables != "" {
					if err := json.Unmarshal([]byte(variables), &req.Variables); err != nil {
						http.Error(w, "invalid GraphQL variables", http.StatusBadRequest)
						return
					}
				}
			} else {
				decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
				if err := decoder.Decode(&req); err != nil {
					http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
					return
				}
			}
		}
		if req.Query == "" {
			http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
			return
		}
		if exceedsQueryComplexity(req.Query) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"message": fmt.Sprintf("GraphQL query exceeds the %d-field complexity limit", maxQueryFields)}}})
			return
		}
		result := graphql.Do(graphql.Params{Schema: schema, RequestString: req.Query, VariableValues: req.Variables, OperationName: req.OperationName, Context: contextWithResolver(r, resolve(r))})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
}

func exceedsQueryComplexity(query string) bool {
	document, err := parser.Parse(parser.ParseParams{Source: query})
	if err != nil {
		return false
	}
	count := 0
	visitor.Visit(document, &visitor.VisitorOptions{Enter: func(p visitor.VisitFuncParams) (string, any) {
		switch p.Node.(type) {
		case *ast.Field, *ast.FragmentSpread:
			count++
			if count > maxQueryFields {
				return visitor.ActionBreak, nil
			}
		}
		return visitor.ActionNoChange, nil
	}}, nil)
	return count > maxQueryFields
}
