package httputil

import (
	"net/http/httptest"
	"testing"
)

func TestClientDetection(t *testing.T) {
	tests := []struct {
		name, userAgent    string
		tool, browser, cli bool
	}{
		{name: "empty agent", tool: true},
		{name: "curl", userAgent: "curl/8.4.0", tool: true},
		{name: "text browser", userAgent: "Lynx/2.9", browser: true},
		{name: "companion CLI", userAgent: "shortner-cli/1.2", cli: true},
		{name: "ordinary browser", userAgent: "Mozilla/5.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", tt.userAgent)
			if got := IsHTTPTool(req); got != tt.tool {
				t.Errorf("IsHTTPTool() = %v, want %v", got, tt.tool)
			}
			if got := IsTextBrowser(req); got != tt.browser {
				t.Errorf("IsTextBrowser() = %v, want %v", got, tt.browser)
			}
			if got := IsOurCLIClient(req); got != tt.cli {
				t.Errorf("IsOurCLIClient() = %v, want %v", got, tt.cli)
			}
		})
	}
}
