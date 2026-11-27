package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/common/httputil"
	"github.com/apimgr/shortner/src/routecontract"
	"github.com/apimgr/shortner/src/swagger"
)

func apiResponseFormatMiddleware(next http.Handler) http.Handler {
	return apiResponseFormatMiddlewareWithRegistry(next, swagger.RouteRegistry())
}

func apiResponseFormatMiddlewareWithRegistry(next http.Handler, registry *routecontract.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		route, routeFound := apiRouteForRequestInRegistry(r, registry)
		if routeFound && (route.OperationID == "handleMCP" || route.OperationID == "handleMCPAlias" || route.OperationID == "handleRootMCP") {
			next.ServeHTTP(w, r)
			return
		}
		if !apiWantsText(r) {
			next.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		if routeFound && route.Binary != "" && strings.HasSuffix(originalRequestPath(r), ".txt") {
			sendError(recorder, r, apperr.New(apperr.CodeNotFound))
		} else {
			next.ServeHTTP(recorder, r)
		}
		result := recorder.Result()
		if err := result.Body.Close(); err != nil {
			http.Error(w, "response formatting failed", http.StatusInternalServerError)
			return
		}
		for name, values := range result.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.Header().Del("Content-Length")
		body := recorder.Body.Bytes()
		if routeFound && route.Binary != "" && recorder.Code < http.StatusBadRequest {
			for name, values := range result.Header {
				w.Header().Del(name)
				for _, value := range values {
					w.Header().Add(name, value)
				}
			}
			w.WriteHeader(recorder.Code)
			_, _ = w.Write(body)
			return
		}
		if strings.Contains(result.Header.Get("Content-Type"), "application/json") {
			body = serializeAPITextForRoute(body, route, routeFound)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(recorder.Code)
		if recorder.Code == http.StatusNoContent || recorder.Code == http.StatusNotModified || recorder.Code < http.StatusOK {
			return
		}
		if _, err := w.Write(ensureSingleNewline(body)); err != nil {
			log.Printf("write formatted API response: %v", err)
		}
	})
}

func apiWantsText(r *http.Request) bool {
	if strings.HasSuffix(originalRequestPath(r), ".txt") {
		return true
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") {
		return false
	}
	if strings.Contains(accept, "text/plain") {
		return true
	}
	return isHTTPTool(r) || httputil.IsTextBrowser(r)
}

func serializeAPITextForRoute(body []byte, route swagger.Route, routeFound bool) []byte {
	var envelope struct {
		OK      *bool           `json:"ok"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	if envelope.OK != nil && !*envelope.OK {
		return []byte(fmt.Sprintf("ERROR: %s: %s", envelope.Error, envelope.Message))
	}
	if envelope.OK == nil {
		data, err := decodeOrderedJSON(body)
		if err != nil {
			return body
		}
		if route.OperationID == "getHealth" || route.OperationID == "getAPIHealth" || route.OperationID == "getRootHealth" {
			return []byte(strings.Join(flattenAPIValue(orderModelFields(data, route), ""), "\n"))
		}
		return formatAPIData(data, envelope.Message, route, routeFound)
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return []byte("OK: " + responseMessage(envelope.Message, route, routeFound))
	}
	data, err := decodeOrderedJSON(envelope.Data)
	if err != nil {
		return envelope.Data
	}
	if routeFound && route.Primary != "" {
		if value, ok := primaryValue(data, route.Primary); ok {
			return []byte(apiTextValue(value))
		}
	}
	return formatAPIData(data, envelope.Message, route, routeFound)
}

func formatAPIData(data any, message string, route swagger.Route, routeFound bool) []byte {
	data = orderModelFields(data, route)
	if data == nil {
		return []byte("OK: " + responseMessage(message, route, routeFound))
	}
	if value, ok := data.(string); ok {
		return []byte("OK: " + responseMessage(message, route, routeFound) + "\nvalue: " + value)
	}
	var lines []string
	switch value := data.(type) {
	case []any:
		lines = formatAPIList(value)
	case orderedObject:
		if model, ok := route.Response.(routecontract.ModelRef); ok && model.Name == "LinkList" {
			if rawItems, exists := objectField(value, "data"); exists {
				if items, ok := rawItems.([]any); ok {
					lines = formatAPIList(items)
					if pagination, ok := objectField(value, "pagination"); ok {
						lines = append(lines, flattenAPIValue(pagination, "pagination")...)
					}
					break
				}
			}
		}
		lines = append(lines, "OK: "+responseMessage(message, route, routeFound))
		lines = append(lines, flattenAPIValue(value, "")...)
	default:
		lines = append(lines, "OK: "+responseMessage(message, route, routeFound))
		lines = append(lines, "value: "+apiTextValue(data))
	}
	return []byte(strings.Join(lines, "\n"))
}

func formatAPIList(items []any) []string {
	lines := []string{fmt.Sprintf("OK: %d items found", len(items))}
	for i, item := range items {
		if _, object := item.(orderedObject); object {
			lines = append(lines, flattenAPIValue(item, "")...)
			if i < len(items)-1 {
				lines = append(lines, "")
			}
		} else {
			lines = append(lines, apiTextValue(item))
		}
	}
	return lines
}

func objectField(object any, name string) (any, bool) {
	fields, ok := object.(orderedObject)
	if !ok {
		return nil, false
	}
	for _, field := range fields {
		if field.name == name {
			return field.value, true
		}
	}
	return nil, false
}

func orderModelFields(data any, route swagger.Route) any {
	model, ok := route.Response.(routecontract.ModelRef)
	if !ok || len(model.Fields) == 0 {
		return data
	}
	object, ok := data.(orderedObject)
	if !ok {
		return data
	}
	ordered := make(orderedObject, 0, len(object))
	used := make(map[string]struct{}, len(object))
	for _, name := range model.Fields {
		for _, field := range object {
			if field.name == name {
				ordered = append(ordered, field)
				used[name] = struct{}{}
				break
			}
		}
	}
	for _, field := range object {
		if _, exists := used[field.name]; !exists {
			ordered = append(ordered, field)
		}
	}
	return ordered
}

type orderedField struct {
	name  string
	value any
}

type orderedObject []orderedField

func decodeOrderedJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	return readOrderedJSON(decoder)
}

func readOrderedJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := orderedObject{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("invalid JSON object key")
			}
			value, err := readOrderedJSON(decoder)
			if err != nil {
				return nil, err
			}
			object = append(object, orderedField{name: key, value: value})
		}
		_, err = decoder.Token()
		return object, err
	case json.Delim('['):
		var values []any
		for decoder.More() {
			value, err := readOrderedJSON(decoder)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = decoder.Token()
		return values, err
	default:
		return token, nil
	}
}

func primaryValue(data any, field string) (any, bool) {
	object, ok := data.(orderedObject)
	if !ok {
		return nil, false
	}
	for _, item := range object {
		if item.name == field {
			return item.value, true
		}
	}
	return nil, false
}

func responseMessage(message string, route swagger.Route, routeFound bool) string {
	if message != "" {
		return message
	}
	if routeFound && route.Message != "" {
		return route.Message
	}
	if !routeFound {
		return "OK"
	}
	switch strings.ToUpper(route.Method) {
	case http.MethodGet, http.MethodHead:
		return "Retrieved successfully"
	case http.MethodPost:
		return "Created successfully"
	case http.MethodPut, http.MethodPatch:
		return "Updated successfully"
	case http.MethodDelete:
		return "Deleted successfully"
	default:
		return "OK"
	}
}

func apiRouteForRequestInRegistry(r *http.Request, registry *routecontract.Registry) (swagger.Route, bool) {
	path := strings.TrimSuffix(originalRequestPath(r), ".txt")
	for _, route := range swagger.RouteAnnotationsForRegistry(registry) {
		if !strings.EqualFold(route.Method, r.Method) || !routePathMatches(route.Path, path) {
			continue
		}
		return route, true
	}
	return swagger.Route{}, false
}

func routePathMatches(pattern, path string) bool {
	patternParts := strings.Split(strings.Trim(pattern, "/"), "/")
	pathParts := strings.Split(strings.Trim(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i, part := range patternParts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if pathParts[i] == "" {
				return false
			}
			continue
		}
		if part != pathParts[i] {
			return false
		}
	}
	return true
}

func flattenAPIValue(value any, prefix string) []string {
	switch typed := value.(type) {
	case orderedObject:
		var lines []string
		for _, field := range typed {
			name := field.name
			if prefix != "" {
				name = prefix + "." + field.name
			}
			lines = append(lines, flattenAPIValue(field.value, name)...)
		}
		return lines
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, apiTextValue(item))
		}
		return []string{prefix + ": " + strings.Join(parts, ", ")}
	default:
		return []string{prefix + ": " + apiTextValue(typed)}
	}
}

func apiTextValue(value any) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprint(value)
}

func ensureSingleNewline(body []byte) []byte {
	return append(bytes.TrimRight(body, "\r\n"), '\n')
}
