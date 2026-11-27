package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/routecontract"
)

func TestMCPRejectsLegacyInitialize(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{}, version: "test"}
	req := authenticatedMCPRequest(httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "UnsupportedProtocolVersionError") {
		t.Fatalf("unexpected initialize response: status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestMCPVersionValidationPrecedesContentType(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{}}
	req := authenticatedMCPRequest(httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	rec := httptest.NewRecorder()
	mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "UnsupportedProtocolVersionError") {
		t.Fatalf("legacy initialize status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPAcceptsModernInitializeMetadata(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{}, version: "test"}
	req := authenticatedMCPRequest(httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"params":{"protocolVersion":"2026-07-28"}}`)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "initialize")
	rec := httptest.NewRecorder()
	mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"protocolVersion":"2026-07-28"`) {
		t.Fatalf("modern initialize status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPProtocolMetadataMismatchIsHeaderMismatch(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{}}
	req := authenticatedMCPRequest(httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2025-03-26"}}`)))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "ping")
	rec := httptest.NewRecorder()
	mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "HeaderMismatch") {
		t.Fatalf("metadata mismatch status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPRejectsUnsupportedVersionAndBadJSON(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{}}
	for _, tc := range []struct{ name, header, body, want string }{
		{"unsupported version", "2099-01-01", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "UnsupportedProtocolVersionError"},
		{"bad JSON", "", `{`, "ParseError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := authenticatedMCPRequest(httptest.NewRequest("POST", "/mcp", strings.NewReader(tc.body)))
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Content-Type", "application/json")
			if tc.header != "" {
				req.Header.Set("MCP-Protocol-Version", tc.header)
			}
			rec := httptest.NewRecorder()
			mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, req)
			if rec.Code != 400 || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMCPHeaderMismatchAndMethodHandling(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{}}
	for _, tc := range []struct {
		method, mcpMethod string
		status            int
		contains          string
	}{
		{"POST", "tools/list", 400, "HeaderMismatch"},
		{"GET", "", 405, "MethodNotAllowed"},
	} {
		req := authenticatedMCPRequest(httptest.NewRequest(tc.method, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`)))
		req.Header.Set("Accept", "application/json")
		if tc.method == "POST" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		}
		if tc.mcpMethod != "" {
			req.Header.Set("Mcp-Method", tc.mcpMethod)
		}
		rec := httptest.NewRecorder()
		mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, req)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.contains) {
			t.Fatalf("%s: status=%d body=%s", tc.method, rec.Code, rec.Body.String())
		}
		if tc.method == "GET" {
			var response struct {
				Error struct {
					Code float64 `json:"code"`
				} `json:"error"`
			}
			if rec.Header().Get("Content-Type") != "application/json" || json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.Error.Code != -32600 {
				t.Fatalf("GET response is not a numeric JSON-RPC error: headers=%v body=%s", rec.Header(), rec.Body.String())
			}
		}
	}
}

func authenticatedMCPRequest(req *http.Request) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), authTokenContextKey{}, &db.APIToken{Kind: "primary"}))
}

func TestMCPValidationOrderAndUnknownMethod(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{}, resolver: NewProxyResolver(nil)}
	legacy := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"initialize"}`))
	legacy.Header.Set("Origin", "https://blocked.example")
	rec := httptest.NewRecorder()
	mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, legacy)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Origin validation status=%d body=%s", rec.Code, rec.Body.String())
	}

	unauthenticated := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"initialize"}`))
	rec = httptest.NewRecorder()
	mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, unauthenticated)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("missing token status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}

	unknown := authenticatedMCPRequest(httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"made/up","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`)))
	unknown.Header.Set("Content-Type", "application/json")
	unknown.Header.Set("MCP-Protocol-Version", "2026-07-28")
	unknown.Header.Set("Mcp-Method", "made/up")
	unknown.Header.Set("Accept", "application/json")
	rec = httptest.NewRecorder()
	mcpHandler(fd, httpNotFoundHandler()).ServeHTTP(rec, unknown)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"id":7`) || !strings.Contains(rec.Body.String(), `"code":-32601`) {
		t.Fatalf("unknown method status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMCPToolAuditIdentifiesSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	audit, err := applog.NewAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	fd := &frontendDeps{audit: audit, resolver: NewProxyResolver(nil)}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "192.0.2.10:4321"
	req = req.WithContext(context.WithValue(req.Context(), authTokenContextKey{}, &db.APIToken{OwnerID: "owner_123"}))
	auditMCPToolCall(fd, req, mcpTool{Name: "getLink", Method: "get"}, "/api/v1/links/demo", http.StatusOK)
	if err := audit.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Event   string         `json:"event"`
		Source  string         `json:"source"`
		Actor   applog.Actor   `json:"actor"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Event != "api.mcp_tool_call" || entry.Source != "mcp" || entry.Details["operation"] != "getLink" || entry.Actor.UserID != "owner_123" {
		t.Fatalf("MCP audit entry = %+v", entry)
	}
}

func TestMCPMethodNotAllowedRouterHandlerReturnsJSON(t *testing.T) {
	req := httptest.NewRequest("DELETE", "/api/v1/server/mcp", nil)
	rec := httptest.NewRecorder()
	mcpMethodNotAllowed(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(rec.Body.String(), `"type":"MethodNotAllowed"`) {
		t.Fatalf("unexpected method-not-allowed response: status=%d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestMCPAnonymousToolsExcludeOwnerCreation(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{Server: config.Server{APIVersion: "v1"}}, resolver: NewProxyResolver(nil)}
	for _, tool := range mcpToolsForRequest(fd, httptest.NewRequest("POST", "/mcp", nil)) {
		if tool.Name == "createLink" {
			t.Fatal("anonymous tools include owner creation")
		}
		if strings.Contains(tool.Path, "/preferences/tokens") {
			t.Fatalf("token management exposed as tool: %s", tool.Name)
		}
	}
}

func TestMCPRouteAuthClasses(t *testing.T) {
	tests := []struct {
		name            string
		auth            string
		operator, owner bool
		want            bool
	}{
		{name: "public", auth: routecontract.AuthNone, want: true},
		{name: "owner required", auth: routecontract.AuthOwner, owner: true, want: true},
		{name: "owner excludes operator", auth: routecontract.AuthOwner, operator: true},
		{name: "operator required", auth: routecontract.AuthOperator, operator: true, want: true},
		{name: "token accepts owner", auth: routecontract.AuthToken, owner: true, want: true},
		{name: "token accepts operator", auth: routecontract.AuthToken, operator: true, want: true},
		{name: "service is never an MCP tool", auth: routecontract.AuthService, operator: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpRouteAllowed(tt.auth, tt.operator, tt.owner); got != tt.want {
				t.Errorf("mcpRouteAllowed(%q, %t, %t) = %t, want %t", tt.auth, tt.operator, tt.owner, got, tt.want)
			}
		})
	}
}

func TestMCPCallToolDispatchesInProcessWithBearerAndClientAddress(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{Server: config.Server{APIVersion: "v1"}}, resolver: NewProxyResolver(nil)}
	original := httptest.NewRequest("POST", "/mcp", nil)
	original.RemoteAddr = "192.0.2.14:4321"
	original.Header.Set("Authorization", "Bearer owner-token")
	original = original.WithContext(context.WithValue(original.Context(), authTokenContextKey{}, &db.APIToken{Kind: "primary"}))
	var called bool
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/server/healthz" || r.RemoteAddr != original.RemoteAddr {
			t.Errorf("internal call = %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		}
		if r.Header.Get("Authorization") != "Bearer owner-token" || r.Header.Get("Cookie") != "" {
			t.Errorf("internal request did not preserve Bearer and remove cookies: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"data":{"status":"ok"}}`))
	})
	result, rpcErr := mcpCallTool(fd, server, original, json.RawMessage(`{"name":"getHealth","arguments":{}}`))
	if rpcErr != nil || !called || result == nil {
		t.Fatalf("mcpCallTool() = (%#v, %#v), called=%t", result, rpcErr, called)
	}
}

func TestMCPCallToolRejectsProtectedOperations(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{Server: config.Server{APIVersion: "v1"}}, resolver: NewProxyResolver(nil)}
	request := httptest.NewRequest("POST", "/mcp", nil)
	_, err := mcpCallTool(fd, httpNotFoundHandler(), request, json.RawMessage(`{"name":"updateLink","arguments":{}}`))
	if err == nil || err.Code != -32001 {
		t.Fatalf("anonymous protected call error = %#v, want unauthorized", err)
	}
	operator := request.WithContext(context.WithValue(request.Context(), ctxKeyOperator, true))
	_, err = mcpCallTool(fd, httpNotFoundHandler(), operator, json.RawMessage(`{"name":"createLink","arguments":{}}`))
	if err == nil || err.Code != -32003 {
		t.Fatalf("operator owner-creation call error = %#v, want forbidden", err)
	}
}

func TestMCPOriginValidation(t *testing.T) {
	fd := &frontendDeps{cfg: &config.Config{Server: config.Server{CORS: config.CORS{AllowedOrigins: []string{"https://trusted.example"}}}}, resolver: NewProxyResolver(nil)}
	req := httptest.NewRequest("POST", "http://service.example/mcp", nil)
	if !mcpOriginAllowed(fd, req, "http://service.example") {
		t.Fatal("request's own origin was rejected")
	}
	if !mcpOriginAllowed(fd, req, "https://trusted.example") {
		t.Fatal("explicit origin was rejected")
	}
	if mcpOriginAllowed(fd, req, "https://attacker.example") {
		t.Fatal("unlisted origin was allowed")
	}
}

func TestMCPTokenExtractionRequiresBearerHeader(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp?token=query-token", nil)
	req.Header.Set("X-API-Key", "header-token")
	req.AddCookie(&http.Cookie{Name: "owner_token", Value: "cookie-token"})
	if got := ExtractToken(req); got != "" {
		t.Fatalf("ExtractToken(MCP without Bearer) = %q, want empty", got)
	}
	req.Header.Set("Authorization", "Bearer direct-token")
	if got := ExtractToken(req); got != "direct-token" {
		t.Fatalf("ExtractToken(MCP) = %q, want Bearer value", got)
	}
}

func TestMCPHelpPageRendersLiveEndpointAndClientSetup(t *testing.T) {
	cfg := config.Default("unused")
	fd := &frontendDeps{cfg: cfg, resolver: NewProxyResolver(nil), version: "test"}
	req := httptest.NewRequest("GET", "http://shortner.test/server/help/mcp", nil)
	rec := httptest.NewRecorder()
	fd.mcpHelpPage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "http://shortner.test/api/v1/server/mcp") || !strings.Contains(rec.Body.String(), "claude mcp add --transport http") || !strings.Contains(rec.Body.String(), "getLinkStats") {
		t.Fatalf("MCP help page status=%d, body missing required content: %s", rec.Code, rec.Body.String())
	}
}

func TestMCPHelpAPIProvidesCurrentConfiguration(t *testing.T) {
	cfg := config.Default("unused")
	fd := &frontendDeps{cfg: cfg, resolver: NewProxyResolver(nil)}
	req := httptest.NewRequest("GET", "http://shortner.test/api/v1/server/help/mcp", nil)
	rec := httptest.NewRecorder()
	fd.mcpHelpAPIHandler(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled": true`) || !strings.Contains(rec.Body.String(), `"url": "http://shortner.test/api/v1/server/mcp"`) || !strings.Contains(rec.Body.String(), `"scheme": "Bearer"`) {
		t.Fatalf("MCP help API status=%d, body=%s", rec.Code, rec.Body.String())
	}
}

func httpNotFoundHandler() http.Handler { return http.HandlerFunc(http.NotFound) }
