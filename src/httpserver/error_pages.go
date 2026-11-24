package httpserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
)

type frontendErrorRendererKey struct{}

type errorPageData struct {
	Base    PageData
	Message string
}

func frontendErrorContextMiddleware(fd *frontendDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), frontendErrorRendererKey{}, fd)
			next.ServeHTTP(w, r.WithContext(ctx))
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
	return renderPage(w, status, "error", data) == nil
}
