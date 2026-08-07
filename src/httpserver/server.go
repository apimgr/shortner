// Server wires the chi router, the PART 12 middleware chain, and the
// PART 13 health endpoints into a *http.Server with graceful shutdown.
package httpserver

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

// Server wraps the chi router and the underlying *http.Server.
type Server struct {
	httpServer *http.Server
	stats      *Stats
}

// Options configures New.
type Options struct {
	Config    *config.Config
	DB        *sql.DB
	DataDir   string
	AccessLog *applog.Logger
	Version   string
	CommitID  string
	BuildDate string
	StartTime time.Time
}

// New builds a Server ready for Start. Listen address is
// cfg.Server.Listen:cfg.Server.Port; request/response limits come from
// cfg.Server.Limits (invalid values were already normalized by
// config.Validate before this is called).
func New(opts Options) *Server {
	cfg := opts.Config
	stats := NewStats()

	resolver := NewProxyResolver(cfg.Server.TrustedProxies.Additional)
	d := &deps{
		resolver:    resolver,
		rateLimiter: NewRateLimiter(cfg.Server.RateLimit),
		stats:       stats,
		access:      opts.AccessLog,
		operatorTok: cfg.Server.Token,
	}

	r := chi.NewRouter()
	if cfg.Server.Compression.Enabled {
		r.Use(chimw.Compress(cfg.Server.Compression.Level, cfg.Server.Compression.Types...))
	}

	hd := &healthDeps{
		sqlDB:     opts.DB,
		dataDir:   opts.DataDir,
		startTime: opts.StartTime,
		stats:     stats,
		version:   opts.Version,
		commit:    opts.CommitID,
		buildDate: opts.BuildDate,
	}

	r.Get("/server/healthz", hd.healthHandler())
	r.Get("/api/{api_version}/server/healthz", hd.healthHandler())
	r.Get("/api/healthz", hd.healthHandler())
	if cfg.Server.Healthz.Root.Enabled {
		r.Get("/healthz", hd.healthHandler())
	}
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		apperr.SendError(w, apperr.New(apperr.CodeNotFound))
	})

	handler := d.setupMiddleware(r)

	readTimeout, _ := config.ParseDuration(cfg.Server.Limits.ReadTimeout, 30*time.Second)
	writeTimeout, _ := config.ParseDuration(cfg.Server.Limits.WriteTimeout, 30*time.Second)
	idleTimeout, _ := config.ParseDuration(cfg.Server.Limits.IdleTimeout, 120*time.Second)
	maxBodySize, _ := config.ParseSize(cfg.Server.Limits.MaxBodySize, 10<<20)

	limitedHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, maxBodySize)
		handler.ServeHTTP(w, req)
	})

	addr := fmt.Sprintf("%s:%s", cfg.Server.Listen, cfg.Server.Port)
	return &Server{
		stats: stats,
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      limitedHandler,
			ReadTimeout:  readTimeout,
			WriteTimeout: writeTimeout,
			IdleTimeout:  idleTimeout,
		},
	}
}

// Start begins serving and blocks until the listener stops (normally via
// Shutdown). It returns nil on a clean shutdown, per net/http.Server's
// ErrServerClosed contract.
func (s *Server) Start() error {
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("httpserver: listen %s: %w", s.httpServer.Addr, err)
	}
	return nil
}

// Shutdown gracefully stops the server, waiting up to 10s for in-flight
// requests to finish.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}
