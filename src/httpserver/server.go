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
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/geoip"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/notify"
)

// Server wraps the chi router and the underlying *http.Server.
type Server struct {
	httpServer *http.Server
	stats      *Stats
	blocks     *BlockStore
	allowlist  *AllowlistLookup
	resolver   *ProxyResolver
	// overlayServers tracks the AI.md PART 31 overlay backend listeners
	// (Tor, I2P) started by ServeOverlay so Shutdown can stop them with the
	// clearnet listener rather than leaving onion traffic being served by a
	// server that is otherwise going away.
	overlayMu      sync.Mutex
	overlayServers []*http.Server
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
	InstallSecret         string
	PreviousInstallSecret string
	// Notifier delivers the AI.md PART 17 email events raised from the
	// HTTP layer: the PART 11 security-report maintainer notification and
	// researcher acknowledgment, and the PART 16 contact-form relay. Nil
	// (no working SMTP) means nothing is ever sent — PART 17's "No SMTP =
	// no emails" rule, with no queue and no "would have sent" log line.
	Notifier *notify.Notifier
	// Tor and I2P report the AI.md PART 31 overlay networks to the health
	// endpoint. Both may be nil — no tor binary on the host, or I2P left
	// opt-out — in which case that network reads as disabled and its
	// checks.* entry is omitted entirely.
	Tor TorReporter
	I2P I2PReporter
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
		sqlDB:       opts.DB,
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
		notifier:    opts.Notifier,
		i18n:        cfg.Server.I18N,
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
		geoip:     opts.GeoIP != nil,
		tor:       opts.Tor,
		i2p:       opts.I2P,
	}

	// /server/healthz itself is registered below by
	// fd.registerFrontendRoutes, which negotiates HTML for browsers and
	// falls back to hd.healthHandler()'s existing JSON/text behavior for
	// every other client — see frontend.go's healthzHTMLHandler.
	if cfg.Server.Healthz.Root.Enabled {
		r.Get("/healthz", hd.healthHandler())
	}

	ld := &linkDeps{sqlDB: opts.DB, resolver: resolver, cfg: cfg, log: opts.AccessLog, geo: opts.GeoIP}
	if tokenKey, err := config.DecodeEncryptionKey(cfg); err == nil {
		ld.tokenKey = tokenKey
	}

	rd := newReportDeps(cfg, resolver, opts.AuditLog)
	fd := &frontendDeps{
		cfg:                   cfg,
		version:               opts.Version,
		commitID:              opts.CommitID,
		buildDate:             opts.BuildDate,
		ld:                    ld,
		resolver:              resolver,
		installSecret:         opts.InstallSecret,
		previousInstallSecret: opts.PreviousInstallSecret,
		audit:                 opts.AuditLog,
		configDir:             opts.ConfigDir,
		notifier:              opts.Notifier,
	}

	r.Route("/api", func(api chi.Router) {
		api.Use(corsAPIMiddleware)
		api.Use(apiResponseFormatMiddleware)
		api.Get("/healthz", hd.healthHandler())
		registerCLIUpdateRoutes(api, cfg, opts.Version)
		api.Method(http.MethodGet, "/swagger", openAPIHandler(fd))
		api.Method(http.MethodPost, "/graphql", graphQLHandler(ld))
		api.Route("/{api_version}", func(v chi.Router) {
			v.Get("/server/healthz", hd.healthHandler())
			v.Method(http.MethodGet, "/server/swagger", openAPIHandler(fd))
			v.Method(http.MethodPost, "/server/graphql", graphQLHandler(ld))
			v.Get("/server/preferences", fd.preferenceAPISnapshotHandler)
			v.Get("/server/auth/check", authCheckHandler)
			v.Post("/server/preferences", fd.preferenceAPISaveHandler)
			v.Get("/server/preferences/export", fd.preferenceExportHandler)
			v.Get("/server/preferences/import", fd.preferenceImportHandler)
			v.Get("/server/preferences/tokens", fd.tokenAPISnapshotHandler)
			v.Post("/server/preferences/tokens", fd.tokenAPICreateHandler)
			v.Post("/server/preferences/tokens/primary/reveal", fd.tokenAPIActionHandler)
			v.Post("/server/preferences/tokens/primary/rotate", fd.tokenAPIActionHandler)
			v.Post("/server/preferences/tokens/{id}/reveal", fd.tokenAPIActionHandler)
			v.Post("/server/preferences/tokens/{id}/rotate", fd.tokenAPIActionHandler)
			v.Patch("/server/preferences/tokens/{id}", fd.tokenAPIActionHandler)
			v.Delete("/server/preferences/tokens/{id}", fd.tokenAPIActionHandler)
			v.Get("/server/security/policy", fd.securityPolicyAPIHandler)
			v.Get("/server/dpo", fd.dpoAPIHandler)
			RegisterVersionedMetricsRoutes(v, cfg.Server.Metrics, opts.Metrics, opts.AccessLog)
			rd.registerReportRoutes(v)
			ld.registerLinkAPIRoutes(v)
		})
	})
	registerCLIBinaryRoute(r, cfg)
	RegisterMetricsRoutes(r, cfg.Server.Metrics, opts.Metrics, opts.AccessLog)

	wk := &wellKnownDeps{
		cfg:                   cfg,
		resolver:              resolver,
		dataDir:               opts.DataDir,
		configDir:             opts.ConfigDir,
		installSecret:         opts.InstallSecret,
		previousInstallSecret: opts.PreviousInstallSecret,
	}
	wk.registerWellKnownRoutes(r)

	(&sitemapDeps{cfg: cfg, resolver: resolver, startTime: opts.StartTime, sqlDB: opts.DB, configDir: opts.ConfigDir}).registerSitemapRoutes(r)
	registerPWARoutes(r, opts.Version, opts.CommitID)

	fd.registerFrontendRoutes(r, hd, ld)
	r.Get("/server/preferences", fd.preferencesHandler)
	r.Post("/server/preferences", fd.preferencesHandler)
	r.Get("/server/preferences/export", fd.preferenceExportHandler)
	r.Get("/server/preferences/import", fd.preferenceImportHandler)
	r.Get("/server/preferences/tokens", fd.tokenPageHandler)
	r.Post("/server/preferences/token/restore", fd.tokenRestoreHandler)
	r.Post("/server/preferences/token/reveal", fd.tokenPrimaryRevealHandler)
	r.Post("/server/preferences/token/rotate", fd.tokenPrimaryRotateHandler)
	r.Post("/server/preferences/token/forget", fd.tokenForgetHandler)
	r.Post("/server/preferences/tokens", fd.tokenCreateHandler)
	r.Post("/server/preferences/tokens/{id}/reveal", fd.tokenLinkedRevealHandler)
	r.Post("/server/preferences/tokens/{id}/rotate", fd.tokenLinkedRotateHandler)
	r.Post("/server/preferences/tokens/{id}/update", fd.tokenLinkedUpdateHandler)
	r.Post("/server/preferences/tokens/{id}/revoke", fd.tokenLinkedRevokeHandler)

	r.Handle("/static/*", staticAssetHandler(assetStamp(opts.Version, opts.CommitID)))

	// AI.md PART 30: the WebUI's JavaScript reads the same embedded locale
	// files the server renders from, so a dynamic string never disagrees
	// with a server-rendered one.
	r.Get("/locales/{lang}.json", localesHandler)
	r.Head("/locales/{lang}.json", localesHandler)

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		sendError(w, req, apperr.New(apperr.CodeNotFound))
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
		resolver:  resolver,
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      buildStampMiddleware(assetStamp(opts.Version, opts.CommitID), limitedHandler),
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
	s.shutdownOverlays(ctx)
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
