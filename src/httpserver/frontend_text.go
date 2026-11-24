package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/apimgr/shortner/src/common/httputil"
)

// frontendTextMiddleware converts rendered frontend pages for non-interactive
// HTTP clients while leaving API, static, and machine-readable responses intact.
func frontendTextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if (r.Method != http.MethodGet && r.Method != http.MethodPost) || !frontendPagePath(r.URL.Path) ||
			!httputil.IsHTTPTool(r) || strings.Contains(accept, "application/json") ||
			strings.Contains(accept, "text/html") {
			next.ServeHTTP(w, r)
			return
		}

		recorder := httptest.NewRecorder()
		next.ServeHTTP(recorder, r)
		if !strings.HasPrefix(strings.ToLower(recorder.Header().Get("Content-Type")), "text/html") {
			copyRecordedResponse(w, recorder)
			return
		}

		for key, values := range recorder.Header() {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Del("Content-Length")
		w.Header().Add("Vary", "Accept, User-Agent")
		w.WriteHeader(recorder.Code)
		_, _ = w.Write([]byte(httputil.HTML2TextConverter(recorder.Body.String(), 80)))
	})
}

func frontendPagePath(path string) bool {
	for _, prefix := range []string{"/api/", "/cli/", "/static/", "/locales/", "/metrics/"} {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}
	return path != "/healthz" && path != "/metrics" && path != "/server/metrics" && !strings.HasPrefix(path, "/server/metrics/")
}

func copyRecordedResponse(w http.ResponseWriter, recorder *httptest.ResponseRecorder) {
	for key, values := range recorder.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(recorder.Code)
	_, _ = w.Write(recorder.Body.Bytes())
}
