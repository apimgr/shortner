package server

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
)

type frontendErrorRendererKey struct{}

type requestAwareResponseWriter struct {
	http.ResponseWriter
	request *http.Request
}

func (w *requestAwareResponseWriter) Request() *http.Request      { return w.request }
func (w *requestAwareResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *requestAwareResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (w *requestAwareResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}
func (w *requestAwareResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

type errorPageData struct {
	Base    PageData
	Message string
}

func frontendErrorContextMiddleware(fd *frontendDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), frontendErrorRendererKey{}, fd)
			r = r.WithContext(ctx)
			next.ServeHTTP(&requestAwareResponseWriter{ResponseWriter: w, request: r}, r)
		})
	}
}

func renderFrontendError(w http.ResponseWriter, r *http.Request, err *apperr.AppError) bool {
	if err == nil || strings.HasPrefix(r.URL.Path, "/api/") || detectClientType(r) != clientHTML {
		return false
	}
	fd, ok := r.Context().Value(frontendErrorRendererKey{}).(*frontendDeps)
	if !ok || fd == nil {
		return false
	}
	status := err.HTTPStatus
	if status < http.StatusBadRequest {
		status = http.StatusInternalServerError
	}
	data := errorPageData{
		Base:    fd.newPageData(r, requestCSRFToken(r, fd.cfg), t(r, "errors.page_title"), fd.cfg.Server.Branding.Description),
		Message: err.Message,
	}
	if err := renderPage(w, status, "error", data); err != nil {
		return true
	}
	return true
}

// RecoverMiddleware guarantees a response when a request handler panics.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic recovered: %v\n%s", recovered, debug.Stack())
				renderFallbackError(w, r, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func renderFallbackError(w http.ResponseWriter, r *http.Request, status int) {
	accept, path := "", ""
	if r != nil {
		accept = r.Header.Get("Accept")
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	if strings.Contains(accept, "application/json") || strings.HasPrefix(path, "/api/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, "{\"ok\":false,\"error\":\"SERVER_ERROR\",\"message\":%q}\n", http.StatusText(status))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>%d %s</title></head><body><main><h1>%d %s</h1><p><a href=\"/\">Home</a></p></main></body></html>", status, http.StatusText(status), status, http.StatusText(status))
}
