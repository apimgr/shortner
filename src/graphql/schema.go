package graphql

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/swagger"
	"github.com/graphql-go/graphql"
)

// The executable schema is built directly from the Go API types and
// resolver contract so field names and types cannot drift from implementation.
func localized(lang, key, fallback string) string {
	value := i18n.Translate(lang, key)
	if value == key {
		return fallback
	}
	return value
}

func linkObject(lang string) *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name:        "Link",
		Description: localized(lang, "graphql.type.link", "A publicly readable shortened link."),
		Fields: graphql.Fields{
			"shortCode":      &graphql.Field{Type: graphql.NewNonNull(graphql.String), Description: localized(lang, "graphql.field.short_code", "The short URL path code.")},
			"shortURL":       &graphql.Field{Type: graphql.NewNonNull(graphql.String), Description: localized(lang, "graphql.field.short_url", "The absolute short URL, using the request origin.")},
			"destinationURL": &graphql.Field{Type: graphql.NewNonNull(graphql.String), Description: localized(lang, "graphql.field.destination_url", "The destination URL.")},
			"createdAt":      &graphql.Field{Type: graphql.NewNonNull(graphql.String), Description: localized(lang, "graphql.field.created_at", "Creation timestamp in RFC 3339 UTC format.")},
			"expiresAt":      &graphql.Field{Type: graphql.String, Description: localized(lang, "graphql.field.expires_at", "Expiration timestamp in RFC 3339 UTC format, or null.")},
			"clickCount":     &graphql.Field{Type: graphql.NewNonNull(graphql.Int), Description: localized(lang, "graphql.field.click_count", "Total recorded clicks.")},
		},
	})
}

func linkStatsObject(lang string) *graphql.Object {
	return graphql.NewObject(graphql.ObjectConfig{
		Name:        "LinkStats",
		Description: localized(lang, "graphql.type.link_stats", "Public click analytics for a short link."),
		Fields: graphql.Fields{
			"shortCode":   &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
			"totalClicks": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
			"referrers":   &graphql.Field{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(referrerType)))},
			"timeSeries":  &graphql.Field{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(dayCountType)))},
			"recent":      &graphql.Field{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(clickInfoType)))},
		},
	})
}

var referrerType = graphql.NewObject(graphql.ObjectConfig{Name: "ReferrerCount", Fields: graphql.Fields{
	"name":  &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
	"count": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
}})

var dayCountType = graphql.NewObject(graphql.ObjectConfig{Name: "DayCount", Fields: graphql.Fields{
	"date":  &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
	"count": &graphql.Field{Type: graphql.NewNonNull(graphql.Int)},
}})

var clickInfoType = graphql.NewObject(graphql.ObjectConfig{Name: "ClickInfo", Fields: graphql.Fields{
	"timestamp": &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
	"ip":        &graphql.Field{Type: graphql.NewNonNull(graphql.String)},
	"referrer":  &graphql.Field{Type: graphql.String},
	"country":   &graphql.Field{Type: graphql.String},
	"region":    &graphql.Field{Type: graphql.String},
}})

func apiReadScalar(lang string) *graphql.Scalar {
	return graphql.NewScalar(graphql.ScalarConfig{
		Name:        "JSON",
		Description: localized(lang, "graphql.type.json", "A JSON value returned by a documented read-only REST endpoint."),
		Serialize:   func(value any) any { return value },
	})
}

// Schema is generated from the same public Link fields used by the resolver.
func Schema(languages ...string) (graphql.Schema, error) {
	lang := "en"
	if len(languages) > 0 {
		lang = languages[0]
	}
	linkType, linkStatsType := linkObject(lang), linkStatsObject(lang)
	apiReadType := apiReadScalar(lang)
	fields := graphql.Fields{
		"link": &graphql.Field{Type: linkType, Description: localized(lang, "graphql.query.link", "Read one link by its short code."), Args: graphql.FieldConfigArgument{"slug": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}}, Resolve: func(p graphql.ResolveParams) (any, error) {
			return p.Context.Value(resolverKey{}).(Resolver).Link(p.Args["slug"].(string))
		}},
		"links": &graphql.Field{Type: graphql.NewList(graphql.NewNonNull(linkType)), Description: localized(lang, "graphql.query.links", "List links ordered by the REST API's default ordering."), Args: graphql.FieldConfigArgument{"limit": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 50}, "offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0}}, Resolve: func(p graphql.ResolveParams) (any, error) {
			return p.Context.Value(resolverKey{}).(Resolver).Links(p.Args["limit"].(int), p.Args["offset"].(int))
		}},
		"linkStats": &graphql.Field{Type: linkStatsType, Description: localized(lang, "graphql.query.link_stats", "Read click statistics for a link."), Args: graphql.FieldConfigArgument{"slug": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}}, Resolve: func(p graphql.ResolveParams) (any, error) {
			return p.Context.Value(resolverKey{}).(Resolver).LinkStats(p.Args["slug"].(string))
		}},
	}
	for _, route := range swagger.RouteAnnotations(lang) {
		if route.Method != "get" || route.OperationID == "importSyncedPreferences" || strings.HasPrefix(route.Path, "/cli/") {
			continue
		}
		route := route
		args := graphql.FieldConfigArgument{}
		for _, name := range routePathArguments(route.Path) {
			args[name] = &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}
		}
		fields[route.OperationID] = &graphql.Field{
			Type:        apiReadType,
			Description: route.Summary,
			Args:        args,
			Resolve: func(p graphql.ResolveParams) (any, error) {
				path, err := routePath(route.Path, p.Args)
				if err != nil {
					return nil, err
				}
				return p.Context.Value(resolverKey{}).(Resolver).APIRead(path)
			},
		}
	}
	query := graphql.NewObject(graphql.ObjectConfig{Name: "Query", Description: i18n.Translate(lang, "graphql.type.query"), Fields: fields})
	return graphql.NewSchema(graphql.SchemaConfig{Query: query})
}

func routePathArguments(path string) []string {
	var names []string
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			names = append(names, strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}"))
		}
	}
	return names
}

func routePath(path string, args map[string]any) (string, error) {
	for _, name := range routePathArguments(path) {
		value, ok := args[name].(string)
		if !ok || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "/?#") {
			return "", fmt.Errorf("invalid GraphQL REST path argument %q", name)
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}
	return path, nil
}
