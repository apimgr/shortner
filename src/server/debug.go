package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"expvar"
	"fmt"
	"net/http"
	httppprof "net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/mode"
	"github.com/go-chi/chi/v5"
)

func registerDebugRoutes(r chi.Router, deps *deps, scheduler SchedulerReporter, cacheDriver string, cacheStore any) {
	if !mode.IsDebugEnabled() {
		return
	}
	r.Route("/debug", func(debug chi.Router) {
		debug.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
				if !ok || deps.operatorTok == "" || len(token) != len(deps.operatorTok) || subtle.ConstantTimeCompare([]byte(token), []byte(deps.operatorTok)) != 1 {
					sendError(w, req, apperr.New(apperr.CodeUnauthorized))
					return
				}
				next.ServeHTTP(w, req)
			})
		})
		debug.Get("/pprof/", httppprof.Index)
		debug.Get("/pprof", func(w http.ResponseWriter, req *http.Request) {
			clone := req.Clone(req.Context())
			clone.URL.Path += "/"
			httppprof.Index(w, clone)
		})
		debug.Get("/pprof/heap", httppprof.Handler("heap").ServeHTTP)
		debug.Get("/pprof/goroutine", httppprof.Handler("goroutine").ServeHTTP)
		debug.Get("/pprof/allocs", httppprof.Handler("allocs").ServeHTTP)
		debug.Get("/pprof/block", httppprof.Handler("block").ServeHTTP)
		debug.Get("/pprof/mutex", httppprof.Handler("mutex").ServeHTTP)
		debug.Get("/pprof/threadcreate", httppprof.Handler("threadcreate").ServeHTTP)
		debug.Get("/pprof/profile", httppprof.Profile)
		debug.Get("/pprof/symbol", httppprof.Symbol)
		debug.Post("/pprof/symbol", httppprof.Symbol)
		debug.Get("/pprof/trace", httppprof.Trace)
		debug.Get("/pprof/cmdline", func(w http.ResponseWriter, _ *http.Request) {
			binary, err := os.Executable()
			if err != nil {
				http.Error(w, "binary name unavailable", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(filepath.Base(binary) + "\n"))
		})
		debug.Get("/vars", func(w http.ResponseWriter, _ *http.Request) {
			values := map[string]any{"cmdline": filepath.Base(os.Args[0])}
			expvar.Do(func(v expvar.KeyValue) {
				if v.Key == "cmdline" {
					return
				}
				var value any
				if json.Unmarshal([]byte(v.Value.String()), &value) == nil {
					values[v.Key] = value
				} else {
					values[v.Key] = v.Value.String()
				}
			})
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(values)
		})
		debug.Get("/memory", func(w http.ResponseWriter, _ *http.Request) {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(stats)
		})
		debug.Get("/goroutines", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			stacks := make([]byte, 64*1024)
			n := runtime.Stack(stacks, true)
			_, _ = fmt.Fprintf(w, "goroutines=%s\n%s", strconv.Itoa(runtime.NumGoroutine()), stacks[:n])
		})
		debug.Get("/config", func(w http.ResponseWriter, _ *http.Request) {
			cfg := deps.currentConfig()
			if cfg == nil {
				writeDebugJSON(w, map[string]any{"available": false})
				return
			}
			body, err := json.Marshal(cfg)
			if err != nil {
				http.Error(w, "configuration unavailable", http.StatusInternalServerError)
				return
			}
			var value any
			if json.Unmarshal(body, &value) != nil {
				http.Error(w, "configuration unavailable", http.StatusInternalServerError)
				return
			}
			writeDebugJSON(w, redactDebugConfig(value))
		})
		debug.Get("/routes", func(w http.ResponseWriter, _ *http.Request) {
			routes := []string{}
			_ = chi.Walk(r, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
				routes = append(routes, method+" "+route)
				return nil
			})
			writeDebugJSON(w, routes)
		})
		debug.Get("/cache", func(w http.ResponseWriter, req *http.Request) {
			stats := map[string]any{"driver": cacheDriver, "stats_available": false}
			if source, ok := cacheStore.(interface {
				Stats(context.Context) (int, int64, error)
			}); ok {
				ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
				defer cancel()
				if entries, bytes, err := source.Stats(ctx); err == nil {
					stats["entries"] = entries
					stats["bytes"] = bytes
					stats["stats_available"] = true
				}
			} else if counted, ok := cacheStore.(interface{ Len() int }); ok {
				stats["entries"] = counted.Len()
			}
			writeDebugJSON(w, stats)
		})
		debug.Get("/db", func(w http.ResponseWriter, req *http.Request) {
			if deps.sqlDB == nil {
				writeDebugJSON(w, map[string]string{"status": "unavailable"})
				return
			}
			ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
			defer cancel()
			if err := deps.sqlDB.PingContext(ctx); err != nil {
				writeDebugJSON(w, map[string]string{"status": "error"})
				return
			}
			writeDebugJSON(w, map[string]string{"status": "ok"})
		})
		debug.Get("/scheduler", func(w http.ResponseWriter, _ *http.Request) {
			status := "unavailable"
			if scheduler != nil {
				status = "running"
				if !scheduler.Healthy() {
					status = "stopped"
				}
			}
			result := map[string]any{"status": status}
			if listing, ok := scheduler.(interface {
				List(context.Context) ([]db.SchedulerTask, error)
			}); ok {
				if tasks, err := listing.List(context.Background()); err == nil {
					result["tasks"] = tasks
				}
			}
			writeDebugJSON(w, result)
		})
		debug.Get("/error500", func(w http.ResponseWriter, req *http.Request) {
			sendError(w, req, apperr.New(apperr.CodeServerError))
		})
	})
}

func (d *deps) debugAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if mode.IsDebugEnabled() && (req.URL.Path == "/debug" || strings.HasPrefix(req.URL.Path, "/debug/")) {
			token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
			if !ok || d.operatorTok == "" || len(token) != len(d.operatorTok) || subtle.ConstantTimeCompare([]byte(token), []byte(d.operatorTok)) != 1 {
				sendError(w, req, apperr.New(apperr.CodeUnauthorized))
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

func writeDebugJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func redactDebugConfig(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			lower := strings.ToLower(key)
			if lower == "webhooks" || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "private_key") || strings.Contains(lower, "api_key") {
				current[key] = "[redacted]"
			} else if lower == "url" {
				if raw, ok := child.(string); ok {
					current[key] = redactDebugURL(raw)
				} else {
					current[key] = redactDebugConfig(child)
				}
			} else {
				current[key] = redactDebugConfig(child)
			}
		}
	case []any:
		for i := range current {
			current[i] = redactDebugConfig(current[i])
		}
	case string:
		return redactDebugURL(current)
	}
	return value
}

func redactDebugURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.User != nil {
		parsed.User = url.User("[redacted]")
	}
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "password") || strings.Contains(lower, "passwd") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "auth") || strings.Contains(lower, "credential") || strings.Contains(lower, "api_key") {
			query.Set(key, "[redacted]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
