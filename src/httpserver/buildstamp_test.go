package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildStampMiddlewareSetsResponseHeader(t *testing.T) {
	handler := buildStampMiddleware("1.2.3-abc1234", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz", nil))
	if got := recorder.Header().Get("X-Build"); got != "1.2.3-abc1234" {
		t.Fatalf("X-Build = %q", got)
	}
}
