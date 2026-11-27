package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/swagger"
)

func TestAPIResponseFormatMiddlewareSupportsTextAndJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/about" {
			t.Errorf("handler path = %q, want /api/v1/server/about", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"data":{"name":"sample","stats":{"count":2}}}`))
	})
	h := urlNormalizeMiddleware(apiResponseFormatMiddleware(handler))

	tests := []struct {
		name        string
		path        string
		accept      string
		userAgent   string
		wantType    string
		wantContent string
	}{
		{name: "txt suffix", path: "/api/v1/server/about.txt", accept: "application/json", userAgent: "Mozilla/5.0", wantType: "text/plain; charset=utf-8", wantContent: "OK: Retrieved successfully\nname: sample\nstats.count: 2\n"},
		{name: "curl defaults to text", path: "/api/v1/server/about", userAgent: "curl/8.0", wantType: "text/plain; charset=utf-8", wantContent: "OK: Retrieved successfully\nname: sample\nstats.count: 2\n"},
		{name: "text browser defaults to text on API", path: "/api/v1/server/about", userAgent: "Lynx/2.9", wantType: "text/plain; charset=utf-8", wantContent: "OK: Retrieved successfully\nname: sample\nstats.count: 2\n"},
		{name: "explicit json wins", path: "/api/v1/server/about", accept: "application/json", userAgent: "curl/8.0", wantType: "application/json", wantContent: "\"data\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("User-Agent", tt.userAgent)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Header().Get("Content-Type"); got != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantType)
			}
			if !strings.Contains(rec.Body.String(), tt.wantContent) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tt.wantContent)
			}
			if tt.wantType == "application/json" && !json.Valid(rec.Body.Bytes()) {
				t.Errorf("JSON body is invalid: %q", rec.Body.String())
			}
		})
	}
}

func TestBinaryTextSuffixReturnsNotFound(t *testing.T) {
	registry := swagger.RouteRegistry()
	if err := registry.SetHandler("downloadCLIBinary", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("binary"))
	}); err != nil {
		t.Fatal(err)
	}
	handler := urlNormalizeMiddleware(apiResponseFormatMiddlewareWithRegistry(http.NotFoundHandler(), registry))
	request := httptest.NewRequest(http.MethodGet, "/cli/binaries/shortner-cli-linux-amd64.txt", nil)
	request.Header.Set("User-Agent", "curl/8.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	if !strings.Contains(response.Body.String(), "ERROR: NOT_FOUND:") {
		t.Fatalf("body = %q, want negotiated not-found error", response.Body.String())
	}
}

func TestAPITextSerializerUsesRouteResponseModel(t *testing.T) {
	var linkRoute, createRoute, listRoute, healthRoute swagger.Route
	for _, route := range swagger.RouteAnnotations() {
		switch route.OperationID {
		case "getLink":
			linkRoute = route
		case "createLink":
			createRoute = route
		case "listLinks":
			listRoute = route
		case "getHealth":
			healthRoute = route
		}
	}
	ordered := serializeAPITextForRoute([]byte(`{"ok":true,"data":{"click_count":3,"destination_url":"https://example.com","short_url":"https://sho.rt/x","short_code":"x","created_at":"2026-01-01T00:00:00Z","expires_at":null}}`), linkRoute, true)
	if got, want := string(ordered), "OK: Retrieved successfully\nshort_code: x\nshort_url: https://sho.rt/x\ndestination_url: https://example.com\ncreated_at: 2026-01-01T00:00:00Z\nexpires_at: null\nclick_count: 3"; got != want {
		t.Fatalf("ordered response = %q, want %q", got, want)
	}
	primary := serializeAPITextForRoute([]byte(`{"ok":true,"data":{"short_url":"https://sho.rt/x","short_code":"x"}}`), createRoute, true)
	if got, want := string(primary), "https://sho.rt/x"; got != want {
		t.Fatalf("primary response = %q, want %q", got, want)
	}
	list := serializeAPITextForRoute([]byte(`{"data":[{"short_code":"x","short_url":"https://sho.rt/x"}],"pagination":{"page":1,"limit":250,"total":1,"pages":1}}`), listRoute, true)
	if got := string(list); !strings.Contains(got, "OK: 1 items found\nshort_code: x\nshort_url: https://sho.rt/x\npagination.page: 1") {
		t.Fatalf("list response = %q, want ordered items and pagination", got)
	}
	health := serializeAPITextForRoute([]byte(`{"project":{"name":"shortner"},"status":"ok"}`), healthRoute, true)
	if got, want := string(health), "project.name: shortner\nstatus: ok"; got != want {
		t.Fatalf("health response = %q, want %q", got, want)
	}
}

func TestWantsText(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		accept string
		ua     string
		want   bool
	}{
		{"txt suffix", "/server/healthz.txt", "", "Mozilla/5.0", true},
		{"accept text/plain", "/server/healthz", "text/plain", "Mozilla/5.0", true},
		{"accept json wins over text/plain mix", "/server/healthz", "text/plain, application/json", "Mozilla/5.0", false},
		{"accept html wins over curl", "/server/healthz", "text/html", "curl/8.0", false},
		{"companion CLI defaults to JSON", "/server/healthz", "", "shortner-cli/1.0", false},
		{"curl user agent", "/server/healthz", "", "curl/8.0", true},
		{"empty user agent", "/server/healthz", "", "", true},
		{"browser gets json", "/server/healthz", "", "Mozilla/5.0 (Macintosh)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			req.Header.Set("User-Agent", tt.ua)
			if got := wantsText(req); got != tt.want {
				t.Errorf("wantsText() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsHTTPTool(t *testing.T) {
	tests := []struct {
		ua   string
		want bool
	}{
		{"curl/8.0", true},
		{"Wget/1.21", true},
		{"python-requests/2.31", true},
		{"Go-http-client/1.1", true},
		{"HTTPie/3.2", true},
		{"shortner-cli/1.0", false},
		{"Lynx/2.9", false},
		{"", true},
		{"Mozilla/5.0 (X11; Linux x86_64)", false},
	}
	for _, tt := range tests {
		t.Run(tt.ua, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("User-Agent", tt.ua)
			if got := isHTTPTool(req); got != tt.want {
				t.Errorf("isHTTPTool(%q) = %v, want %v", tt.ua, got, tt.want)
			}
		})
	}
}

func TestDetectClientType(t *testing.T) {
	tests := []struct {
		name, accept, userAgent string
		want                    clientType
	}{
		{name: "explicit JSON", accept: "application/json", userAgent: "Mozilla/5.0", want: clientJSON},
		{name: "explicit HTML overrides tool", accept: "text/html", userAgent: "curl/8.0", want: clientHTML},
		{name: "explicit text", accept: "text/plain", userAgent: "Mozilla/5.0", want: clientText},
		{name: "companion CLI is JSON", userAgent: "shortner-cli/1.0", want: clientJSON},
		{name: "text browser is HTML", userAgent: "Lynx/2.9", want: clientHTML},
		{name: "curl is text", userAgent: "curl/8.0", want: clientText},
		{name: "browser is HTML", userAgent: "Mozilla/5.0", want: clientHTML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("User-Agent", tt.userAgent)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			if got := detectClientType(req); got != tt.want {
				t.Errorf("detectClientType() = %v, want %v", got, tt.want)
			}
		})
	}
}
