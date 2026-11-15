package httpserver

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/server"
)

func registerPWARoutes(r chi.Router, version, commitID string) {
	r.Get("/manifest.json", func(w http.ResponseWriter, req *http.Request) {
		serveEmbeddedAsset(w, req, "static/manifest.json", "application/manifest+json")
	})
	r.Head("/manifest.json", func(w http.ResponseWriter, req *http.Request) {
		serveEmbeddedAsset(w, req, "static/manifest.json", "application/manifest+json")
	})
	r.Get("/offline.html", func(w http.ResponseWriter, req *http.Request) {
		serveEmbeddedAsset(w, req, "static/offline.html", "text/html; charset=utf-8")
	})
	r.Head("/offline.html", func(w http.ResponseWriter, req *http.Request) {
		serveEmbeddedAsset(w, req, "static/offline.html", "text/html; charset=utf-8")
	})
	worker := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := serviceWorkerBody(version, commitID)
		if err != nil {
			sendError(w, req, apperr.New(apperr.CodeNotFound))
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(http.StatusOK)
		if req.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
	r.Get("/sw.js", worker)
	r.Head("/sw.js", worker)
}

func serviceWorkerBody(version, commitID string) ([]byte, error) {
	body, err := fs.ReadFile(server.StaticFS, "static/sw.js")
	if err != nil {
		return nil, err
	}
	return []byte(strings.ReplaceAll(string(body), "__BUILD_STAMP__", assetStamp(version, commitID))), nil
}

func assetStamp(version, commitID string) string {
	commitID = strings.TrimSpace(commitID)
	if len(commitID) > 7 {
		commitID = commitID[:7]
	}
	return strings.TrimSpace(version) + "-" + commitID
}

func staticAssetHandler(stamp string) http.Handler {
	files := http.StripPrefix("/static/", http.FileServer(http.FS(mustSubFS(server.StaticFS, "static"))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") == stamp && stamp != "-" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	})
}

func serveEmbeddedAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	body, err := fs.ReadFile(server.StaticFS, name)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
