# TODO.AI.md

AI-owned task list for `shortner`. PART 0-6 bootstrap (directory layout,
project files, build system, dependencies, config, metadata) is complete.
Everything below is deferred work, in dependency order.

## Foundation follow-ups (small, do first)

- `src/paths/` directory name is plural; project convention (global
  CLAUDE.md, Go section) requires singular directory names (`path/`) to
  match package naming. Flagged by go-lint during the PART 14-15 review.
  Renaming requires updating every import site across the tree — deferred
  as a standalone mechanical rename rather than folded into an unrelated
  feature commit.
  Read: global CLAUDE.md "Directory naming is language-specific"
- Windows admin-token detection in `src/paths/paths_windows.go` is a
  minimal probe (`\\.\PHYSICALDRIVE0` open test) — verify against real
  Windows UAC behavior and harden if needed.
  Read: AI.md PART 4
- `go.sum`/LICENSE.md third-party attribution section is currently a
  placeholder — now that `modernc.org/sqlite` and `golang.org/x/crypto`
  are real dependencies (added for PART 10/11), populate the attribution
  section with their licenses; still pending an HTTP router if one is
  added later.
  Read: AI.md PART 2, 10

## PART 7: Binary requirements — DONE

- Single static binary: `CGO_ENABLED=0` in every Makefile target and the
  Docker toolchain build; pure-Go dependencies only (`modernc.org/sqlite`
  etc.).
- Default behavior: no-args init+start, first-run `server.yml` +
  directories + banner, PID file — all implemented in `src/main.go`.
- Embedded assets: `src/server/embed.go` (`//go:embed all:template`,
  `all:static`), `src/data/embed.go` (`//go:embed all:*`, currently just
  `.gitkeep` — no later PART has defined concrete `src/data/*.json`
  schemas yet, so this stays a placeholder until one does).
- External Data (GeoIP/blocklists/CVE/Trivy) is explicitly PART 18/19
  scheduler work, not PART 7 — tracked under "PART 17-22: Features" below.
- Display Environment Detection: `src/common/display/detect.go`,
  `detect_unix.go`, `detect_windows.go` — `DisplayMode`/`DisplayEnv`/
  `DetectDisplayEnv`/`autoDetectDisplayMode`/`IsDumbTerminal`/`CanUseANSI`,
  plus `NewSpinner`/`TextSpinner`/`ANSISpinner`/`ShowProgress`
  (`src/common/display/spinner.go`) for the TERM=dumb fallback behavior.
- Terminal Package: `src/common/terminal/size.go` — `SizeMode` breakpoints
  and `ShowASCIIArt`/`ShowBorders`/`ShowSidebar`/`ShowIcons` helpers,
  matching `calculateMode`'s thresholds exactly.
- Theme Package: `src/common/theme/colors.go` (`ThemePalette`,
  `ThemePaletteDark`/`Light`, `TerminalPalette`, Dracula-based per
  IDEA.md) plus `src/common/theme/detect.go`
  (`GetThemePalette`/`IsSystemDarkTheme`) — `IsSystemDarkTheme` is a
  best-effort COLORFGBG heuristic with no CLI/TUI consumer yet (PART 32);
  see the doc comment for the platform-API gap this leaves for a future
  CLI build.
- Banner Package: `src/common/banner/banner.go` —
  `PrintStartupBanner`/full/compact/minimal/micro variants.
- Signal handling: `src/signal` installs SIGTERM/SIGINT shutdown handlers
  (plus SIGQUIT/SIGRTMIN+3, SIGUSR1 log-reopen, SIGUSR2 status-dump on
  Unix); SIGHUP is explicitly ignored — config reload is a file-watcher
  concern, not a signal, per PART 8 "Smart Config Reload". `src/main.go`'s
  `run()` blocks on `startHTTPServer(srv)` until a shutdown signal closes
  the HTTP server, DB, access log, and removes the PID file.
- Deferred, no gap: `src/common/terminal/resize.go` (SIGWINCH) and
  `symbols.go` (Unicode/ASCII symbol set) from PART 7's module-structure
  listing have no consumer yet — nothing interactive resizes or renders
  symbols until the CLI/TUI binary (PART 32) exists. Same for
  `src/common/display/mode.go` (its content already lives correctly in
  `detect.go` — no separate file needed) and `src/common/banner/ascii.go`
  (ASCII art is inline in `banner.go`'s size-tiered renderers, no
  standalone generator needed yet). `src/common/theme/css.go` (CSS
  variable generation) is not needed here either — the web frontend's CSS
  custom properties already live in `src/server/static/css/common.css`
  under PART 16, using the same hex values as `ThemePaletteDark`/`Light`.

## PART 9-11: Backend core

**Foundational (non-HTTP-dependent) pieces are done:** `src/apperr`
(canonical error envelope, HTTP status mapping, retry/backoff),
`src/cache` (in-process TTL cache), `src/security` (token gen/hash/
constant-time compare, Argon2id password hashing, IP anonymization,
short-code/slug generation), `src/db` (idempotent SQLite schema,
connection pooling, query timeouts, transaction + serialization-retry
helpers, Link/Click/api_tokens/app_secrets CRUD), `src/applog`
(text/logfmt/apache/nginx/json/syslog/fail2ban/CEF formatters, file
logger, ULID-based JSON-Lines audit logger). All have table-driven tests;
`make test` passes with 77.5% coverage.

- Response-layer wiring (calling `apperr.SendOK`/`SendError` from actual
  HTTP handlers, expired-link 410 Gone response) needs the HTTP server,
  which doesn't exist yet.
  Read: AI.md PART 9, PART 14
- Security Headers, CSP, CORS, Permissions-Policy, Cross-Origin Isolation,
  Privacy Signal Headers, Sec-Fetch-* validation, Reporting API,
  Server-Timing, Well-Known Files (robots.txt/security.txt/llms.txt),
  Security Reports/GPG keypair management, Compliance Standards/routes,
  Abuse Detection middleware, IP Block Management middleware — all
  require `http.Request`/middleware chains that don't exist until the
  HTTP server (PART 12+) is built.
  Read: AI.md PART 11 (sections after "Cryptographic Keys")
- `src/security/slug.go`'s `reservedSlugs` list is a minimal provisional
  set (api, static, health, admin, login, ...), not the canonical list —
  PART 16 hasn't defined the full reserved-names table yet.
  Read: AI.md PART 16
- Click bot/crawler filtering (IDEA.md "Business rules": "Click tracking
  excludes known bot/crawler user agents") needs a UA-classification list
  this project has not defined yet; `db.RecordClick` always records —
  callers must decide whether to call it.
  Read: IDEA.md Business logic
- `src/db` only supports `modernc.org/sqlite`; libsql/Turso remote-DB
  support depends on the full `server.yml` database schema.
  Read: AI.md PART 10, PART 12
- `server.security.encryption_key` (server.yml-stored secret, distinct
  from the three DB-stored secrets in `src/db/secret.go`) isn't wired
  into `src/config` yet — needs the full server.yml schema.
  Read: AI.md PART 11 "Cryptographic Keys", PART 12
- `src/applog.Logger.Rotate()` is a manual, callable rotation only — the
  scheduled daily/weekly/size-based rotation and `keep:` retention
  policies (AI.md PART 11 "Log Rotation" / "Audit Log Retention") need
  the scheduler PART 18 defines.
  Read: AI.md PART 11 "Rotation Options"/"Retention Options", PART 18
- Apply the Tier 1/2/3 Public Endpoint Safety Principle to every new
  endpoint as it's built.
  Read: AI.md PART 11

## PART 12: Server configuration — DONE

- Full `server.yml` schema (`Limits`, `Compression`, `TrustedProxies`,
  `RateLimit`, `CacheConfig`, `Healthz`) plus the Config Validation Rule
  (`src/config/limits.go`) landed in commit 2693120764bb.
- URL & FQDN detection (reverse-proxy headers), Request ID middleware, and
  auth-token header parsing (commit 2693120764bb) are now consumed by
  PART 14's route handlers (`src/httpserver/links.go`) — see PART 14 below.

## PART 13: Health & versioning — DONE

- `/server/healthz`, `/api/{api_version}/server/healthz`, `/api/healthz`,
  and the optional `/healthz` root alias all implemented in
  `src/httpserver/health.go` (commit 2693120764bb).

## PART 14: API structure — DONE

- Core API routes landed in `src/httpserver/links.go`: `POST
  /api/{api_version}/links` (create, auto or custom slug, returns a
  one-time `owner_token`), `GET/PATCH/DELETE
  /api/{api_version}/links/{slug}` (owner/operator-token-gated
  update/delete), `GET /api/{api_version}/links/{slug}/stats`
  (click-analytics sub-resource), plus the public vanity routes `GET
  /{slug}` (redirect, bot-UA-filtered click recording, 410 Gone when
  expired) and `GET /{slug}/stats`.
- `Access-Control-Allow-Origin: *` (`corsAPIMiddleware`) now covers the
  entire `/api` route group in `src/httpserver/server.go`, closing a gap
  where the pre-existing health routes were not CORS-enabled.
- GeoIP-based location fields on `StatsResponse.Recent` (`Country`/`Region`)
  are now populated at click time by the PART 19 `src/geoip` lookup — see
  the PART 19 entry below.

## PART 15: SSL/TLS & Let's Encrypt — DONE (HTTP-01/TLS-ALPN-01 only)

- `src/fqdn` implements FQDN resolution (`GetFQDN`, `DOMAIN` env var →
  hostname → `$HOSTNAME` → global IPv6 → global IPv4 → `localhost`) and
  dev-TLD detection (`IsDevTLD`) per AI.md PART 15.
- `src/certmgr` implements the 4-tier Certificate Lookup Order
  (`/etc/letsencrypt/live/domain/` → `/etc/letsencrypt/live/{fqdn}/` →
  `{config_dir}/ssl/letsencrypt/{fqdn}/` → `{config_dir}/ssl/local/{fqdn}/`),
  CN/SAN + expiry validation, the 7-day `NeedsRenewal` window, and
  `SaveAppManagedCertificate`. `src/certmgr/acme.go` builds a `*tls.Config`
  backed by `golang.org/x/crypto/acme/autocert` (HTTP-01/TLS-ALPN-01) as the
  issuance fallback when no existing certificate is found. Wired into
  `src/httpserver.Options.TLSConfig` / `Server.Start` and
  `src/main.go:buildTLSConfig` (skips TLS entirely for a dev-only TLD).
- Deferred (not implemented — literal spec gaps, tracked here rather than
  silently dropped):
  - DNS-01 provider matrix (`server.tls.dns_provider` /
    `dns_credentials.*` — cloudflare, route53, digitalocean, godaddy,
    namecheap, rfc2136, the full lego provider list). Only HTTP-01/
    TLS-ALPN-01 via autocert exist today; no DNS-01 challenge, so wildcard
    certs are not obtainable yet.
    Read: AI.md PART 15 "DNS-01 Provider Configuration"
  - `credentials_encrypted` (AES-256-GCM at rest for DNS provider
    credentials) — `src/config.TLS.DNSCredentials` is plaintext YAML for
    now; this codebase has no AES-256-GCM/secret-encryption primitive yet
    (`src/security` has Argon2id + SHA-256 only). Needs a new
    `src/security/encrypt.go` (or similar) before DNS-01 can be built.
    Read: AI.md PART 15 "Provider Credential Storage"
  - Autocert-issued certificates are cached in autocert's own opaque
    `DirCache` format (`{config_dir}/ssl/letsencrypt/autocert-cache/`), not
    bridged into the certbot-mirroring `{fullchain,privkey}.pem` layout in
    `AppManagedDir` — `SaveAppManagedCertificate` exists but nothing calls
    it yet after a fresh autocert issuance.
    Read: AI.md PART 15 "Certificate Directory Structure"
  - Daily-03:00-aligned proactive renewal loop — no PART 18 scheduler
    exists yet to hang this off of; `certmgr.NeedsRenewal` is ready to be
    called by it once PART 18 lands.
    Read: AI.md PART 18
  - Dual HTTP+HTTPS simultaneous-port serving and the full responsive
    startup-banner integration (showing both `http://` and `https://` URLs,
    overlay `.onion`/`.i2p` addresses) is not wired — `main.go` currently
    serves either HTTP or HTTPS on one port, never both.
    Read: AI.md PART 15 "Port Configuration", PART 8 "Startup Banner"
  - Tor/I2P overlay network TLS handling (PART 31) not addressed here.
    Read: AI.md PART 31

## PART 16: Web frontend — DONE (core)

- Server-rendered pages implemented: home (`/`, create-link form + POST +
  success state, reuses PART 14's `db.CreateLink*`/`CreateResourceToken`
  rather than duplicating), `/server/{about,privacy,contact,help,terms}`,
  `/server` -> 301 -> `/server/about`, `/server/healthz` (HTML variant,
  falls back to JSON/text via `detectClientType`), `/{slug}/stats` (HTML
  variant, same fallback). Cookie-consent banner + `/server/consent` POST +
  `/server/ccpa` (only when `server.privacy.data.sold`). `common.css`/
  `components.css`/`public.css`, single `static/js/app.js` (vanilla,
  `data-action` only). `/static/*` served from the embedded FS.
  All new handlers have real test coverage (`frontend_test.go`) exercising
  `renderPage()` against the live embedded templates — this is the only
  way `html/template` field-name mismatches surface, since `go vet` does
  not catch them.

## PART 16 (continued): deferred sub-items

- Full PWA support (manifest.json, service worker, offline.html) — not
  implemented this pass; only the create-link page itself was required.
  Read: AI.md PART 16 "PWA"
- `sitemap.xml` — not implemented.
- Remote branding/SEO image fetching + site-verification meta tags — not
  implemented; `head.tmpl` has no dynamic OG/Twitter image logic beyond
  static config fields already present.
- `/favicon.ico` — `head.tmpl`/`home.tmpl` reference it but no icon asset
  or route exists (`find -iname favicon*` finds nothing). Harmless (browsers
  handle a missing favicon gracefully; the home-page logo `<img>` uses
  `alt=""`), but should be added — either a real asset or an explicit
  `/favicon.ico` route — before PART 16 is called fully complete.
- `Web.Announcements` (site-banner) — the config struct
  (`config.WebAnnouncements`/`config.Announcement`) exists and
  `.site-banner`/`.site-banner-info`/`.site-banner-warning` CSS classes are
  ready, but no template renders `cfg.Web.Announcements.Items` yet and no
  handler passes announcement data to `PageData`. Needs wiring: add an
  `Announcements []config.Announcement` (or similar) field to `PageData`,
  populate it in `newPageData`, and add a banner partial to
  `layout/public.tmpl`.
- DONE: GeoIP location data on `/{slug}/stats` — `page/stats.tmpl` renders
  "unknown" gracefully when empty, and now gets real values: PART 19's
  `src/geoip` lookup populates `ClickInfo.Country`/`.Region` at click time.
- Contact-form email delivery — `contactPost` in `frontend.go` accepts and
  validates the form (including a static math-captcha check) and shows a
  success message, but nothing is sent or persisted; real delivery depends
  on PART 17 (SMTP/notifications), documented as a deliberate no-op in the
  function's doc comment.
- `/server/docs/swagger`, `/server/docs/graphql` — confirmed still out of
  scope per the AI.md `/server` routes table (PART 16); no Swagger/GraphQL
  doc UI exists.
- DONE: Home page (`/`) body LAYOUT diffed against the prior Node.js app
  (`github.com/casjaydns/csj.lol`, reference assets formerly at
  `reference/csjlol/`) — `page/home.tmpl` (centered logo, h1 site name,
  tagline, single-column create-link card with url+slug inputs and submit,
  success state with short URL + "Create another") already matched the
  reference's `<main id="app">` structure. The one real gap found was
  `nav.tmpl`'s mobile menu, which was JS-only (`data-action="toggle-nav"`)
  in violation of AI.md PART 16 "CSS-Only Mobile Menu (NO JavaScript)" —
  fixed to a checkbox/label CSS-only pattern. `reference/csjlol/` has been
  deleted; theming was never derived from it (AI.md's own `theme-dark`/
  `theme-light` CSS variables throughout, per IDEA.md).
## PART 17-22: Features

- Email & notifications.
  Read: AI.md PART 17
- DONE: Internal scheduler (`src/scheduler/`, `src/db/scheduler.go`,
  `src/scheduler_cli.go`, wired into `src/main.go` startup/shutdown).
  All 12 AI.md PART 18 "Built-in Tasks (Required)" are registered with
  their default schedules (config-overridable per task via
  `server.scheduler.tasks`); 5 have real implementations
  (`token_cleanup`, `log_rotation`, `healthcheck_self`, `ssl_renewal`,
  `geoip_update`, plus `update_check` from PART 22 and
  `backup_daily`/`backup_hourly` from PART 21); the remaining 4
  (`blocklist_update`, `cve_update`, `tor_health`, `i2p_health`) are
  honest no-op "skipped" stand-ins until their underlying subsystem lands
  (PART 9/11, PART 9, PART 31.1, PART 31.2 respectively) — each is wired
  up to real work as its subsystem's own TODO item below is implemented,
  not here.
  `--scheduler list/show/run/enable/disable/history` CLI dispatch
  implemented in `src/scheduler_cli.go`; startup catch-up window and
  graceful shutdown (drain running tasks, no forced timeout kill yet —
  AI.md's 30s force-release is not implemented, tracked separately)
  implemented in `src/scheduler/scheduler.go`.
  Read: AI.md PART 18
- DONE: GeoIP integration (`src/geoip/geoip.go`) — `Manager` wraps up to 4
  `oschwald/maxminddb-golang` readers (ASN, Country, City IPv4/IPv6) from
  `sapics/ip-location-db` via the jsDelivr CDN (never MaxMind GeoLite2,
  per spec). `Open`/`Lookup`/`Reload`/`Close`/`Download`/`IsBlocked` all
  fail open per the NON-NEGOTIABLE "risk signal only" rule: nil/disabled
  Manager, missing/corrupt DB, and private/loopback IPs (`net.IP.IsPrivate
  ()`/`IsLoopback()`) all skip lookup rather than error or block.
  `Download` writes to a temp file, validates via `maxminddb.Open`, then
  atomically renames into place, so a bad download never corrupts a
  working database. Config: `server.geoip.{enabled,dir,deny_countries,
  allow_countries,databases.{asn,country,city}}` in `src/config/config.go`
  (`Default()`: enabled, all 3 DBs on, both country lists empty). Wired
  into: `httpserver/middleware.go` (`geoIPMiddleware` — country-blocking,
  execution position 8, allowlist/no-country/nil-Manager pass through),
  `httpserver/links.go` (`resolveHandler` looks up the raw IP before
  `db.RecordClick` anonymizes it, populating `Country`/`Region`),
  `scheduler/tasks.go` (`geoip_update` task re-downloads + `Reload()`s),
  `main.go` (dir defaults to `{data_dir}/security/geoip`; `Open` is
  synchronous/fast, first-run `Download` runs in a background goroutine
  with a 5-minute timeout so startup is never blocked on the network;
  `geoManager.Close()` registered as a shutdown hook). CC BY 4.0
  attribution (DB-IP HTML link + NRO text notice) added to
  `page/about.tmpl` (reachable from every screen via nav) and
  `LICENSE.md`'s Third-Party Licenses section.
  Deferred: `server.security.allowlist` bypassing country-blocking is not
  wired — PART 11's allowlist backing store doesn't exist yet, so
  `IsAllowlisted(ctx)` is a permanent pass-through stub; revisit once
  PART 11's allowlist lands.
  Read: AI.md PART 19, IDEA.md Business logic
- Metrics endpoint — DONE. `src/metrics/` (Prometheus registry, HTTP/DB/
  scheduler/system/runtime metrics, instrumented sql driver wrapper),
  `src/httpserver/metrics.go` (`metricsAuth` per-service bearer-token
  check, `RegisterMetricsRoutes`/`RegisterVersionedMetricsRoutes` mounting
  `/server/metrics[/prometheus|grafana|loki]` + `/api/{api_version}/...`
  + `/api/metrics` + root `/metrics` aliases, Grafana dashboard JSON,
  Loki JSON handler), `src/applog/logger.go` (bounded in-memory ring
  buffer + `Recent()` to back the `loki` service). Wired into
  `src/httpserver/server.go`, `src/httpserver/middleware.go` (HTTP/rate-
  limit/auth metrics), `src/scheduler/scheduler.go` (task metrics).
  Deferred sub-items (each an open gap, not silently dropped):
  - Business metrics (`LinksTotal`, `LinksCreated24h`, `LinksClicked24h`,
    `APITokensActive`) are declared but never populated — needs either a
    periodic query against `src/db` or hooks in the link-create/click/
    token code paths.
  - Cache metrics (`Cache*`) are declared but inert — `src/cache` is not
    wired into any request path yet.
  - Tor/I2P metrics deferred to PART 31.
  Read: AI.md PART 20
- Backup & restore — DONE. `src/backup/` (`crypt.go` AES-256-GCM sealed
  under an Argon2id-derived key reusing `src/security`'s existing
  parameters; `archive.go` tar+gzip with manifest.json and a path-
  traversal guard; `backup.go` create/verify/delete-on-failure; `verify.go`
  the full 7-check suite including SQLite integrity check; `retention.go`
  yearly>monthly>weekly>daily tiering with disk-space/threshold checks;
  `restore.go` the authorization table (empty DB/root+confirm/service
  user+token/denied) and atomic file placement; `audit.go` all 8 PART 21
  audit events). Wired into `src/scheduler/backup.go` (`backup_daily`
  8-step flow, `backup_hourly`), `src/backup_cli.go` (interactive
  password prompt — no password flag ever, per spec), `src/maintenance.go`
  (`backup`/`restore` dispatch), `src/main.go` (audit logger + BackupDeps),
  `src/config/config.go`/`limits.go` (`server.backup.*`,
  `server.compliance.enabled`, warn-don't-error validation with the
  spec's exact message/threshold shapes).
  Deferred sub-items (each an open gap, not silently dropped):
  - `--maintenance backup list` / `delete` subcommands — PART 21 shows
    them but the flag parser only passes a single positional arg.
  - Backup metadata table in `server.db` (PART 21 "What Is in server.db"
    lists "Backup metadata"); history currently lives only in the audit
    log and the filenames on disk.
  - `backup.encryption.hint` is parsed and persisted but never surfaced
    at the restore prompt.
  - Restore does not yet stop/restart the running server or take a
    pre-restore safety snapshot.
  - `src/metrics/metrics.go` fails `gofmt -l` (pre-existing, from the
    PART 20 commit, unrelated to this work) — needs a `gofmt -w` pass.
  Read: AI.md PART 21
- `--maintenance mode`, `setup` actions (persist app mode, reset config to
  defaults) — flag surface done in `src/maintenance.go`, actions still
  "not yet available".
  Read: AI.md PART 11, 12
- `--maintenance pgp`, `secret`, `token` actions (PGP key management,
  stored-secret management, operator token management) — flag surface
  done in `src/maintenance.go`, actions still "not yet available".
  Read: AI.md PART 11, 21
- DONE: `--update` self-update command (`src/updater/`, `src/update.go`,
  `src/scheduler/update.go`, `--maintenance update` alias in
  `src/maintenance.go`, `server.update.*` in `src/config/`). Implements
  `check`/`yes`/`branch {stable|beta|daily}`, the cumulative channel
  semantics, the `defer_days` window (scheduled task only), SHA-256
  verified downloads, Unix rename-in-place + Windows
  `MOVEFILE_DELAY_UNTIL_REBOOT` replacement, service-aware restart, and
  the `update_check` task with once-per-version notification.
  Read: AI.md PART 22
- `update_available` email event (AI.md PART 22 "Surfacing rules"). The
  WARN log line, `--update check`, and `--status` surfaces exist; email
  delivery needs PART 17's notification system, which is not built yet.
  Read: AI.md PART 17, PART 22
- `--update` progress reporting during the download. AI.md PART 22's flow
  is implemented end to end, but the download streams silently — a large
  binary on a slow link shows nothing between "Downloading" and
  "Verified". Add byte-count/percentage output once PART 16's shared
  progress display exists.
  Read: AI.md PART 22
- Windows deferred-delete cleanup notice. The replaced `{binary}.old` is
  scheduled for removal at reboot via `MoveFileEx`; nothing tells the
  operator a reboot is what clears it.
  Read: AI.md PART 22

## PART 23-24: Privilege escalation & service

- `--service` start/stop/restart/reload/`--install`/`--uninstall`/
  `--disable` actions (systemd, launchd, SysV, rc.d, Windows service).
  Flag parsing/dispatch/`--help`/service-manager auto-detection
  (`detectServiceManager()` in `src/service.go`) are already implemented;
  each action currently prints "not yet available" and exits 1 —
  implement the real unit-file/plist/service-registration logic here.
  Read: AI.md PART 23, 24

## PART 27: CI/CD workflows

- `.github/workflows/` and `.gitea/workflows/` — security-only workflows
  first, then `ci.yml`/`release.yml` last. Pin all third-party Actions to
  full commit SHAs. Verify each staged workflow with `act --list -W {file}`.
  Read: AI.md PART 27

## PART 28-30: Testing, docs, i18n

- Expand `tests/run_tests.sh` coverage as real packages/handlers are added;
  maintain the 60% coverage gate.
  Read: AI.md PART 28
- `tests/run_tests.sh` lines 11-16 run a raw `docker run ... go test`
  instead of invoking the Makefile's `test` target (found by the go-lint
  agent) — bring it in line with `make test` so there is one source of
  truth for the test invocation.
  Read: AI.md PART 25, 28
- `tests/incus.sh` currently only proves container lifecycle — add real
  systemd/service-install checks once PART 23/24 lands.
  Read: AI.md PART 28
- `docs/index.md`, `installation.md`, `configuration.md`, `api.md`,
  `cli.md`, `security.md`, `integrations.md`, `development.md`,
  `requirements.txt`, `stylesheets/dark.css`, `stylesheets/light.css` —
  directories exist, files do not yet.
  Read: AI.md PART 29
- I18N string extraction and accessibility pass.
  Read: AI.md PART 30

## PART 31: Overlay networks (Tor & I2P)

AI.md PART 31 was retitled and split by the 2026-08-16 spec revision
(commit 575f0957e6bd): 31.1 Tor (REQUIRED, auto-enabled when the `tor`
binary is found, no toggle) and 31.2 I2P eepsite (OPTIONAL, opt-in,
default off).

### PART 31.1: Tor hidden service

- Auto-enable Tor hidden service when Tor is present on the host;
  dedicated Tor process via `github.com/cretz/bine`, dedicated loopback
  backend port, v3 hidden service, SafeLogging.
  Read: AI.md PART 31.1
- Tor request detection at priority 0 in `GetURLVars`/`BuildURL`
  (`src/httpserver/urlvars.go` explicitly defers this today).
  Read: AI.md PART 12 "Tor Hidden Service Configuration"
- Tor HTTP semantics: never issue an HTTPS redirect, HSTS header, or CSP
  `upgrade-insecure-requests` on a Tor request; clearnet HTTPS-only
  (port 443) must NOT propagate to the onion; `Secure` cookies still set.
  Read: AI.md PART 12 "Tor HTTP Semantics (No HTTPS Upgrade)"
- Tor request logging identity: never log/display `127.0.0.1` for a Tor
  request — use `tor:{circuit_id}` from the `HiddenServiceExportCircuitID
  haproxy` PROXY header, or the literal `tor` when export is off. Rate
  limiting must key on circuit/session, never the collapsed loopback IP
  (`src/httpserver/ratelimit.go` keys on client IP today).
  Read: AI.md PART 12 "Tor Request Logging & Identity"
- Tor timestamp normalization: render user-facing timestamps in UTC for
  Tor requests (server local timezone leaks operator location).
  Read: AI.md PART 12 "Tor Timestamp Normalization (UTC)"
- `Onion-Location` header on clearnet HTML document responses only (2xx
  top-level navigations); never on onion responses, API/JSON, static
  assets, or redirects.
  Read: AI.md PART 12 "Onion-Location Advertisement"
- Footer/help "Tor Access" section + `checks.tor` + `features.tor.*`
  populated from the real Tor manager (health currently reports the
  honest zero value).
  Read: AI.md PART 13, 16

### PART 31.2: I2P eepsite (OPTIONAL — opt-in, default off)

Entirely unimplemented; new in the 2026-08-16 spec revision.

- Config surface: `features.i2p.enabled` (default false) / `I2P_ENABLED`
  env / `--i2p` flag. Disabled means no provider, no port, no generated
  config — nothing at all.
  Read: AI.md PART 31.2 "Configuration"
- Provider resolution: i2pd binary (Model A, dedicated process,
  `{config_dir}/i2p/tunnels.conf` regenerated each startup) preferred;
  external SAM bridge at `127.0.0.1:7656` (Model B) as fallback; neither
  available → log WARN and continue (non-fatal).
  Read: AI.md PART 31.2 "Provider Model"
- Destination key persisted at `{data_dir}/i2p/site/`; `.b32.i2p` address
  derived from it.
- Startup sequence step 17b and step 20 "I2P: {.b32.i2p}" banner/log line.
  Read: AI.md PART 8 startup PHASE 5
- I2P exception in the trusted-proxy gate: a request whose `Host` matches
  `i2p.b32_address` resolves from `i2p.*` config, always `http://`, no
  proxy-header inspection, no IP check.
  Read: AI.md PART 12 "I2P exception"
- Health: `features.i2p.{enabled,running,status,hostname,provider}` — the
  struct and text/JSON rendering now exist in
  `src/httpserver/health.go` and report the disabled zero value with
  `provider: none`; wire them to a real I2P manager when built. Add
  `checks.i2p` (omitted while disabled).
  Read: AI.md PART 13
- Frontend: footer I2P block (`{{ if and .I2PEnabled .I2PRunning
  .I2PAddress }}`), `/server/help#i2p-access` section, `{i2p_address}`
  custom-HTML template variable.
  Read: AI.md PART 16
- Scheduler task `i2p_health` every 10 minutes (only when opt-in enabled).
  Read: AI.md PART 18
- Overlay protocol rule (applies to both Tor and I2P): overlays are
  ALWAYS `http://`; no certificate is ever issued or self-signed for an
  overlay address. The older "overlays inherit HTTPS-only from clearnet
  port 443" rule was REMOVED from the spec — do not reintroduce it.
  Read: AI.md PART 12, PART 16 "Overlay Network Protocol Rules"

## Spec-revision follow-ups (2026-08-14 / 2026-08-16 AI.md updates)

- Daily release identity changed: the daily release tag is the rolling
  `daily` tag (deleted and recreated nightly) and the daily VERSION is the
  short commit id (`git rev-parse --short HEAD`) — never a timestamp and
  never `release.txt`. `--update branch daily` now handles the rolling tag
  (`src/updater/updater.go` keys it by `published_at` against the embedded
  build epoch); the remaining half is the PART 27 release workflow, which
  must publish that rolling tag and a `sha256.txt` asset naming each
  `shortner-{os}-{arch}` binary — `src/updater` verifies against exactly
  that file and fails closed without it.
  Read: AI.md PART 13 "Version Format", PART 22, PART 27

## Audit follow-ups (2026-08-19 compliance audit)

- RESOLVED: `docs/reference/csjlol/` (6 files) violated AI.md PART 3, which
  reserves `docs/` for the ReadTheDocs documentation set only. Moved to
  top-level `reference/csjlol/`; `IDEA.md`'s "Frontend design reference"
  section and this file's PART 16 home-page item were updated to the new
  path.
  Read: AI.md PART 3, PART 29
- `docs/` still has none of the required ReadTheDocs pages (index,
  installation, configuration, api, cli, security, integrations,
  development) — PART 29 remains unimplemented; README no longer links a
  non-existent `docs/api.md`.
  Read: AI.md PART 29
- `CACHE_URL` (and the commented `DATABASE_DRIVER`/`DATABASE_URL`) are set
  in the compose files as PART 26 requires, but no Go code reads any of
  them — the cache/database URL config surface is unimplemented, so the
  documented override silently does nothing. Implement the env-to-config
  wiring in `src/config` when PART 10's storage layer lands.
  Read: AI.md PART 5, PART 10, PART 26

- `src/main.go:306` calls `certmgr.NewTLSConfig(configDir, host, "")` and
  discards the returned `*autocert.Manager`, so the HTTP-01 challenge
  handler is never mounted and ACME issuance can never complete. Wire the
  manager's `HTTPHandler` into the plain-HTTP listener.
  Read: AI.md PART 15
- `apperr.SendError` never logs `err.Internal`, so 5xx root causes are
  discarded. PART 9 "Error Logging" requires every error logged with
  context (5xx at Error, 4xx at Warn); `apperr` needs a logger dependency
  to do it.
  Read: AI.md PART 9 "Error Logging"
- `src/httpserver/server.go:90` — `corsAPIMiddleware` forces
  `Access-Control-Allow-Origin: *` even when the configured CORS
  middleware set `Access-Control-Allow-Credentials: true`; browsers reject
  that pair, so credentialed CORS silently breaks. Needs a policy decision
  on which wins.
  Read: AI.md PART 12, PART 14
- `statsHandler` is unauthenticated and exposes anonymized IPs and
  referrers — needs an explicit Tier 2/Tier 3 classification decision
  before it can be called compliant.
  Read: AI.md PART 11 "Public Endpoint Safety Principle"
- `src/certmgr/certmgr.go` `SaveAppManagedCertificate` writes
  `fullchain.pem`/`privkey.pem` non-atomically (truncate in place);
  `config.Save` now uses the temp-file + rename pattern — do the same here.
  Read: AI.md PART 15
- `src/applog/logger.go` `Rotate()` reopens the same path without renaming,
  so it never actually rotates. Blocked on the PART 18 scheduler.
  Read: AI.md PART 11 "Log Rotation", PART 18

## Documented spec deviations

- `src/daemon.go` `filterDaemonFlag` filters `-daemon` in addition to the
  `--daemon`/`-d` shown in AI.md PART 8's literal example. `main.go`
  registers the daemon flag as `fs.Bool("daemon", ...)` with no `-d`
  alias; Go's `flag` package accepts `-daemon` and `--daemon` as
  equivalent spellings of that one flag, so AI.md's example, filtering
  only the double-dash form, leaves an infinite re-exec/fork loop
  reachable via `-daemon` (flagged by automated security review,
  2026-08-19, HIGH). Kept as a deliberate, tested deviation from the
  literal spec code rather than a silent guess — AI.md is read-only and
  cannot be amended, but its example is incomplete for this flag
  registration.
  Read: AI.md PART 8 "Daemonization"

## PART 32: Client

- `shortner-cli` client binary (currently only a `doc.go` placeholder in
  `src/client/`) — setup wizard, `--server`/`--token`/`--output` flags.
  Read: AI.md PART 32

## Repo-level (calling session, not this bootstrap)

- `git init` and initial commit — intentionally left to the calling
  session; this bootstrap pass did not run any git command.
