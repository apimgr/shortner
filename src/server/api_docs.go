package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	apiGraphql "github.com/apimgr/shortner/src/graphql"
	"github.com/apimgr/shortner/src/routecontract"
	"github.com/apimgr/shortner/src/swagger"
)

type apiDocsPageData struct {
	Base          PageData
	Endpoint      string
	APIVersion    string
	GraphQLSchema string
	Operations    []swagger.Route
	Queries       []apiQueryDoc
}

type apiQueryDoc = apiGraphql.QueryDocumentation

func (fd *frontendDeps) swaggerPage(w http.ResponseWriter, r *http.Request) {
	data := apiDocsPageData{
		Base:       fd.newPageData(r, "", "API documentation", "Explore the Shortner REST API."),
		Endpoint:   fd.resolver.BuildURL(r, "/api/swagger"),
		APIVersion: fd.cfg.Server.APIVersion,
		Operations: swagger.RouteAnnotationsForRegistry(fd.routes, langFromContext(r)),
	}
	for i := range data.Operations {
		data.Operations[i].Method = strings.ToUpper(data.Operations[i].Method)
	}
	if err := renderPage(w, http.StatusOK, "swagger", data); err != nil {
		http.Error(w, "unable to render API documentation", http.StatusInternalServerError)
	}
}

func (fd *frontendDeps) graphqlPage(w http.ResponseWriter, r *http.Request) {
	lang := langFromContext(r)
	queries, err := apiGraphql.DocumentQueries(lang)
	if err != nil {
		http.Error(w, "GraphQL schema unavailable", http.StatusInternalServerError)
		return
	}
	data := apiDocsPageData{
		Base:          fd.newPageData(r, "", "GraphQL documentation", "Explore read-only Shortner GraphQL queries."),
		Endpoint:      fd.resolver.BuildURL(r, "/api/"+fd.cfg.Server.APIVersion+"/server/graphql"),
		GraphQLSchema: string(apiGraphql.SchemaDocument(lang)),
		Queries:       queries,
	}
	if err := renderPage(w, http.StatusOK, "graphql", data); err != nil {
		http.Error(w, "unable to render GraphQL documentation", http.StatusInternalServerError)
	}
}

func openAPIHandler(fd *frontendDeps) http.Handler {
	return swagger.HandlerWithRegistry(fd.routes,
		func(r *http.Request) string { return fd.resolver.BuildURL(r, "") },
		func(r *http.Request) string { return fd.branding.load(fd.cfg).Branding.Title },
	)
}

func graphQLHandler(ld *linkDeps, apiRouter http.Handler, registries ...*routecontract.Registry) http.Handler {
	var registry *routecontract.Registry
	if len(registries) > 0 {
		registry = registries[0]
	}
	return apiGraphql.Handler(func(r *http.Request) apiGraphql.Resolver {
		return apiGraphql.LinkResolver{
			DB:       ld.sqlDB,
			Request:  r,
			BuildURL: ld.resolver.BuildURL,
			ReadAPI: func(path string) (any, error) {
				return graphQLAPIRead(apiRouter, r, path, registry)
			},
		}
	})
}

func graphQLAPIRead(apiRouter http.Handler, original *http.Request, path string, registries ...*routecontract.Registry) (any, error) {
	parsed, err := url.ParseRequestURI(path)
	var registry *routecontract.Registry
	if len(registries) > 0 {
		registry = registries[0]
	}
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !swagger.IsReadPathInRegistry(registry, parsed.Path) {
		return nil, fmt.Errorf("GraphQL path is not a documented read-only API route")
	}
	ctx := context.WithValue(original.Context(), chi.RouteCtxKey, chi.NewRouteContext())
	req := original.Clone(ctx)
	req.URL = parsed
	req.Method = http.MethodGet
	req.Header = original.Header.Clone()
	req.Header.Set("Accept", "application/json")
	req.Header.Del("Accept-Encoding")
	req.Header.Set("User-Agent", "shortner-graphql")
	recorder := httptest.NewRecorder()
	apiRouter.ServeHTTP(recorder, req)
	if recorder.Code < http.StatusOK || recorder.Code >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("REST read returned HTTP %d", recorder.Code)
	}
	if strings.Contains(recorder.Header().Get("Content-Type"), "json") {
		var response any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			return nil, fmt.Errorf("decode REST read response: %w", err)
		}
		return response, nil
	}
	if strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/") {
		return recorder.Body.String(), nil
	}
	return nil, fmt.Errorf("REST read returned an unsupported content type")
}
