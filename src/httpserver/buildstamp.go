package httpserver

import "net/http"

func buildStampMiddleware(stamp string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Build", stamp)
		next.ServeHTTP(w, r)
	})
}
