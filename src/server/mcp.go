package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/common/sanitize"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/routecontract"
	"github.com/apimgr/shortner/src/swagger"
)

const mcpProtocolVersion = "2026-07-28"

func isMCPPath(path string) bool {
	return path == "/mcp" || path == "/api/mcp" || strings.HasSuffix(path, "/server/mcp")
}

func mcpMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	if isMCPPath(r.URL.Path) {
		mcpHTTPError(w, http.StatusMethodNotAllowed, -32600, "MethodNotAllowed", "POST is required")
		return
	}
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Meta    json.RawMessage `json:"_meta"`
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"inputSchema"`
	Annotations map[string]bool `json:"annotations,omitempty"`
	Path        string          `json:"-"`
	Method      string          `json:"-"`
	Auth        bool            `json:"-"`
	PathParams  []string        `json:"-"`
	QueryParams []string        `json:"-"`
	BodyFields  []string        `json:"-"`
}

func mcpHandler(fd *frontendDeps, router http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			mcpHTTPError(w, http.StatusMethodNotAllowed, -32600, "MethodNotAllowed", "POST is required")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !mcpOriginAllowed(fd, r, origin) {
			mcpHTTPError(w, http.StatusForbidden, -32003, "Forbidden", "Origin is not allowed")
			return
		}
		if tokenFromContext(r.Context()) == nil && !IsOperator(r.Context()) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			mcpHTTPError(w, http.StatusUnauthorized, -32001, "Unauthorized", "A valid Bearer token is required")
			return
		}
		var msg mcpMessage
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&msg); err != nil || msg.JSONRPC != "2.0" || msg.Method == "" {
			mcpHTTPError(w, http.StatusBadRequest, -32700, "ParseError", "Request must be a JSON-RPC 2.0 request")
			return
		}
		version := r.Header.Get("MCP-Protocol-Version")
		var meta struct {
			ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
		}
		_ = json.Unmarshal(msg.Meta, &meta)
		if version != mcpProtocolVersion || msg.Method == "initialize" && meta.ProtocolVersion == "" {
			mcpParsedHTTPError(w, http.StatusBadRequest, msg.ID, -32600, "UnsupportedProtocolVersionError", "Supported protocol version: 2026-07-28")
			return
		}
		if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			mcpParsedHTTPError(w, http.StatusUnsupportedMediaType, msg.ID, -32600, "UnsupportedMediaType", "Content-Type must be application/json")
			return
		}
		if mismatch := mcpHeaderMismatch(r, msg); mismatch != "" {
			mcpParsedHTTPError(w, http.StatusBadRequest, msg.ID, -32020, "HeaderMismatch", mismatch)
			return
		}
		if !mcpKnownMethod(msg.Method) {
			mcpParsedHTTPError(w, http.StatusNotFound, msg.ID, -32601, "Method not found", "Method not found")
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "application/json") && !strings.Contains(r.Header.Get("Accept"), "*/*") {
			mcpHTTPError(w, http.StatusNotAcceptable, -32600, "NotAcceptable", "Accept must include application/json")
			return
		}
		var result any
		var rpcErr *mcpRPCError
		switch msg.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.ProtocolVersion != "" && p.ProtocolVersion != mcpProtocolVersion {
				mcpParsedHTTPError(w, http.StatusBadRequest, msg.ID, -32600, "UnsupportedProtocolVersionError", "Supported protocol version: 2026-07-28")
				return
			} else {
				version := p.ProtocolVersion
				if version == "" {
					version = mcpProtocolVersion
				}
				w.Header().Set("MCP-Protocol-Version", version)
				result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": fd.branding.load(fd.cfg).Branding.Title, "version": fd.version}}
			}
		case "notifications/initialized", "notifications/cancelled":
			w.WriteHeader(http.StatusAccepted)
			return
		case "ping":
			result = map[string]any{}
		case "tools/list":
			result = map[string]any{"tools": mcpToolsForRequest(fd, r)}
		case "tools/call":
			result, rpcErr = mcpCallTool(fd, router, r, msg.Params)
		default:
			if strings.HasPrefix(msg.Method, "notifications/") {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			rpcErr = &mcpRPCError{Code: -32601, Message: "method not found"}
		}
		if rpcErr != nil && (rpcErr.Code == -32001 || rpcErr.Code == -32003) {
			w.Header().Set("Content-Type", "application/json")
			if rpcErr.Code == -32001 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				w.WriteHeader(http.StatusForbidden)
			}
		}
		writeMCPResponse(w, msg.ID, result, rpcErr)
	})
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func writeMCPResponse(w http.ResponseWriter, id json.RawMessage, result any, rpcErr *mcpRPCError) {
	w.Header().Set("Content-Type", "application/json")
	var responseID any
	if len(id) > 0 {
		responseID = json.RawMessage(id)
	}
	response := map[string]any{"jsonrpc": "2.0", "id": responseID}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	_ = json.NewEncoder(w).Encode(response)
}

func mcpHTTPError(w http.ResponseWriter, status int, code int, name, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": code, "message": message, "data": map[string]string{"type": name}}})
}

func mcpParsedHTTPError(w http.ResponseWriter, status int, id json.RawMessage, code int, name, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeMCPResponse(w, id, nil, &mcpRPCError{Code: code, Message: message, Data: map[string]string{"type": name}})
}

func mcpKnownMethod(method string) bool {
	switch method {
	case "initialize", "notifications/initialized", "notifications/cancelled", "ping", "tools/list", "tools/call":
		return true
	default:
		return false
	}
}

func mcpHeaderMismatch(r *http.Request, msg mcpMessage) string {
	var meta struct {
		ProtocolVersion string `json:"io.modelcontextprotocol/protocolVersion"`
	}
	_ = json.Unmarshal(msg.Meta, &meta)
	if r.Header.Get("MCP-Protocol-Version") == "" || meta.ProtocolVersion == "" || r.Header.Get("MCP-Protocol-Version") != meta.ProtocolVersion {
		return "MCP-Protocol-Version does not match JSON-RPC metadata"
	}
	if value := r.Header.Get("Mcp-Method"); value == "" || value != msg.Method {
		return "Mcp-Method does not match JSON-RPC method"
	}
	var p struct {
		Name string `json:"name"`
		URI  string `json:"uri"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	nameRequired := msg.Method == "tools/call" || msg.Method == "resources/read" || msg.Method == "prompts/get"
	if nameRequired {
		want := p.Name
		if want == "" {
			want = p.URI
		}
		value := r.Header.Get("Mcp-Name")
		decoded, ok := decodeMCPName(value)
		if value == "" || !ok || decoded != want {
			return "Mcp-Name does not match JSON-RPC parameters"
		}
	}
	return ""
}

func decodeMCPName(value string) (string, bool) {
	if !strings.HasPrefix(value, "=?base64?") {
		for _, r := range value {
			if r < 0x20 || r > 0x7e {
				return "", false
			}
		}
		return value, true
	}
	encoded, ok := strings.CutPrefix(value, "=?base64?")
	if !ok {
		return "", false
	}
	encoded, ok = strings.CutSuffix(encoded, "?=")
	if !ok {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	return string(decoded), err == nil && len(decoded) > 0
}

func mcpOriginAllowed(fd *frontendDeps, r *http.Request, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	if strings.EqualFold(u.Host, r.Host) && strings.EqualFold(u.Scheme, requestScheme) {
		return true
	}
	current := fd.branding.load(fd.cfg).Config
	for _, allowed := range current.Server.CORS.AllowedOrigins {
		if allowed != "*" && strings.EqualFold(strings.TrimRight(allowed, "/"), strings.TrimRight(raw, "/")) {
			return true
		}
	}
	base := fd.resolver.BuildURL(r, "")
	if parsed, err := url.Parse(base); err == nil && parsed.IsAbs() && strings.EqualFold(parsed.Host, u.Host) && strings.EqualFold(parsed.Scheme, u.Scheme) {
		return true
	}
	return false
}

func mcpToolsForRequest(fd *frontendDeps, r *http.Request) []mcpTool {
	operator := IsOperator(r.Context())
	owner := tokenFromContext(r.Context()) != nil
	current := fd.branding.load(fd.cfg).Config
	tools := make([]mcpTool, 0)
	registry := mcpRouteRegistry(fd)
	docBytes, _ := swagger.DocumentWithRegistry(registry, fd.resolver.BuildURL(r, ""))
	var doc map[string]any
	_ = json.Unmarshal(docBytes, &doc)
	paths, _ := doc["paths"].(map[string]any)
	for _, route := range swagger.RouteAnnotationsForRegistry(registry) {
		if route.Internal || !strings.HasPrefix(route.Path, "/api/{api_version}/") || route.OperationID == "handleMCP" || strings.Contains(route.Path, "/preferences/tokens") || strings.Contains(route.Path, "/server/mcp") || strings.Contains(route.Path, "/metrics") || !mcpRouteAllowed(route.Auth, operator, owner) {
			continue
		}
		if route.OperationID == "createLink" && !owner {
			continue
		}
		path := strings.ReplaceAll(route.Path, "{api_version}", current.Server.APIVersion)
		operations, _ := paths[path].(map[string]any)
		operation, _ := operations[route.Method].(map[string]any)
		properties := map[string]any{}
		required := []string{}
		tool := mcpTool{Name: route.OperationID, Description: route.Summary, Annotations: map[string]bool{"readOnlyHint": route.Method == "get", "destructiveHint": route.Method == "delete", "idempotentHint": route.Method == "put" || route.Method == "delete"}, Path: route.Path, Method: route.Method, Auth: route.Auth != routecontract.AuthNone}
		if parameters, ok := operation["parameters"].([]any); ok {
			for _, raw := range parameters {
				parameter, _ := raw.(map[string]any)
				name, _ := parameter["name"].(string)
				location, _ := parameter["in"].(string)
				if name == "" || location == "" {
					continue
				}
				properties[name] = parameter["schema"]
				if location == "path" {
					tool.PathParams = append(tool.PathParams, name)
				}
				if location == "query" {
					tool.QueryParams = append(tool.QueryParams, name)
				}
				if parameter["required"] == true {
					required = append(required, name)
				}
			}
		}
		if requestBody, ok := operation["requestBody"].(map[string]any); ok {
			content, _ := requestBody["content"].(map[string]any)
			media, _ := content["application/json"].(map[string]any)
			schema, _ := media["schema"].(map[string]any)
			if ref, ok := schema["$ref"].(string); ok {
				parts := strings.Split(strings.TrimPrefix(ref, "#/components/schemas/"), "/")
				components, _ := doc["components"].(map[string]any)
				schemas, _ := components["schemas"].(map[string]any)
				if len(parts) > 0 {
					schema, _ = schemas[parts[0]].(map[string]any)
				}
			}
			if bodyProperties, ok := schema["properties"].(map[string]any); ok {
				for name, property := range bodyProperties {
					properties[name] = property
					tool.BodyFields = append(tool.BodyFields, name)
				}
			}
			if bodyRequired, ok := schema["required"].([]any); ok {
				for _, name := range bodyRequired {
					if value, ok := name.(string); ok {
						required = append(required, value)
					}
				}
			}
		}
		input := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			input["required"] = required
		}
		tool.InputSchema = input
		tools = append(tools, tool)
	}
	return tools
}

func mcpStandardTools(fd *frontendDeps, r *http.Request) []mcpTool {
	ctx := context.WithValue(r.Context(), authTokenContextKey{}, &db.APIToken{Kind: "owner"})
	return mcpToolsForRequest(fd, r.WithContext(ctx))
}

func mcpCallTool(fd *frontendDeps, router http.Handler, original *http.Request, raw json.RawMessage) (any, *mcpRPCError) {
	var input struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &input); err != nil || input.Name == "" {
		return nil, &mcpRPCError{Code: -32602, Message: "invalid tool arguments"}
	}
	var selected *mcpTool
	for _, tool := range mcpToolsForRequest(fd, original) {
		if tool.Name == input.Name {
			copyTool := tool
			selected = &copyTool
			break
		}
	}
	if selected == nil {
		for _, route := range swagger.RouteAnnotationsForRegistry(mcpRouteRegistry(fd)) {
			if route.OperationID != input.Name || strings.Contains(route.Path, "/preferences/tokens") || strings.Contains(route.Path, "/server/mcp") {
				continue
			}
			if route.Auth != routecontract.AuthNone || route.OperationID == "createLink" {
				if IsOperator(original.Context()) {
					return nil, &mcpRPCError{Code: -32003, Message: "insufficient permission for this tool"}
				}
				return nil, &mcpRPCError{Code: -32001, Message: "a valid Bearer token is required"}
			}
		}
		return nil, &mcpRPCError{Code: -32601, Message: "tool not found"}
	}
	path := strings.ReplaceAll(selected.Path, "{api_version}", fd.branding.load(fd.cfg).Config.Server.APIVersion)
	query := url.Values{}
	bodyFields := map[string]json.RawMessage{}
	for key, value := range input.Arguments {
		if strings.Contains(path, "{"+key+"}") {
			var pathValue any
			_ = json.Unmarshal(value, &pathValue)
			path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(fmt.Sprint(pathValue)))
			continue
		}
		var generic any
		_ = json.Unmarshal(value, &generic)
		if containsString(selected.QueryParams, key) {
			query.Set(key, fmt.Sprint(generic))
			continue
		}
		if mcpContainsString(selected.BodyFields, key) {
			bodyFields[key] = value
		}
	}
	var body []byte
	if len(bodyFields) > 0 {
		body, _ = json.Marshal(bodyFields)
	}
	if selected.Method == http.MethodGet || selected.Method == http.MethodDelete {
		body = nil
	}
	u, _ := url.Parse(path)
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	req := original.Clone(original.Context())
	req.Method = strings.ToUpper(selected.Method)
	req.URL = u
	req.RequestURI = u.RequestURI()
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.Header = original.Header.Clone()
	req.Header.Del("Cookie")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "shortner-mcp")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	auditMCPToolCall(fd, original, *selected, path, response.Code)
	data, err := io.ReadAll(response.Result().Body)
	if err != nil {
		return nil, &mcpRPCError{Code: -32603, Message: "API response could not be read"}
	}
	var parsed any
	if json.Unmarshal(data, &parsed) != nil {
		parsed = string(data)
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(data)}}, "structuredContent": parsed, "isError": response.Code < 200 || response.Code >= 300}, nil
}

func mcpRouteRegistry(fd *frontendDeps) *routecontract.Registry {
	if fd != nil && fd.routes != nil {
		return fd.routes
	}
	return swagger.RouteRegistry()
}

func mcpRouteAllowed(auth string, operator, owner bool) bool {
	switch auth {
	case routecontract.AuthNone:
		return true
	case routecontract.AuthOperator:
		return operator
	case routecontract.AuthOwner:
		return owner
	case routecontract.AuthToken:
		return operator || owner
	case routecontract.AuthService:
		return false
	default:
		return false
	}
}

func auditMCPToolCall(fd *frontendDeps, r *http.Request, tool mcpTool, path string, status int) {
	if fd.audit == nil {
		return
	}
	ip := ""
	if fd.resolver != nil {
		ip = fd.resolver.ResolveClientIP(r)
	}
	actor := applog.Actor{IP: ip}
	if token := tokenFromContext(r.Context()); token != nil {
		actor.UserID = token.OwnerID
	}
	result := applog.ResultSuccess
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		result = applog.ResultFailure
	}
	_ = fd.audit.Write(applog.Entry{
		Time: time.Now().UTC(), Event: "api.mcp_tool_call", Category: "api", Severity: applog.SeverityInfo,
		Actor:   actor,
		Details: map[string]any{"operation": tool.Name, "method": tool.Method, "path": path, "status": status},
		Source:  "mcp",
		Result:  result,
	})
}

func mcpContainsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type mcpHelpPageData struct {
	Base           PageData
	URL            string
	Aliases        []string
	Project        template.HTML
	ProjectName    string
	Tools          []mcpTool
	Versions       []string
	Overview       string
	Authentication string
	Connect        string
	ToolsInfo      string
	Limits         string
	Errors         []mcpHelpError
}

type mcpHelpError struct{ Status, Problem, Fix string }

func (fd *frontendDeps) mcpHelpPage(w http.ResponseWriter, r *http.Request) {
	current := fd.branding.load(fd.cfg).Config
	data := mcpHelpPageData{
		Base:        fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "mcp.title"), t(r, "mcp.subtitle")),
		URL:         fd.resolver.BuildURL(r, "/api/"+current.Server.APIVersion+"/server/mcp"),
		Aliases:     []string{fd.resolver.BuildURL(r, "/api/mcp"), fd.resolver.BuildURL(r, "/mcp")},
		Project:     sanitize.RenderMarkdown(current.Server.Pages.Help.MCP, r.Host),
		ProjectName: fd.branding.load(fd.cfg).Branding.Title,
		Tools:       mcpStandardTools(fd, r),
		Versions:    []string{mcpProtocolVersion},
	}
	data.Overview = t(r, "mcp.overview_text")
	data.Authentication = t(r, "mcp.auth_text")
	data.Connect = tf(r, "mcp.connect_text", map[string]string{"project": data.ProjectName, "url": data.URL})
	data.ToolsInfo = tf(r, "mcp.tools_info", map[string]string{"count": fmt.Sprint(len(data.Tools))})
	data.Limits = tf(r, "mcp.limits_text", map[string]string{"versions": strings.Join(data.Versions, ", ")})
	data.Errors = []mcpHelpError{
		{"400", t(r, "mcp.error_400_problem"), t(r, "mcp.error_400_fix")},
		{"401", t(r, "mcp.error_401_problem"), t(r, "mcp.error_401_fix")},
		{"403", t(r, "mcp.error_403_problem"), t(r, "mcp.error_403_fix")},
		{"405", t(r, "mcp.error_405_problem"), t(r, "mcp.error_405_fix")},
	}
	if err := renderPage(w, http.StatusOK, "mcp-help", data); err != nil {
		http.Error(w, "unable to render MCP help", http.StatusInternalServerError)
	}
}
