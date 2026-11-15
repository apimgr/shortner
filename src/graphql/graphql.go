// Package graphql implements the read-only GraphQL view of the REST API.
package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/graphql-go/graphql"
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

// Handler accepts standard GraphQL JSON POST requests and returns GraphQL's
// data/errors response shape. Mutations are rejected by the query-only schema.
func Handler(resolve ResolverFactory) http.Handler {
	schema, err := Schema()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.Error(w, "GraphQL schema unavailable", http.StatusInternalServerError)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var req request
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			_ = r.ParseForm()
			req.Query = r.FormValue("query")
		} else {
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
			_ = decoder.Decode(&req)
		}
		if req.Query == "" {
			http.Error(w, "invalid GraphQL request", http.StatusBadRequest)
			return
		}
		result := graphql.Do(graphql.Params{Schema: schema, RequestString: req.Query, VariableValues: req.Variables, OperationName: req.OperationName, Context: contextWithResolver(r, resolve(r))})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
}
