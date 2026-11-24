package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIResponseFormatMiddlewareSupportsTextAndJSON(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/items" {
			t.Errorf("handler path = %q, want /api/v1/items", r.URL.Path)
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
		{name: "txt suffix", path: "/api/v1/items.txt", accept: "application/json", userAgent: "Mozilla/5.0", wantType: "text/plain; charset=utf-8", wantContent: "OK: Success\nname: sample\nstats.count: 2\n"},
		{name: "curl defaults to text", path: "/api/v1/items", userAgent: "curl/8.0", wantType: "text/plain; charset=utf-8", wantContent: "OK: Success\nname: sample\nstats.count: 2\n"},
		{name: "explicit json wins", path: "/api/v1/items", accept: "application/json", userAgent: "curl/8.0", wantType: "application/json", wantContent: "\"data\""},
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
