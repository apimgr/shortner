package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	apiGraphql "github.com/apimgr/shortner/src/graphql"
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
		Operations: swagger.RouteAnnotations(langFromContext(r)),
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
		Endpoint:      fd.resolver.BuildURL(r, "/api/graphql"),
		GraphQLSchema: string(apiGraphql.SchemaDocument(lang)),
		Queries:       queries,
	}
	if err := renderPage(w, http.StatusOK, "graphql", data); err != nil {
		http.Error(w, "unable to render GraphQL documentation", http.StatusInternalServerError)
	}
}

func openAPIHandler(fd *frontendDeps) http.Handler {
	return swagger.Handler(
		func(r *http.Request) string { return fd.resolver.BuildURL(r, "") },
		func(r *http.Request) string { return fd.branding.load(fd.cfg).Branding.Title },
	)
}

func graphQLHandler(ld *linkDeps, apiRouter http.Handler) http.Handler {
	return apiGraphql.Handler(func(r *http.Request) apiGraphql.Resolver {
		return apiGraphql.LinkResolver{
			DB:       ld.sqlDB,
			Request:  r,
			BuildURL: ld.resolver.BuildURL,
			ReadAPI: func(path string) (any, error) {
				return graphQLAPIRead(apiRouter, r, path)
			},
		}
	})
}

func graphQLAPIRead(apiRouter http.Handler, original *http.Request, path string) (any, error) {
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !swagger.IsReadPath(parsed.Path) {
		return nil, fmt.Errorf("GraphQL path is not a documented read-only API route")
	}
	req := original.Clone(original.Context())
	req.URL = parsed
	req.Method = http.MethodGet
	req.Header = original.Header.Clone()
	req.Header.Set("Accept", "application/json")
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
