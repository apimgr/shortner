package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
)

func buildStampMiddleware(stamp string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Build", stamp)
		next.ServeHTTP(&buildStampWriter{ResponseWriter: w, request: r, stamp: stamp}, r)
	})
}

type buildStampWriter struct {
	http.ResponseWriter
	request   *http.Request
	stamp     string
	wroteHead bool
}

func (w *buildStampWriter) WriteHeader(status int) {
	if w.wroteHead {
		return
	}
	w.wroteHead = true
	if isHTMLResponse(w.Header().Get("Content-Type")) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("ETag", buildETag(w.stamp))
		w.Header().Add("Set-Cookie", (&http.Cookie{
			Name: "shortner_build", Value: w.stamp, Path: "/", MaxAge: 31536000,
			Secure: true, SameSite: http.SameSiteLaxMode,
		}).String())
		if w.shouldPurge(status) {
			w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *buildStampWriter) Write(body []byte) (int, error) {
	if !w.wroteHead {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *buildStampWriter) Flush() {
	if !w.wroteHead {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *buildStampWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *buildStampWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("http.Hijacker is not supported")
	}
	return h.Hijack()
}

func (w *buildStampWriter) shouldPurge(status int) bool {
	if status < 200 || status >= 300 || w.request.Method == http.MethodHead {
		return false
	}
	dest, mode := w.request.Header.Get("Sec-Fetch-Dest"), w.request.Header.Get("Sec-Fetch-Mode")
	if (dest != "" || mode != "") && (dest != "document" || mode != "navigate") {
		return false
	}
	cookie, err := w.request.Cookie("shortner_build")
	return err == nil && cookie.Value != w.stamp
}

func isHTMLResponse(contentType string) bool {
	return len(contentType) >= 9 && contentType[:9] == "text/html"
}

func buildETag(stamp string) string { return fmt.Sprintf("%q", stamp) }

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
