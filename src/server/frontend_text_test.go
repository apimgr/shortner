package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrontendTextMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body><h1>About</h1><p>Readable page</p><form><input value="secret"></form></body></html>`))
	})
	tests := []struct {
		name, method, path, userAgent, accept, contentType, bodyPart string
	}{
		{name: "HTTP tool gets text", method: http.MethodGet, path: "/server/about", userAgent: "curl/8.0", contentType: "text/plain; charset=utf-8", bodyPart: "Readable page"},
		{name: "HTTP tool gets text on form error", method: http.MethodPost, path: "/server/contact", userAgent: "curl/8.0", contentType: "text/plain; charset=utf-8", bodyPart: "Readable page"},
		{name: "explicit HTML wins", method: http.MethodGet, path: "/server/about", userAgent: "curl/8.0", accept: "text/html", contentType: "text/html; charset=utf-8", bodyPart: "<h1>About</h1>"},
		{name: "API is not converted", method: http.MethodGet, path: "/api/v1/server/about", userAgent: "curl/8.0", contentType: "text/html; charset=utf-8", bodyPart: "<h1>About</h1>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("User-Agent", tt.userAgent)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			res := httptest.NewRecorder()
			frontendTextMiddleware(next).ServeHTTP(res, req)
			if got := res.Header().Get("Content-Type"); got != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if !strings.Contains(res.Body.String(), tt.bodyPart) {
				t.Errorf("response body %q does not contain %q", res.Body.String(), tt.bodyPart)
			}
			if tt.name == "HTTP tool gets text" && strings.Contains(res.Body.String(), "secret") {
				t.Errorf("converted response contains form content: %q", res.Body.String())
			}
		})
	}
}
