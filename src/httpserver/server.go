// Server wires the chi router, the PART 12 middleware chain, and the
// PART 13 health endpoints into a *http.Server with graceful shutdown.
package httpserver

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/geoip"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/server"
)

// Server wraps the chi router and the underlying *http.Server.
type Server struct {
	httpServer *http.Server
	stats      *Stats
	blocks     *BlockStore
	allowlist  *AllowlistLookup
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
	// TLSConfig, when non-nil, makes Start serve HTTPS (AI.md PART 15
	// "Built-in Let's Encrypt Support") instead of plain HTTP. Built via
	// src/certmgr.NewTLSConfig using the FQDN resolved from src/fqdn.
	TLSConfig *tls.Config
	// GeoIP is the shared lookup manager for country blocking (PART 12
	// geoIPMiddleware) and click-analytics country/region enrichment (PART
	// 19). May be nil (e.g. server.geoip.enabled: false), in which case both
	// consumers fail open per AI.md PART 19's risk-signal rule.
	GeoIP *geoip.Manager
	// Metrics is nil when server.metrics.enabled is false (AI.md PART 20).
	// When set, RegisterMetricsRoutes mounts /server/metrics and its
	// aliases and the middleware chain records HTTP/rate-limit/auth
	// metrics.
	Metrics *metrics.Metrics
	// ConfigDir is the resolved config directory (AI.md PART 4). The PGP
	// public key served at /.well-known/pgp-key.asc lives under it.
	ConfigDir string
	// AuditLog receives the PART 11 security/compliance audit events. May
	// be nil, in which case those events are dropped rather than failing
	// the request.
	AuditLog *applog.AuditLogger
	// InstallSecret is the persisted `installation_secret` used to derive
	// the rotating {security_id} in security.txt (AI.md PART 11). It is a
	// Tier 1 value: never logged, never rendered anywhere but as the HMAC
	// input.
	InstallSecret string
}

// New builds a Server ready for Start. Listen address is
// cfg.Server.Listen:cfg.Server.Port; request/response limits come from
// cfg.Server.Limits (invalid values were already normalized by
// config.Validate before this is called).
func New(opts Options) *Server {
	cfg := opts.Config
	stats := NewStats()

	resolver := NewProxyResolver(cfg.Server.TrustedProxies.Additional)
	allowlist := NewAllowlistLookup(cfg.Server.Security.Allowlist)
	blocks := NewBlockStore(cfg.Server.Security.BlockedIPs, opts.AuditLog)
	d := &deps{
		resolver:    resolver,
		rateLimiter: NewRateLimiter(cfg.Server.RateLimit),
		stats:       stats,
		access:      opts.AccessLog,
		operatorTok: cfg.Server.Token,
		cors:        cfg.Server.CORS,
		csrf:        cfg.Server.CSRF,
		geo:         opts.GeoIP,
		geoCfg:      cfg.Server.GeoIP,
		metrics:     opts.Metrics,
		cfgHeaders:  cfg.Web.Headers,
		headers:     newHeaderDeps(cfg, resolver),
		privacy:     &privacyDeps{cfg: cfg, resolver: resolver, audit: opts.AuditLog},
		allowlist:   allowlist,
		blocks:      blocks,
		abuse:       NewAbuseDetector(cfg.Server.Security.AbuseDetection, cfg.Server.RateLimit.Read.Requests),
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

	// /server/healthz itself is registered below by
	// fd.registerFrontendRoutes, which negotiates HTML for browsers and
	// falls back to hd.healthHandler()'s existing JSON/text behavior for
	// every other client — see frontend.go's healthzHTMLHandler.
	if cfg.Server.Healthz.Root.Enabled {
		r.Get("/healthz", hd.healthHandler())
	}

	ld := &linkDeps{sqlDB: opts.DB, resolver: resolver, log: opts.AccessLog, geo: opts.GeoIP}

	rd := newReportDeps(cfg, resolver, opts.AuditLog)

	r.Route("/api", func(api chi.Router) {
		api.Use(corsAPIMiddleware)
		api.Get("/healthz", hd.healthHandler())
		api.Route("/{api_version}", func(v chi.Router) {
			v.Get("/server/healthz", hd.healthHandler())
			RegisterVersionedMetricsRoutes(v, cfg.Server.Metrics, opts.Metrics, opts.AccessLog)
			rd.registerReportRoutes(v)
			ld.registerLinkAPIRoutes(v)
		})
	})
	RegisterMetricsRoutes(r, cfg.Server.Metrics, opts.Metrics, opts.AccessLog)

	wk := &wellKnownDeps{
		cfg:           cfg,
		resolver:      resolver,
		dataDir:       opts.DataDir,
		configDir:     opts.ConfigDir,
		installSecret: opts.InstallSecret,
	}
	wk.registerWellKnownRoutes(r)

	fd := &frontendDeps{
		cfg:           cfg,
		version:       opts.Version,
		commitID:      opts.CommitID,
		buildDate:     opts.BuildDate,
		ld:            ld,
		resolver:      resolver,
		installSecret: opts.InstallSecret,
		audit:         opts.AuditLog,
		configDir:     opts.ConfigDir,
	}
	fd.registerFrontendRoutes(r, hd, ld)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(mustSubFS(server.StaticFS, "static")))))

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
		stats:     stats,
		blocks:    blocks,
		allowlist: allowlist,
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      limitedHandler,
			ReadTimeout:  readTimeout,
			WriteTimeout: writeTimeout,
			IdleTimeout:  idleTimeout,
			TLSConfig:    opts.TLSConfig,
		},
	}
}

// Start begins serving and blocks until the listener stops (normally via
// Shutdown). It returns nil on a clean shutdown, per net/http.Server's
// ErrServerClosed contract. When the server was built with a non-nil
// Options.TLSConfig, it serves HTTPS (AI.md PART 15); the empty cert/key
// path arguments are required by ListenAndServeTLS's signature but unused
// since TLSConfig.GetCertificate supplies certificates dynamically.
func (s *Server) Start() error {
	var err error
	if s.httpServer.TLSConfig != nil {
		err = s.httpServer.ListenAndServeTLS("", "")
	} else {
		err = s.httpServer.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("httpserver: listen %s: %w", s.httpServer.Addr, err)
	}
	return nil
}

// Shutdown gracefully stops the server, waiting up to 30s for in-flight
// requests to finish, per the AI.md PART 8 "Shutdown Timeouts" table.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// ReleaseExpiredBlocks drops every temporary IP block whose window has
// passed, plus any whose address is now allowlisted, and returns how many
// were released, per AI.md PART 11 "IP Block Management" -> "Auto-Release"
// ("checked every minute by scheduler" — the ip_block_release task in
// src/main.go drives it).
//
// Enforcement never depends on this running: BlockStore.Blocked treats an
// expired entry as released at read time, so a missed sweep can only cost
// memory, never keep an address blocked past its window.
func (s *Server) ReleaseExpiredBlocks() int {
	if s.blocks == nil {
		return 0
	}
	return s.blocks.ReleaseExpired(time.Now()) + s.blocks.ReleaseAllowlisted(s.allowlist)
}

// Addr returns the configured listen address.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// mustSubFS returns the dir subtree of embedFS, per AI.md PART 7 "Embedded
// Assets": the embed directive keeps the "static/" prefix, but
// http.FileServer must be rooted at the directory's own contents so
// "/static/css/x.css" maps to "css/x.css" inside the FS. Panics only if
// the embed itself is malformed (dir missing), which would already be a
// build-time failure — see src/server/embed.go.
func mustSubFS(embedFS fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(embedFS, dir)
	if err != nil {
		panic(fmt.Sprintf("httpserver: embedded %q missing: %v", dir, err))
	}
	return sub
}
