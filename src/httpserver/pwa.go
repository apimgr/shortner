package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/server"
)

func registerPWARoutes(r chi.Router, version, commitID string, branding ...*brandingStateStore) {
	stamp := assetStamp(version, commitID)
	var brandState *brandingStateStore
	if len(branding) > 0 {
		brandState = branding[0]
	}
	manifest := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		serveManifest(w, req, brandState, stamp)
	})
	r.Get("/manifest.json", manifest)
	r.Head("/manifest.json", manifest)
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
		w.Header().Set("ETag", buildETag(stamp))
		if etagMatches(req.Header.Get("If-None-Match"), buildETag(stamp)) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
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

func serveManifest(w http.ResponseWriter, r *http.Request, branding *brandingStateStore, stamp string) {
	body, err := fs.ReadFile(server.StaticFS, "static/manifest.json")
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	if branding != nil {
		settings := branding.load(nil)
		if strings.TrimSpace(settings.Branding.Favicon) != "" {
			var manifest map[string]json.RawMessage
			if err := json.Unmarshal(body, &manifest); err == nil {
				var icons []map[string]any
				if err := json.Unmarshal(manifest["icons"], &icons); err == nil {
					for _, icon := range icons {
						size, _ := icon["sizes"].(string)
						if size == "180x180" || size == "192x192" || size == "512x512" {
							querySize := strings.SplitN(size, "x", 2)[0]
							icon["src"] = "/branding/favicon?size=" + querySize
							delete(icon, "type")
							if strings.EqualFold(filepath.Ext(settings.Branding.Favicon), ".svg") {
								icon["sizes"] = "any"
							}
						}
					}
					if updatedIcons, err := json.Marshal(icons); err == nil {
						manifest["icons"] = updatedIcons
						if updated, err := json.Marshal(manifest); err == nil {
							body = updated
						}
					}
				}
			}
		}
	}
	body = stampManifestAssets(body, stamp)
	etag := manifestETag(body, stamp)
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func manifestETag(body []byte, stamp string) string {
	digest := sha256.Sum256(body)
	return buildETag(stamp + "-" + hex.EncodeToString(digest[:]))
}

func stampManifestAssets(body []byte, stamp string) []byte {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(body, &manifest); err != nil {
		return body
	}
	var icons []map[string]any
	if err := json.Unmarshal(manifest["icons"], &icons); err != nil {
		return body
	}
	for _, icon := range icons {
		src, ok := icon["src"].(string)
		if !ok || !strings.HasPrefix(src, "/static/") {
			continue
		}
		separator := "?"
		if strings.Contains(src, "?") {
			separator = "&"
		}
		icon["src"] = src + separator + "v=" + stamp
	}
	updatedIcons, err := json.Marshal(icons)
	if err != nil {
		return body
	}
	manifest["icons"] = updatedIcons
	updated, err := json.Marshal(manifest)
	if err != nil {
		return body
	}
	return updated
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
		etag := buildETag(stamp)
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		files.ServeHTTP(w, r)
	})
}

func serveEmbeddedAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	body, err := fs.ReadFile(server.StaticFS, name)
	if err != nil {
		sendError(w, r, apperr.New(apperr.CodeNotFound))
		return
	}
	if name == "static/offline.html" {
		if stamp := w.Header().Get("X-Build"); stamp != "" {
			body = stampOfflineAssets(body, stamp)
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	if stamp := w.Header().Get("X-Build"); stamp != "" {
		w.Header().Set("ETag", buildETag(stamp))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func stampOfflineAssets(body []byte, stamp string) []byte {
	body = bytes.ReplaceAll(body, []byte(`href="/static/css/common.css"`), []byte(`href="/static/css/common.css?v=`+stamp+`"`))
	return bytes.ReplaceAll(body, []byte(`href="/static/css/public.css"`), []byte(`href="/static/css/public.css?v=`+stamp+`"`))
}
