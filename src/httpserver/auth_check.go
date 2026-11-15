package httpserver

import (
	"net/http"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
)

func authCheckHandler(w http.ResponseWriter, r *http.Request) {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(value, "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		sendError(w, r, apperr.New(apperr.CodeUnauthorized))
		return
	}

	kind := ""
	switch {
	case IsOperator(r.Context()):
		kind = "operator"
	case tokenFromContext(r.Context()) != nil:
		kind = "owner"
	}
	if kind == "" {
		sendError(w, r, apperr.New(apperr.CodeUnauthorized))
		return
	}
	apperr.SendOK(w, map[string]string{"kind": kind})
}
