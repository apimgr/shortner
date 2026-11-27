// Server wires the chi router, the PART 12 middleware chain, and the
// PART 13 health endpoints into a *http.Server with graceful shutdown.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/blocklist"
	"github.com/apimgr/shortner/src/cache"
	"github.com/apimgr/shortner/src/common/netutil"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/geoip"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/notify"
	"github.com/apimgr/shortner/src/routecontract"
	"github.com/apimgr/shortner/src/security"
	"github.com/apimgr/shortner/src/swagger"
	"github.com/apimgr/shortner/src/tor"
)

// Server wraps the chi router and the underlying *http.Server.
type Server struct {
	httpServer            *http.Server
	servers               []*http.Server
	portErr               error
	apiRoutes             []registeredAPIRoute
	listeners             map[string]net.Listener
	stats                 *Stats
	blocks                *BlockStore
	allowlist             *AllowlistLookup
	resolver              *ProxyResolver
	contactDeps           *frontendDeps
	contactWatcherCancel  context.CancelFunc
	contactWatcherDone    chan struct{}
	brandingWatcherCancel context.CancelFunc
	brandingWatcherDone   chan struct{}
	brandingState         *brandingStateStore
	runtimeDeps           *deps
	// overlayServers tracks the AI.md PART 31 overlay backend listeners
	// (Tor, I2P) started by ServeOverlay so Shutdown can stop them with the
	// clearnet listener rather than leaving onion traffic being served by a
	// server that is otherwise going away.
	overlayMu      sync.Mutex
	overlayServers []*http.Server
}

// Options configures New.
type Options struct {
	Config      *config.Config
	DB          db.Handle
	Store       *db.Store
	Cache       cache.Cache
	CacheHealth func(context.Context) string
	Scheduler   SchedulerReporter
	DataDir     string
	CacheDir    string
	AccessLog   *applog.Logger
	Version     string
	CommitID    string
	BuildDate   string
	StartTime   time.Time
	// TLSConfig, when non-nil, configures HTTPS for the selected listener or
	// the second listener in an HTTP,HTTPS pair. Built via
	// src/certmgr.NewTLSConfig using the FQDN resolved from src/fqdn.
	TLSConfig *tls.Config
	// BoundListeners contains root-bound sockets for privileged ports. Keys
	// match the configured server Addr and are used after privileges drop.
	BoundListeners    map[string]net.Listener
	ContactConfigPath string
	// HTTPChallengeHandler wraps the normal HTTP handler with ACME HTTP-01
	// challenge handling. It is applied only to the cleartext listener.
	HTTPChallengeHandler func(http.Handler) http.Handler
	// GeoIP is the shared lookup manager for country blocking (PART 12
	// geoIPMiddleware) and click-analytics country/region enrichment (PART
	// 19). May be nil (e.g. server.geoip.enabled: false), in which case both
	// consumers fail open per AI.md PART 19's risk-signal rule.
	GeoIP      *geoip.Manager
	Blocklists *blocklist.Manager
	// Metrics is nil when server.metrics.enabled is false (AI.md PART 20).
	// When set, its handlers are attached to the shared route contract and
	// the middleware chain records HTTP/rate-limit/auth metrics.
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
	SecretKeys            *security.SecretKeyring
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

// New builds a Server ready for Start. cfg.Server.Port accepts one port or
// an HTTP,HTTPS pair; request/response limits come from cfg.Server.Limits
// (invalid values were already normalized by config.Validate before this is
// called).
func New(opts Options) *Server {
	cfg := opts.Config
	secretKeys := opts.SecretKeys
	if secretKeys == nil {
		cookieKey, _ := security.GenerateSecret(32)
		csrfKey, _ := security.GenerateSecret(32)
		secretKeys = security.NewSecretKeyring(cookieKey, nil, csrfKey, nil)
	}
	stats := NewStats()
	if err := stats.AttachDB(opts.DB); err != nil && opts.AccessLog != nil {
		_ = opts.AccessLog.WriteLine(applog.LevelWarn, "health: request statistics persistence unavailable\n")
	}

	resolver := NewProxyResolver(cfg.Server.TrustedProxies.Additional)
	allowlist := NewAllowlistLookup(cfg.Server.Security.Allowlist)
	blocks := NewBlockStore(cfg.Server.Security.BlockedIPs, opts.AuditLog)
	d := &deps{
		sqlDB:           opts.DB,
		store:           opts.Store,
		resolver:        resolver,
		rateLimiter:     newRateLimiter(cfg.Server.RateLimit, opts.Cache, opts.Metrics),
		stats:           stats,
		access:          opts.AccessLog,
		accessLogFormat: cfg.Server.Logs.Access.Format,
		operatorTok:     cfg.Server.Token,
		secretKeys:      secretKeys,
		cors:            cfg.Server.CORS,
		csrf:            cfg.Server.CSRF,
		geo:             opts.GeoIP,
		geoCfg:          cfg.Server.GeoIP,
		metrics:         opts.Metrics,
		cfgHeaders:      cfg.Web.Headers,
		headers:         newHeaderDeps(cfg, resolver),
		privacy:         &privacyDeps{cfg: cfg, resolver: resolver, audit: opts.AuditLog},
		allowlist:       allowlist,
		blocks:          blocks,
		blocklists:      opts.Blocklists,
		abuse:           NewAbuseDetector(cfg.Server.Security.AbuseDetection, cfg.Server.RateLimit.Read.Requests),
		notifier:        opts.Notifier,
		i18n:            cfg.Server.I18N,
	}

	r := chi.NewRouter()
	r.MethodNotAllowed(http.HandlerFunc(mcpMethodNotAllowed))
	brandState := newBrandingStateStore(cfg)

	hd := &healthDeps{
		sqlDB:        opts.DB,
		cacheStatus:  opts.CacheHealth,
		store:        opts.Store,
		scheduler:    opts.Scheduler,
		dataDir:      opts.DataDir,
		startTime:    opts.StartTime,
		stats:        stats,
		version:      opts.Version,
		commit:       opts.CommitID,
		buildDate:    opts.BuildDate,
		geoip:        cfg.Server.GeoIP.Enabled,
		geoipDir:     cfg.Server.GeoIP.Dir,
		geoipDBs:     cfg.Server.GeoIP.Databases,
		geoipManager: opts.GeoIP,
		blocklists:   opts.Blocklists,
		tor:          opts.Tor,
		i2p:          opts.I2P,
		branding:     brandState,
		torExpose:    cfg.Server.Tor.Expose,
		i2pExpose:    cfg.Server.I2P.Expose,
	}

	ld := &linkDeps{sqlDB: opts.DB, resolver: resolver, cfg: cfg, log: opts.AccessLog, geo: opts.GeoIP}
	if tokenKey, err := config.DecodeEncryptionKey(cfg); err == nil {
		ld.tokenKey = tokenKey
		d.tokenKey = tokenKey
	}

	d.headers.branding = brandState
	d.runtimeConfig = brandState
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
		secretKeys:            secretKeys,
		audit:                 opts.AuditLog,
		configDir:             opts.ConfigDir,
		notifier:              opts.Notifier,
		metrics:               opts.Metrics,
		store:                 opts.Store,
		branding:              brandState,
	}
	fd.setContactAntispam(cfg.Server.Contact.Antispam)
	apiHandler := d.setupMiddleware(r)
	apiHandler = d.debugAuthMiddleware(apiHandler)
	if cfg.Server.Compression.Enabled {
		r.Use(chimw.Compress(cfg.Server.Compression.Level, cfg.Server.Compression.Types...))
	}
	registerDebugRoutes(r, d, opts.Scheduler, cfg.Server.Cache.Type, opts.Cache)
	if manager, ok := opts.Tor.(torController); ok {
		registerTorControlRoutes(r, manager, cfg.Server.Tor, tor.Dirs{Config: opts.ConfigDir, Data: opts.DataDir})
	}

	routeRegistry := swagger.RouteRegistry()
	contractHandlers := apiRouteHandlers(fd, hd, ld, rd, cfg, opts, r, apiHandler, routeRegistry)
	for operationID, handler := range contractHandlers {
		if err := routeRegistry.SetHandler(operationID, handler); err != nil {
			panic(err)
		}
	}
	fd.routes = routeRegistry
	if cfg.Server.Healthz.Root.Enabled {
		mountContractRoutes(r, routeRegistry, func(path string) bool { return path == "/healthz" })
	}
	mountContractRoutes(r, routeRegistry, func(path string) bool {
		return path == "/mcp" ||
			strings.HasPrefix(path, "/server/metrics") ||
			(cfg.Server.Metrics.Root.Enabled && strings.HasPrefix(path, "/metrics"))
	})
	r.Route("/cli", func(cli chi.Router) {
		cli.Use(func(next http.Handler) http.Handler {
			return apiResponseFormatMiddlewareWithRegistry(next, routeRegistry)
		})
		mountContractRoutes(cli, routeRegistry, func(path string) bool { return strings.HasPrefix(path, "/cli/binaries/") })
	})
	r.Route("/api", func(api chi.Router) {
		api.Use(func(next http.Handler) http.Handler {
			return apiResponseFormatMiddlewareWithRegistry(next, routeRegistry)
		})
		mountContractRoutes(api, routeRegistry, func(path string) bool {
			return strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/api/{api_version}/")
		})
		api.Route("/{api_version}", func(v chi.Router) {
			mountVersionedAPIRoutes(v, routeRegistry)
		})
	})
	wk := &wellKnownDeps{
		cfg:                   cfg,
		resolver:              resolver,
		dataDir:               opts.DataDir,
		configDir:             opts.ConfigDir,
		installSecret:         opts.InstallSecret,
		previousInstallSecret: opts.PreviousInstallSecret,
		branding:              brandState,
		routes:                routeRegistry,
	}
	wk.registerWellKnownRoutes(r)

	(&sitemapDeps{cfg: cfg, resolver: resolver, startTime: opts.StartTime, sqlDB: opts.DB, configDir: opts.ConfigDir, cacheDir: opts.CacheDir, branding: brandState}).registerSitemapRoutes(r)
	registerPWARoutes(r, opts.Version, opts.CommitID, brandState)

	fd.registerFrontendRoutes(r, hd, ld)
	r.Get("/server/preferences", fd.preferencesHandler)
	r.Post("/server/preferences", fd.preferencesHandler)
	registerPreferencePages(r, fd, cfg)
	r.Post("/server/preferences/token/restore", fd.tokenRestoreHandler)
	r.Post("/server/preferences/token/create", fd.tokenIssueHandler)
	r.Post("/server/preferences/token/reveal", fd.tokenPrimaryRevealHandler)
	r.Post("/server/preferences/token/rotate", fd.tokenPrimaryRotateHandler)
	r.Post("/server/preferences/token/revoke", fd.tokenPrimaryRevokeHandler)
	r.Post("/server/preferences/token/forget", fd.tokenForgetHandler)
	r.Post("/server/preferences/tokens", fd.tokenCreateHandler)
	r.Post("/server/preferences/tokens/{id}/reveal", fd.tokenLinkedRevealHandler)
	r.Post("/server/preferences/tokens/{id}/rotate", fd.tokenLinkedRotateHandler)
	r.Post("/server/preferences/tokens/{id}/update", fd.tokenLinkedUpdateHandler)
	r.Post("/server/preferences/tokens/{id}/revoke", fd.tokenLinkedRevokeHandler)

	r.Get("/static/css/accent.css", fd.accentCSSHandler)
	r.Head("/static/css/accent.css", fd.accentCSSHandler)
	r.Handle("/static/*", staticAssetHandler(assetStamp(opts.Version, opts.CommitID)))

	// AI.md PART 30: the WebUI's JavaScript reads the same embedded locale
	// files the server renders from, so a dynamic string never disagrees
	// with a server-rendered one.
	r.Get("/locales/{lang}.json", localesHandler)
	r.Head("/locales/{lang}.json", localesHandler)
	fd.routeMatch = func(path string) (string, map[string]string, bool) {
		rctx := chi.NewRouteContext()
		if !r.Match(rctx, http.MethodGet, path) {
			return "", nil, false
		}
		params := make(map[string]string, len(rctx.URLParams.Keys))
		for i, key := range rctx.URLParams.Keys {
			params[key] = rctx.URLParams.Values[i]
		}
		return rctx.RoutePattern(), params, true
	}

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		sendError(w, req, apperr.New(apperr.CodeNotFound))
	})

	handler := frontendErrorContextMiddleware(fd)(apiHandler)

	readTimeout, _ := config.ParseDuration(cfg.Server.Limits.ReadTimeout, 30*time.Second)
	writeTimeout, _ := config.ParseDuration(cfg.Server.Limits.WriteTimeout, 30*time.Second)
	idleTimeout, _ := config.ParseDuration(cfg.Server.Limits.IdleTimeout, 120*time.Second)
	maxBodySize, _ := config.ParseSize(cfg.Server.Limits.MaxBodySize, 10<<20)

	limitedHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, maxBodySize)
		handler.ServeHTTP(w, req)
	})
	frontendHandler := frontendTextMiddleware(limitedHandler)

	ports, portErr := parsePortSetting(cfg.Server.Port)
	httpHandler := http.Handler(buildStampMiddleware(assetStamp(opts.Version, opts.CommitID), frontendHandler))
	if opts.HTTPChallengeHandler != nil {
		httpHandler = opts.HTTPChallengeHandler(httpHandler)
	}
	httpHandler = RecoverMiddleware(httpHandler)
	newHTTPServer := func(port string, handler http.Handler, tlsConfig *tls.Config) *http.Server {
		return &http.Server{
			Addr:         net.JoinHostPort(strings.Trim(cfg.Server.Listen, "[]"), port),
			Handler:      handler,
			ReadTimeout:  readTimeout,
			WriteTimeout: writeTimeout,
			IdleTimeout:  idleTimeout,
			TLSConfig:    tlsConfig,
		}
	}
	s := &Server{
		stats:         stats,
		blocks:        blocks,
		allowlist:     allowlist,
		resolver:      resolver,
		portErr:       portErr,
		listeners:     opts.BoundListeners,
		apiRoutes:     collectAPIRoutes(r),
		contactDeps:   fd,
		brandingState: brandState,
		runtimeDeps:   d,
	}
	appHandler := RecoverMiddleware(buildStampMiddleware(assetStamp(opts.Version, opts.CommitID), frontendHandler))
	switch {
	case portErr != nil:
		s.httpServer = newHTTPServer("0", appHandler, nil)
		s.servers = []*http.Server{s.httpServer}
	case len(ports) == 2:
		if opts.TLSConfig == nil {
			s.portErr = fmt.Errorf("server: dual HTTP,HTTPS ports require TLS to be enabled")
			s.httpServer = newHTTPServer(ports[0], httpHandler, nil)
			s.servers = []*http.Server{s.httpServer}
			break
		}
		s.httpServer = newHTTPServer(ports[0], httpHandler, nil)
		s.servers = []*http.Server{s.httpServer, newHTTPServer(ports[1], appHandler, opts.TLSConfig)}
	case len(ports) == 1 && ports[0] == "443" && opts.TLSConfig == nil:
		s.portErr = fmt.Errorf("server: port 443 is HTTPS-only but TLS is unavailable")
		s.httpServer = newHTTPServer(ports[0], appHandler, nil)
		s.servers = []*http.Server{s.httpServer}
	case len(ports) == 1:
		s.httpServer = newHTTPServer(ports[0], appHandler, opts.TLSConfig)
		s.servers = []*http.Server{s.httpServer}
		if opts.TLSConfig != nil && opts.HTTPChallengeHandler != nil {
			// TLS-only mode cannot answer HTTP-01 challenges; TLS-ALPN-01
			// remains available on the configured HTTPS listener.
			s.httpServer.Handler = appHandler
		}
	default:
		s.portErr = fmt.Errorf("server: invalid port configuration")
		s.httpServer = newHTTPServer("0", appHandler, nil)
		s.servers = []*http.Server{s.httpServer}
	}
	if opts.ContactConfigPath != "" {
		s.startContactConfigWatcher(opts.ContactConfigPath, cfg.Server.Database.URL)
		s.startBrandingConfigWatcher(opts.ContactConfigPath, cfg.Server.Database.URL)
	}
	return s
}

// apiRouteHandlers attaches runtime handlers to the API route contracts. The
// route metadata is declared once by swagger.RouteRegistry; this map contains
// only the runtime dependencies needed to serve those contracts.
func apiRouteHandlers(fd *frontendDeps, hd *healthDeps, ld *linkDeps, rd *reportDeps, cfg *config.Config, opts Options, apiRouter, middlewareHandler http.Handler, registry *routecontract.Registry) map[string]http.HandlerFunc {
	graphql := graphQLHandler(ld, apiRouter, registry)
	binaryDir := cfg.CLI.BinaryDownload.Dir
	if binaryDir == "" {
		binaryDir = filepath.Join(opts.DataDir, "cli")
	}
	cli := &cliUpdateDeps{cfg: cfg, version: opts.Version, binaryDir: binaryDir, resolver: fd.resolver, audit: opts.AuditLog}
	cli.tor = opts.Tor
	cli.i2p = opts.I2P
	mcpEndpoint := mcpHandler(fd, middlewareHandler)
	handlers := map[string]http.HandlerFunc{
		"getRootHealth":           hd.healthHandler(),
		"getAPIHealth":            hd.healthHandler(),
		"getCLIAutodiscover":      cli.autodiscoverHandler,
		"headCLIAutodiscover":     cli.autodiscoverHandler,
		"downloadCLIBinary":       cli.binaryHandler,
		"headCLIBinary":           cli.binaryHandler,
		"getOpenAPIAlias":         openAPIHandler(fd).ServeHTTP,
		"executeGraphQLAlias":     graphql.ServeHTTP,
		"handleMCPAlias":          mcpEndpoint.ServeHTTP,
		"handleRootMCP":           mcpEndpoint.ServeHTTP,
		"getHealth":               hd.healthHandler(),
		"getAboutPage":            fd.pageContentAPIHandler("about"),
		"getPrivacyPage":          fd.privacyAPIHandler,
		"getHelpPage":             fd.pageContentAPIHandler("help"),
		"getMCPHelp":              fd.mcpHelpAPIHandler,
		"getTermsPage":            fd.pageContentAPIHandler("terms"),
		"getSecurityInfo":         fd.securityAPIHandler,
		"getSecurityThanks":       fd.securityThanksAPIHandler,
		"handleMCP":               mcpEndpoint.ServeHTTP,
		"getOpenAPI":              openAPIHandler(fd).ServeHTTP,
		"executeGraphQL":          graphql.ServeHTTP,
		"executeGraphQLGet":       graphql.ServeHTTP,
		"getPreferences":          fd.preferenceAPISnapshotHandler,
		"updatePreferences":       fd.preferenceAPISaveHandler,
		"syncPreferences":         fd.preferenceSyncHandler,
		"importSyncedPreferences": fd.preferenceSyncImportHandler,
		"checkAuthorization":      authCheckHandler,
		"listTokens":              fd.tokenAPISnapshotHandler,
		"createToken":             fd.tokenAPICreateHandler,
		"issuePrimaryToken":       fd.tokenAPIIssueHandler,
		"revealPrimaryToken":      fd.tokenAPIActionHandler,
		"rotatePrimaryToken":      fd.tokenAPIActionHandler,
		"revokePrimaryToken":      fd.tokenAPIActionHandler,
		"revealLinkedToken":       fd.tokenAPIActionHandler,
		"rotateLinkedToken":       fd.tokenAPIActionHandler,
		"updateToken":             fd.tokenAPIActionHandler,
		"revokeToken":             fd.tokenAPIActionHandler,
		"getSecurityPolicy":       fd.securityPolicyAPIHandler,
		"getDPO":                  fd.dpoAPIHandler,
		"getContactForm":          fd.contactAPIHandler,
		"submitContactForm":       fd.contactAPIHandler,
		"reportCSP":               rd.handler("csp"),
		"reportNEL":               rd.handler("nel"),
		"reportDefault":           rd.handler("default"),
		"listLinks":               ld.listLinksHandler,
		"createLink":              ld.requireLinkWriteScope(ld.createLinkHandler),
		"getLink":                 ld.getLinkHandler,
		"updateLink":              ld.requireLinkWriteScope(ld.updateLinkHandler),
		"deleteLink":              ld.requireLinkWriteScope(ld.deleteLinkHandler),
		"getLinkStats":            ld.statsHandler,
	}
	if opts.Metrics != nil {
		prom := metricsAuth(cfg.Server.Metrics, cfg.Server.Metrics.Auth.Tokens.Prometheus, promhttp.HandlerFor(opts.Metrics.Registry, promhttp.HandlerOpts{}).ServeHTTP)
		grafana := metricsAuth(cfg.Server.Metrics, cfg.Server.Metrics.Auth.Tokens.Grafana, grafanaDashboardHandler())
		loki := metricsAuth(cfg.Server.Metrics, cfg.Server.Metrics.Auth.Tokens.Loki, lokiHandler(cfg.Server.Metrics, opts.AccessLog))
		handlers["getMetrics"] = prom
		handlers["getPrometheusMetrics"] = prom
		handlers["getGrafanaMetrics"] = grafana
		handlers["getLokiMetrics"] = loki
		handlers["getServerMetrics"] = prom
		handlers["getServerPrometheusMetrics"] = prom
		handlers["getServerGrafanaMetrics"] = grafana
		handlers["getServerLokiMetrics"] = loki
		handlers["getAPIMetrics"] = prom
		handlers["getAPIPrometheusMetrics"] = prom
		handlers["getAPIGrafanaMetrics"] = grafana
		handlers["getAPILokiMetrics"] = loki
		handlers["getRootMetrics"] = prom
		handlers["getRootPrometheusMetrics"] = prom
		handlers["getRootGrafanaMetrics"] = grafana
		handlers["getRootLokiMetrics"] = loki
	}
	handlers["redirectShortLink"] = ld.resolveHandler
	handlers["getLinkStatsPage"] = fd.statsHTMLHandler(ld)
	return handlers
}

func mountContractRoutes(router chi.Router, registry *routecontract.Registry, include func(string) bool) {
	for _, route := range registry.Snapshot() {
		if !include(route.Path) || route.Handler == nil {
			continue
		}
		path := route.Path
		for _, prefix := range []string{"/api", "/cli"} {
			if strings.HasPrefix(path, prefix+"/") {
				path = strings.TrimPrefix(path, prefix)
				break
			}
		}
		router.Method(route.Method, path, route.Handler)
	}
}

// mountVersionedAPIRoutes mounts only routes present in the shared route
// contract. The enclosing /api router supplies API response negotiation and
// this router supplies the {api_version} segment.
func mountVersionedAPIRoutes(router chi.Router, registry *routecontract.Registry) {
	for _, route := range registry.Snapshot() {
		const prefix = "/api/{api_version}"
		if !strings.HasPrefix(route.Path, prefix+"/") {
			continue
		}
		handler := route.Handler
		if handler == nil {
			if strings.HasPrefix(route.Path, prefix+"/server/metrics") {
				continue
			}
			panic("server: route contract has no runtime handler for " + route.OperationID)
		}
		path := strings.TrimPrefix(route.Path, prefix)
		router.Method(route.Method, path, handler)
	}
}

func newRateLimiter(cfg config.RateLimit, backend cache.Cache, m *metrics.Metrics) *RateLimiter {
	if observer, ok := backend.(interface {
		SetLookupObserver(cache.CacheLookupObserver)
	}); ok {
		observer.SetLookupObserver(m)
	}
	if counter, ok := backend.(rateCounter); ok {
		rl := NewRateLimiter(cfg, counter)
		rl.SetMetrics(m)
		return rl
	}
	rl := NewRateLimiter(cfg)
	rl.SetMetrics(m)
	return rl
}

type registeredAPIRoute struct {
	Method string
	Path   string
}

func collectAPIRoutes(r chi.Router) []registeredAPIRoute {
	seen := make(map[registeredAPIRoute]struct{})
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") && !strings.HasPrefix(route, "/cli/") && route != "/healthz" && route != "/metrics" && !strings.HasPrefix(route, "/metrics/") && route != "/server/metrics" && !strings.HasPrefix(route, "/server/metrics/") && route != "/{slug}" && route != "/{slug}/stats" {
			return nil
		}
		seen[registeredAPIRoute{Method: strings.ToLower(method), Path: route}] = struct{}{}
		return nil
	})
	result := make([]registeredAPIRoute, 0, len(seen))
	for route := range seen {
		result = append(result, route)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path == result[j].Path {
			return result[i].Method < result[j].Method
		}
		return result[i].Path < result[j].Path
	})
	return result
}

func parsePortSetting(value string) ([]string, error) {
	parts := strings.Split(strings.TrimSpace(value), ",")
	if len(parts) < 1 || len(parts) > 2 {
		return nil, fmt.Errorf("server: port must be one number or an HTTP,HTTPS pair")
	}
	ports := make([]string, len(parts))
	for i, part := range parts {
		part = strings.TrimSpace(part)
		port, err := strconv.Atoi(part)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("server: invalid port %q", part)
		}
		ports[i] = strconv.Itoa(port)
	}
	return ports, nil
}

// Start begins serving and blocks until the listener or listeners stop
// (normally via Shutdown). A configured HTTP,HTTPS pair starts both at once;
// a TLS-enabled single listener serves HTTPS. Certificates are supplied
// dynamically through TLSConfig.GetCertificate.
func (s *Server) Start() error {
	if s.portErr != nil {
		return s.portErr
	}
	if len(s.servers) == 1 {
		return serve(s.servers[0], s.listeners[s.servers[0].Addr])
	}
	errCh := make(chan error, len(s.servers))
	for _, srv := range s.servers {
		go func(server *http.Server) { errCh <- serve(server, s.listeners[server.Addr]) }(srv)
	}
	firstErr := <-errCh
	if firstErr != nil {
		_ = s.shutdownServers()
		return firstErr
	}
	for range s.servers[1:] {
		if err := <-errCh; err != nil {
			_ = s.shutdownServers()
			return err
		}
	}
	return nil
}

func serve(server *http.Server, listener net.Listener) error {
	var err error
	if listener == nil {
		var fallback bool
		listener, fallback, err = netutil.ListenTCP(server.Addr)
		if err == nil && fallback {
			warnIPv4ListenerFallback()
		}
		if err == nil {
			server.Addr = listener.Addr().String()
		}
	}
	if err == nil && listener != nil && server.TLSConfig != nil {
		err = server.ServeTLS(listener, "", "")
	} else if err == nil && listener != nil {
		err = server.Serve(listener)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server: listen %s: %w", server.Addr, err)
	}
	return nil
}

var ipv4ListenerFallbackWarning sync.Once

func warnIPv4ListenerFallback() {
	ipv4ListenerFallbackWarning.Do(func() {
		fmt.Fprintln(os.Stderr, "shortner: warning: IPv6 is unavailable; listening on IPv4 address 0.0.0.0")
	})
}

// Shutdown gracefully stops the server, waiting up to 30s for in-flight
// requests to finish, per the AI.md PART 8 "Shutdown Timeouts" table.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.shutdownOverlays(ctx)
	if s.contactWatcherCancel != nil {
		s.contactWatcherCancel()
		<-s.contactWatcherDone
	}
	if s.brandingWatcherCancel != nil {
		s.brandingWatcherCancel()
		<-s.brandingWatcherDone
	}
	var errs []error
	for _, server := range s.servers {
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	if err := s.stats.Stop(ctx); err != nil {
		errs = append(errs, fmt.Errorf("flush request statistics: %w", err))
	}
	return errors.Join(errs...)
}

func (s *Server) shutdownServers() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var errs []error
	for _, server := range s.servers {
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
		panic(fmt.Sprintf("server: embedded %q missing: %v", dir, err))
	}
	return sub
}
