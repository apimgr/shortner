package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
