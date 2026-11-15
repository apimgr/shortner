package httpserver

import (
	"net/http"

	apiGraphql "github.com/apimgr/shortner/src/graphql"
	"github.com/apimgr/shortner/src/swagger"
)

type apiDocsPageData struct {
	Base     PageData
	Endpoint string
}

func (fd *frontendDeps) swaggerPage(w http.ResponseWriter, r *http.Request) {
	data := apiDocsPageData{Base: fd.newPageData(r, "", "API documentation", "Explore the Shortner REST API."), Endpoint: fd.resolver.BuildURL(r, "/api/swagger")}
	if err := renderPage(w, http.StatusOK, "swagger", data); err != nil {
		http.Error(w, "unable to render API documentation", http.StatusInternalServerError)
	}
}

func (fd *frontendDeps) graphqlPage(w http.ResponseWriter, r *http.Request) {
	data := apiDocsPageData{Base: fd.newPageData(r, "", "GraphQL documentation", "Explore read-only Shortner GraphQL queries."), Endpoint: fd.resolver.BuildURL(r, "/api/graphql")}
	if err := renderPage(w, http.StatusOK, "graphql", data); err != nil {
		http.Error(w, "unable to render GraphQL documentation", http.StatusInternalServerError)
	}
}

func openAPIHandler(fd *frontendDeps) http.Handler {
	return swagger.Handler(func(r *http.Request) string { return fd.resolver.BuildURL(r, "") })
}

func graphQLHandler(ld *linkDeps) http.Handler {
	return apiGraphql.Handler(func(r *http.Request) apiGraphql.Resolver {
		return apiGraphql.LinkResolver{DB: ld.sqlDB, Request: r, BuildURL: ld.resolver.BuildURL}
	})
}
