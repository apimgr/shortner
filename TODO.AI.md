# TODO.AI.md

Implementation status and remaining work for `shortner`, checked against
the read-only `AI.md` source of truth. `DONE` entries record completed work;
open items are either implementable requirements or explicit AI.md contract
gaps recorded in `/root/Projects/github/apimgr/TODO.API.md`. PART 0-6
bootstrap is complete.

## Foundation follow-ups (small, do first)

- Windows admin-token detection in `src/paths/paths_windows.go` is a
  minimal probe (`\\.\PHYSICALDRIVE0` open test) — verify against real
  Windows UAC behavior and harden if needed.
  Read: AI.md PART 4

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
  best-effort COLORFGBG heuristic, now consumed by the client's
  `tui.ResolveTUITheme`/`stylesForTheme` (PART 32); see the doc comment
  for the platform-API gap it still leaves.
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
  listing have no consumer — the client's TUI handles resize through
  bubbletea's own `tea.WindowSizeMsg` and carries its symbol sets in
  `src/client/tui/symbols.go`, so neither shared file is needed. Same for
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

**PART 11's HTTP-dependent work is now implemented** (2026-08-20): the
security-header matrix + CSP + Permissions-Policy + Cross-Origin Isolation
(`src/httpserver/headers.go`), privacy signals DNT/GPC
(`privacy_signals.go`), Sec-Fetch-* validation (`secfetch.go`), the
Reporting API endpoints (`reports.go`), well-known files
(`wellknown.go` — robots.txt, security.txt, llms.txt, pgp-key.asc),
the security pages (`securitypages.go` — `/server/security`,
`/server/security/policy`, `/server/security/thanks`, `/server/dpo`), the
coordinated-disclosure submission path (`securityreport.go` +
`src/db/securityreport.go` + `src/security/seal.go`), and allowlist /
IP-block / abuse-detection middleware (`ipblock.go`) wired into the PART 5
middleware order. `server.security.*` (including `encryption_key`) and
`server.contact.security`/`server.contact.dpo` are in `src/config`.

Already done, verified rather than reimplemented:
- Response-layer wiring — `apperr.SendOK`/`SendError` are called from
  `src/httpserver/{server,middleware,frontend,links}.go`, and the
  expired-link 410 Gone lives at `src/httpserver/links.go` (`link.IsExpired()`
  → `apperr.CodeGone` → `http.StatusGone`).
- `src/security/slug.go`'s `reservedSlugs` is already a superset of PART
  16's canonical `reservedNames` table.
- Click bot/crawler filtering — `src/security/bot.go` holds the UA token
  list (neither AI.md nor IDEA.md defines a canonical one), and
  `src/httpserver/links.go`'s `isBotUserAgent` delegates to it.

Still open under PART 9-11:
- DONE: Security-report notification emails (Submission Flow steps 4 and
  5 — maintainer notification and researcher acknowledgment) are sent by
  `src/httpserver/securityreport.go` now that PART 17 exists. AI.md calls
  these "the CC path, never the primary channel", so a send failure never
  affects the submission: the tracking id is still issued and shown
  server-side. The maintainer copy carries the report body as inline
  AES-256-GCM armor rather than a PGP MIME attachment — see the PART 17
  deferred sub-items below.
  Read: AI.md PART 11 "Submission Flow", PART 17
- Operator `--maintenance security list|show|update` is implemented,
  including forward-only status updates and content-free audit events.
  Researcher contact status notifications remain blocked by the
  `contact_enc` storage-contract issue recorded in the parent
  `TODO.API.md`.
- Security-report database storage still uses AES-256-GCM even when a
  project PGP key exists; AI.md requires PGP-at-rest in that case. Status
  email also needs the missing, separately encrypted `contact_enc` field
  before the server can recover the researcher's address without opening
  the sealed report body.
- `src/db` only supports `modernc.org/sqlite`; libsql/Turso remote-DB
  support depends on the full `server.yml` database schema.
  Read: AI.md PART 10, PART 12
- PART 18 scheduler and rotation are implemented, but the configured
  `server.logs.*` policy schema and integration for all log types (including
  the separate audit logger) still need to be completed.
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
  /api/{api_version}/links` (create, auto or custom slug, creates or
  reuses a persistent anonymous owner and returns `owner_token` only for
  a newly created owner), `GET/PATCH/DELETE
  /api/{api_version}/links/{slug}` (owner/operator-token-gated
  update/delete), `GET /api/{api_version}/links/{slug}/stats`
  (click-analytics sub-resource), plus the public vanity routes `GET
  /{slug}` (redirect, bot-UA-filtered click recording, 410 Gone when
  expired) and `GET /{slug}/stats`.
- Owner-token model migrated from per-resource tokens to persistent
  owner/primary/linked tokens in `src/db/`, with legacy database migration,
  encrypted token recovery, owner-scoped link authorization, the
  `/server/preferences/tokens` management page, and primary-only API
  mirrors. Expired linked tokens remain visible and unusable. The AI.md
  PART 8/PART 18 cleanup contradiction and missing strict-throttle config
  contract are recorded in the parent `TODO.API.md`.
- API routes now use the shared configured CORS middleware, including
  health routes. Explicit origins, credentials, and wildcard behavior follow
  the PART 16 CORS rules; route handlers no longer override those headers.
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
  - DNS-01 provider support and the `credentials_encrypted` representation
    remain unimplemented. The source spec names example fields and links
    the full lego provider list, but does not define the stored encrypted
    credential shape, startup/request validation contract, or credential
    rotation behavior. These AI.md gaps are recorded in the parent
    `TODO.API.md`; existing `src/security` sealing primitives can be reused.
    Read: AI.md PART 15 "DNS-01 Provider Configuration" and "Provider
    Credential Storage"
  - Autocert-issued certificates are now mirrored into the app-managed
    `{fullchain,privkey}.pem` layout from `GetCertificate`. The autocert
    cache remains the source of account state.
    Read: AI.md PART 15 "Certificate Directory Structure"
  - The scheduled `ssl_renewal` task currently warns when a certificate is
    within seven days of expiry; it does not proactively invoke ACME
    re-issuance. Successful on-demand ACME renewal emits `ssl_renewed`
    after the renewed certificate is persisted.
    Read: AI.md PART 15 and PART 18
  - Dual HTTP+HTTPS listeners, HTTP-01 challenge routing on the HTTP
    listener, automatic TLS when port 443 is selected, and separate startup
    URLs are implemented. Config validation accepts the AI.md single, dual,
    and single-port `0` forms; tests and the full project gate pass.
    Read: AI.md PART 8 "Port Configuration", PART 15
  - With `server.port: 0`, the OS-selected port is not reflected in the
    startup URL banner, which currently prints the configured value. Bind
    before rendering the banner so the actual address can be displayed.
    Read: AI.md PART 8 "Special Port Handling"

## PART 16: Web frontend — DONE (core)

- Server-rendered pages implemented: home (`/`, create-link form + POST +
  success state, reuses PART 14's owner-scoped `db.CreateLink*` operations
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

- PWA core is implemented: `/manifest.json`, `/sw.js` with a build-stamped
  cache, `/offline.html`, stamped static-asset caching, install/update
  prompts, offline status, required icon sizes including the iOS 180px icon,
  and configured splash images. Background Sync and geolocation remain
  unimplemented because AI.md gives generic behavior without a Shortner
  action, data, or privacy contract; that gap is recorded in the parent
  `TODO.API.md`.
  Read: AI.md PART 16 "PWA"
- `sitemap.xml` — implemented (`src/httpserver/sitemap.go`,
  `GET/HEAD /sitemap.xml`): static public pages plus each unexpired public
  short-link redirect and its `/stats` page. The configured per-file URL
  limit is clamped to the sitemap protocol maximum; oversized catalogs use
  `/sitemap.xml` as an index with numbered sitemap pages. Expired resources,
  management routes, and API endpoints are excluded. Sitemap generation is
  covered by `src/httpserver/sitemap_test.go`.
- Dynamic SEO metadata and site-verification meta tags are implemented in
  `head.tmpl`. Remote branding/SEO image fetching and resizing remain
  unimplemented; remote URLs are restricted to HTTPS, and overlay requests
  suppress external images.
- `/favicon.ico` — implemented: embedded default icon set generated from
  the Dracula theme palette (`src/server/static/favicon.ico` multi-res
  ICO plus `icons/favicon-{16,32,48}.png`, `icons/apple-touch-icon.png`,
  `icons/icon-{180,192,512}.png`), served by `faviconHandler` in
  `src/httpserver/sitemap.go`, with icon `<link>` tags in `head.tmpl`.
  Configured local branding images are served through `/branding/favicon`
  and `/branding/logo`; remote HTTPS URLs are used directly where safe.
  Remote fetch/resize remains deferred (see above).
- `Web.Announcements` is rendered from `web.announcements.messages` via
  `PageData` and the public layout partial; active date windows, dismissal
  cookies, form submission, and configuration validation are implemented.
- DONE: GeoIP location data on `/{slug}/stats` — `page/stats.tmpl` renders
  "unknown" gracefully when empty, and now gets real values: PART 19's
  `src/geoip` lookup populates `ClickInfo.Country`/`.Region` at click time.
- DONE: Contact-form email delivery — `contactPost` in `frontend.go`
  validates the form (including the static math-captcha check) and
  `relayContactMessage` forwards it to `server.contact.general.email`
  (falling back to `.admin.email`) through the PART 17 notifier. The
  relay is best-effort by design: with no working SMTP the message is
  simply not relayed, per PART 17's no-queue rule.
- `/server/docs/swagger` and `/server/docs/graphql`, their versioned and
  unversioned API endpoints, and local documentation assets now exist.
  GraphQL query fields are generated for each annotated read-only REST
  route, and internal dispatch rejects undocumented paths and GET routes
  with side effects. OpenAPI now describes concrete link, health, CLI
  discovery, preferences, token, GraphQL, report, and metrics contracts;
  create/report success statuses and link pagination parameters match their
  handlers. The annotations cover the API, CLI binary, report, link, and
  metrics routes mounted in `src/httpserver/server.go`; link deletion and the
  OpenAPI endpoint now have specific response schemas, and token-update and
  GraphQL alias requests include examples. Report contracts accept both legacy
  and modern media types and model both object and array payloads. Some
  operations still use flexible JSON schemas or lack useful response examples.
  The Swagger page now renders the complete annotated operation list without
  JavaScript and adds parameter, request, auth, and
  response controls with JavaScript. GraphQL docs list fields from the
  executable schema and support query, operation name, and variables in
  enhanced and standard form submissions. Browser E2E for both pages now
  passes across SSR, no-JavaScript, and browser-interaction tiers. Remaining:
  finish the remaining precise schemas and examples for server operations.
  Specs are
  regenerated by `go generate ./src/swagger` on Makefile, CI, Docker, release,
  and E2E build paths; OpenAPI and GraphQL introspection JSON are embedded
  and verified against their source generators.
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

- The PART 18 scheduler now uses its internal scheduler loop and cron
  parser; the obsolete `gocron` dependency and the earlier review item have
  been removed.
- DONE: Email & notifications (`src/notify/`, `src/notifications.go`,
  `src/email_cli.go`). `src/notify/template.go` parses AI.md PART 17's
  `Subject: ...` / `---` / body wire format and does `{variable}`
  substitution (a valid-but-unset placeholder renders empty; a malformed
  token is left verbatim), `events.go` carries all 12 events plus the
  per-event variable table and the `server.notifications.email.events.*`
  switches, `store.go` resolves custom `{config_dir}/template/email/` over
  the embedded `src/server/template/email/` defaults with live reload and
  reset-by-deletion, `validate.go` implements PART 17 "Template Validation"
  including the "Did you mean {x}?" suggestions, `detect.go` implements
  the priority-ordered SMTP auto-detection (7 hosts x ports 25/465/587,
  EHLO handshake test), `smtp.go` builds and sends RFC 5322 messages over
  `net/smtp` + `crypto/tls` (auto/starttls/tls/none) with CRLF-injection
  and dot-stuffing guards, and `notify.go` is the nil-safe `Notifier` that
  enforces PART 17's SMTP Requirement by construction — no SMTP means no
  send, no queue, and no "would have sent" log line.
  `config.ApplySMTPEnv` layers the `SMTP_*` overrides; the `email
  [test|list|preview|validate|reset]` subcommand implements PART 17
  "Email Template Configuration". Wired call sites: `startup`/`shutdown`
  (`src/notifications.go`, `src/main.go`), `security_alert`
  (`src/httpserver/ipblock.go` abuse detection), the PART 11 Submission
  Flow steps 4/5 emails (`src/httpserver/securityreport.go`),
  `backup_complete`/`backup_failed`/`scheduler_error` with PART 17's
  suppression rule (`src/scheduler/notify.go`),
  `ssl_expiring`/`ssl_renewal_failed` (`src/scheduler/tasks.go`), and
  `update_available`/`update_installed` (`src/scheduler/update.go`).
  Deferred sub-items:
  - `ssl_renewed` is emitted by the autocert `GetCertificate` path when a
    new certificate has a later expiry than the existing certificate, after
    the app-managed copy is saved. The first issuance does not count as a
    renewal.
    Read: AI.md PART 15, PART 17 "ssl_renewed"
  - The PART 11 maintainer notification falls back to inline
    AES-256-GCM armor when no project PGP public key exists; with a key it
    sends an OpenPGP-encrypted report. The mailer does not emit a MIME
    attachment because the spec's current mailer only supports text bodies.
    Read: AI.md PART 11 "Submission Flow", PART 17
  - The toast component now supports stacking, typed feedback, dismiss
    controls, hover-paused timers, keyboard dismissal, localization, and
    configured position/duration. Remaining PART 16 work is the
    no-JavaScript flash-message fallback and wiring feedback to every
    visitor action; current JavaScript invocation is limited to clipboard
    feedback.
    Read: AI.md PART 17 "Notification Systems"
  Read: AI.md PART 17
- DONE: Internal scheduler (`src/scheduler/`, `src/db/scheduler.go`,
  `src/scheduler_cli.go`, wired into `src/main.go` startup/shutdown).
  All 13 AI.md PART 18 "Built-in Tasks (Required)" are registered with
  their default schedules (config-overridable per task via
  `server.scheduler.tasks`); token cleanup (idle-owner purge is active;
  expired linked-token removal is withheld because PART 8 requires those
  tokens to remain listed), log rotation, self health, SSL renewal,
  `geoip_update`, `disk_check`, overlay health, plus `update_check` from
  PART 22 and `backup_daily`/`backup_hourly` from PART 21). Only
  `blocklist_update` and `cve_update` remain honest no-op skips because
  their feed formats and validation contracts are unspecified in AI.md and
  are recorded in the parent `TODO.API.md`.
  `--scheduler list/show/run/enable/disable/history` CLI dispatch,
  startup catch-up, panic recovery, overlap prevention, persisted next-run,
  and graceful shutdown with the spec's 30-second forced release are
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
  `LICENSE.md`'s Third-Party Licenses section, including the manually
  classified modernc.org/mathutil BSD-3-Clause license.
  Allowlist bypass of country-blocking is now wired (2026-08-20): PART
  11's `AllowlistLookup` backs `allowlistMiddleware`, which marks the
  request context, and `geoIPMiddleware` short-circuits on
  `IsAllowlisted(ctx)`.
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
  - Cache metrics (`Cache*`) are declared but inert — `src/cache` is not
    wired into any request path yet.
  - I2P-specific metrics are not defined in AI.md PART 20; the PART 20
    overlay metric contract covers Tor, which is wired.
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
  Backup history metadata is stored in `server.db` with the archive SHA-256;
  encrypted restore prompts display `backup.encryption.hint`. A running
  service-managed server is stopped before restore and started afterward.
  Standalone processes without a detected service manager are not stopped
  by this command.
  Read: AI.md PART 21
- DONE: `--maintenance mode`, `setup`, and `secret rotate` actions — mode
  persistence and config precedence, first-run/root setup authorization,
  token-authorized sensitive operations, PGP private-key re-sealing,
  old-secret grace storage, and eager re-encryption of stored AES data.
  Exact grace-key retirement/failure-atomicity contracts are recorded in
  `/root/Projects/github/apimgr/TODO.API.md` for the API template owner.
  Read: AI.md PART 11, 12, 21
- `--maintenance data` and `compliance` operations remain undefined for
  this product because AI.md does not define their target, format, scope,
  confirmation, retention, or report contract; the AI.md issue is recorded
  in the parent `TODO.API.md`.
  Read: AI.md PART 21
- DONE: `--update` self-update command (`src/updater/`, `src/update.go`,
  `src/scheduler/update.go`, `--maintenance update` alias in
  `src/maintenance.go`, `server.update.*` in `src/config/`). Implements
  `check`/`yes`/`branch {stable|beta|daily}`, the cumulative channel
  semantics, the `defer_days` window (scheduled task only), SHA-256
  verified downloads, Unix rename-in-place + Windows
  `MOVEFILE_DELAY_UNTIL_REBOOT` replacement, service-aware restart, and
  the `update_check` task with once-per-version notification.
  Read: AI.md PART 22
- DONE: `update_available` email event (AI.md PART 22 "Surfacing rules").
  `UpdateDeps.notify` now emits the WARN log line *and* the PART 17
  `update_available` email once per version; `update_installed` fires
  after a successful `auto_install`.
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

PART 23 and PART 24 are implemented (`src/service/` + `src/service.go`).
The remaining items below are environment limits, not unbuilt work.

- Live init-system verification is not possible in this environment. The
  build/test container has no systemd, OpenRC, runit, SysVinit, rc.d,
  launchd, or Windows SCM, so an end-to-end `--service --install` →
  enable → start → `--service --uninstall` cycle cannot be exercised
  against a real init system. What is covered instead: the exact command
  line each manager runs for every verb, the exact install path each
  manager writes, the rendered content of all seven service templates,
  init-system detection for every OS/binary combination, UID/GID
  selection, the escalation chains, and one genuine end-to-end install
  cycle over the systemd `--user` path (rooted at a temporary `HOME`,
  which is the only manager that writes outside a system directory).
  Verify the real install paths on a live host, or via `tests/incus.sh`
  once that script grows systemd checks.
  Read: AI.md PART 23, 24
- Account creation is asserted at the command-line level only.
  `CreateServiceAccount` is tested with `run` stubbed, because a test
  must not create a real system user/group. `dscl` (macOS) and
  `pw` (FreeBSD) paths are therefore unexecuted on this Linux host.
  Read: AI.md PART 23
- Windows service registration (`mgr.CreateService` with an empty
  `ServiceStartName` for the Virtual Service Account) cross-compiles but
  is never run — there is no Windows host here.
  Read: AI.md PART 24

## PART 26: Docker — DONE

- `docker/Dockerfile` and `docker/Dockerfile.dev` include the required
  `i2pd` runtime package and use the PART 26 healthcheck timing.
- Production, development, and test Compose files use the shared project,
  network, and fixed container names; timezone overrides use the specified
  fallback, and development bind mounts omit the SELinux `:z` suffix.
- The entrypoint maps `LISTEN` and `PORT` into safely quoted CLI arguments
  and preserves the final `exec` handoff.
- Verified: both images build, all three Compose files pass `docker compose
  config`, and ShellCheck passes for `entrypoint.sh`.
  Read: AI.md PART 26

## PART 27: CI/CD workflows

- DONE: PART 27 is implemented for this repo's sole provider (GitHub —
  confirmed via `git remote get-url origin`; Gitea/Forgejo/GitLab/Jenkins
  sections do not apply). `.github/workflows/ci.yml` (lint, secret-scan,
  workflow-policy, test with 60% coverage gate, build, vuln-scan,
  image-scan — security jobs also on the weekly cron, build/test skip on
  schedule), `release.yml` (tag push, 8-platform matrix, SBOM, checksums,
  provenance attestation, GitHub Release), `beta.yml` (push to `beta`),
  `daily.yml` (3am UTC cron + push to main/master, rolling `daily` tag
  deleted and recreated each run), `docker.yml` (`build-standard` +
  `build-devel` jobs, ghcr.io, QEMU/buildx multi-arch). Every third-party
  Action SHA independently verified against GitHub's tag refs (annotated
  tags dereferenced to their commit) before pinning — all 11 matched
  AI.md's example SHAs exactly. `act --list -W {file}` passes for all 5
  files; the `workflow-policy` SHA-pin grep also passes locally. Verified
  with a real push-triggered run: the first push's `lint` job caught 3
  pre-existing staticcheck/gofmt violations unrelated to the workflow
  files themselves (`src/metrics/dbdriver.go` deprecated `Begin()`
  fallback needing a `//lint:ignore SA1019`, an unused `gcLast` field, a
  `gofmt` misalignment, and an identical-expression `!=` comparison in
  `src/security/security_test.go`); fixed in a follow-up commit, after
  which `ci.yml`, `daily.yml`, and `docker.yml` all completed `success`.
  Read: AI.md PART 27

## PART 28-30: Testing, docs, i18n

- DONE: PART 28's `tests/` deliverables are implemented.
  `tests/run_tests.sh` is the two-phase entry point — phase 1 runs the Go
  suite through `make test` (this resolves the old "raw `docker run ... go
  test` instead of the Makefile target" item), phase 2 auto-detects the
  runtime (Incus when `incus info` succeeds, else Docker) and dispatches.
  `tests/docker.sh` builds in `casjaysdev/go:latest`, boots a disposable
  `alpine:latest` container bind-mounted to
  `${TMPDIR:-/tmp}/apimgr/shortner-XXXXXX/volumes`, installs
  bash/curl/file/jq, pushes the binaries plus `suite.sh`/`assert.sh`, and
  runs the suite. `tests/incus.sh` does the same against
  `images:debian/trixie` and then exercises the real PART 23/24 service
  lifecycle (`--service --install`, `systemctl is-enabled`/`is-active`,
  the dedicated system user, reload/restart/stop/start, and
  `--service --uninstall` with its confirmation prompt) — this resolves
  the old "incus.sh only proves container lifecycle" item.
  `tests/suite.sh` is the shared in-container suite: prerequisites, static
  ELF/`ldd` binary-info checks, `--version`/`--help`, the binary-rename
  test, first-run `server.yml` auto-creation and `server.token` pickup,
  health endpoints, the content-negotiation matrix, well-known/`.txt`
  endpoints, every frontend page, full link CRUD (auto code, custom slug,
  duplicate 409, reserved 409, malformed URL/expires_at), redirect/410/404
  resolution, stats including bot exclusion and IP anonymization, owner-
  vs operator-token authorization, the client-binary checks, and the write
  rate-limit 429. `tests/assert.sh` and `tests/common.sh` hold the shared
  helpers; `tests/test_content_negotiation.sh` re-runs the negotiation
  matrix against any running instance. `tests/e2e.sh` plus `tests/e2e/`
  (chromedp, `e2e` build tag, untagged `doc.go` so `go test ./...` still
  builds) implement all three browser tiers against a
  `chromedp/headless-shell` container on a private Docker network.
  Verified: `bash -n` clean on all 8 shell files; `go mod tidy`,
  `gofmt -l tests/e2e` (clean), `go vet -tags e2e ./tests/e2e/...` and
  `go test ./src/httpserver/... -cover` (73.3%) all pass in
  `casjaysdev/go:latest`.
  Read: AI.md PART 28
- NOT YET EXERCISED: no full `tests/docker.sh`, `tests/incus.sh`, or
  `tests/e2e.sh` run has been executed end to end — the scripts are
  syntax-checked and their helpers dry-run, but the first real pass will
  almost certainly surface assertion-level drift (exact JSON field names,
  status codes for validation errors, the `--service` verbs' exit codes).
  Run all three and fix what they report.
  Read: AI.md PART 28
- `/server/healthz.txt` (and every other `.txt` path suffix) returns 404:
  `wantsText` honors the suffix, but the chi router has no `.txt` route
  variants and `urlNormalizeMiddleware` does not strip the extension
  before matching. `tests/suite.sh` and
  `tests/test_content_negotiation.sh` report this as a SKIP rather than
  faking a pass. Add extension-stripping to the router.
  Read: AI.md PART 13, 14
- DONE (no longer reachable): `make build`/`make local` used to fail when
  `src/client/` existed without a `package main`. `src/client/main.go` is
  now a real `package main`, so both targets build the client on all 8
  platforms. The `has_client_main()` guard in `tests/common.sh` is now
  redundant but harmless.
  Read: AI.md PART 25
- DONE: `tests/suite.sh`'s CLI checks are un-gated and extended.
  `__has_client_main()` now succeeds, so `__test_cli` runs for real: it
  asserts `--version`/`--help` (including that `--tui`/`--gui` are absent,
  per PART 32), `--shell completions bash`, and then drives `health`,
  `shorten`, `get`, `stats`, and `list` against the live server with
  `--output json` under a throwaway `HOME`. It also asserts the PART 32
  no-command/no-tty usage exit (64) and the 0600 mode on the written
  `cli.yml`. The `__has_client_main()` probe stays as a build-error guard
  for a tree mid-refactor, with its skip reason reworded accordingly.
  Read: AI.md PART 8, 28, 32
- AI.md PART 28 line 37584 references a "Testing Operator-Token Routes"
  section that does not exist anywhere in the spec — the nearest real
  section is "Testing Open API Routes". Spec defect, recorded here
  because AI.md is read-only; operator-token coverage was written against
  the Open API Routes section plus IDEA.md's permission rules instead.
  Read: AI.md PART 28
- DONE: PART 29 (ReadTheDocs documentation) is implemented. `mkdocs.yml`
  and `.readthedocs.yaml` in the project root; `docs/requirements.txt`,
  `docs/stylesheets/dark.css`, `docs/stylesheets/light.css` verbatim from
  the spec templates; and all eight required pages — `index.md`,
  `installation.md`, `configuration.md`, `api.md`, `cli.md`,
  `security.md`, `integrations.md`, `development.md`. Every page is
  written from the actual source (routes enumerated from
  `src/httpserver/`, the config tree and defaults from `src/config/`, the
  path matrix from `src/paths/`, CLI verbs from `src/main.go` +
  `src/service.go` + `src/update.go` + `src/email_cli.go` +
  `src/backup_cli.go` + `src/scheduler_cli.go`) rather than from the
  spec's placeholder skeletons, and every TODO.AI.md-deferred item is
  described as deferred rather than shipped — Swagger/GraphQL are present
  with route/schema sync still in progress, no DNS-01, PWA core complete
  with Background Sync/geolocation awaiting a product contract, no hot reload,
  no cache in the request path, unpopulated business metrics, and a
  warning-only scheduled `ssl_renewal` check. Backup `list`/`delete`
  commands are implemented and documented as CLI conveniences. `mkdocs build
  --strict` verified clean inside `python:alpine` (built in 2.25s, zero
  warnings, all nav links resolve). `site_url` uses the org-project RTD
  format `https://apimgr-shortner.readthedocs.io` per PART 29's own rule
  for organization accounts; no pre-existing RTD project name was found
  anywhere in the repo.
  Read: AI.md PART 29
- PART 30 (I18N & A11Y) is DONE for the server binary and the Web UI:
  `src/common/i18n/` (embedded `locales/*.json` for all 7 spec languages,
  `Translate`/`TranslateFormat`/`TranslatePlural`/`IsSupported`/
  `LangFromRequest`/`SetLanguageCookie`/`Direction`/`RawLocale`/
  `Languages`/`CLILanguage`), `src/cmd/i18n-validate` + the `i18n-validate`
  Makefile target, `src/httpserver/i18n.go` (`LanguageMiddleware`, the
  `t`/`tf`/`tp` FuncMap funcs, the `/locales/{lang}.json` route),
  `src/config/i18n.go` (`server.i18n.*`), `src/lang.go` (the CLI output
  language). 284 `t`/`tf`/`tp` call sites across 22 templates; RTL via
  `dir` on `<html>` plus logical-property CSS; skip links, ARIA
  landmarks, live regions, `.sr-only`, focus-visible and 44px touch
  targets in the CSS.
  Read: AI.md PART 30
- DONE: PART 30's client-binary i18n. `shortner-cli` embeds the same
  `src/common/i18n` catalog, honors `--lang`, and its `--help`/`--version`
  output is fully translated through a new `client.*` section (22 keys)
  added to all 7 locale files.
  Read: AI.md PART 30, PART 32
- PART 30 deferred sub-item: subcommand help text is still hardcoded
  English. `src/service.go`, `src/update.go`, `src/email_cli.go`,
  `src/backup_cli.go`, and `src/scheduler_cli.go` print their own
  `--help`/usage blocks with literal strings; only `src/help.go`,
  `src/status.go`, `src/mode`, and `src/common/banner` were converted to
  `cli.*` keys. Extract the subcommand help into the catalog the same
  way (`helpSection`/`helpEntry` in `src/help.go` is the pattern).
  Read: AI.md PART 30, PART 8
- PART 30 deferred sub-item: Go `error` values are not translated. Errors
  returned from `src/db`, `src/backup`, `src/updater`, etc. cross package
  boundaries with no request/language context, so they stay English;
  only the user-facing HTTP error strings in `src/httpserver` were moved
  to `errors.*` keys. Translating the rest needs a language-carrying
  error type, which PART 30 does not specify.
  Read: AI.md PART 30
- PART 30 deferred sub-items: email templates, Swagger/GraphQL documentation
  pages, and Web UI toast strings are not fully localized. PWA install/update
  strings have locale keys; offline-sync
  strings await the Background Sync product contract. No strings are needed
  for multi-user roles/moderation/registration because this project has no
  user accounts.
  Read: AI.md PART 30, PART 16

## PART 31: Overlay networks (Tor & I2P)

PART 31 is implemented for both networks. What remains here cannot be
exercised in this build container (no `tor`, no `i2pd`, no SAM bridge) or
is honestly deferred with a reason.

### PART 31.1: Tor hidden service — implemented

Built: `src/tor/` (bine-driven dedicated Tor process, v3 hidden service,
SafeLogging, dedicated loopback backend port, HAProxy PROXY-protocol
circuit-ID ingest, vanity search/apply, key import, health monitor),
`src/tor_cli.go` (`tor status|validate|restart|regenerate|vanity
start|vanity apply|import-keys <path>`), `src/config/overlay.go`
(`server.tor.*` + validation warnings), `src/httpserver/overlay.go` +
`urlvars.go` (priority-0 overlay detection, `tor:{circuit_id}` identity,
`BuildURL` always `http://`), `headers.go`/`middleware.go` (no HSTS, no
`upgrade-insecure-requests`, `Onion-Location` on clearnet HTML 2xx
top-level navigations only, `Secure` cookies still set),
`pagedata.go` (UTC timestamps for Tor requests, footer/help address),
`health.go` (`features.tor.*` + `checks.tor`), `src/scheduler/overlay.go`
(`tor_health` every 10m), `src/overlay.go` + `src/main.go` (start/stop
wiring, `Tor: {onion_address}` printed once).

- DEFERRED (environment): no `tor` binary exists in this container, so
  bootstrap, descriptor publication, a live circuit-ID PROXY header, and
  `tor restart`/`regenerate` against a running daemon have never been
  exercised end to end. Every failure path is unit-tested; the success
  paths that need a live daemon are not (`src/tor` is at 63.9%).
  Verify on a host that has Tor installed.
  Read: AI.md PART 31.1

### PART 31.2: I2P eepsite — implemented (opt-in, default off)

Built: `src/i2p/` (Model A i2pd subprocess with a regenerated
`{config_dir}/i2p/tunnels.conf`, Model B external SAMv3 bridge over a raw
`net.Conn`, destination persisted under `{data_dir}/i2p/site/`,
`.b32.i2p` derivation, provider resolution i2pd → SAM → warn-and-continue,
health monitor), `src/i2p_cli.go`, `server.i2p.*` config + validation,
the trusted-proxy-gate exception for a `Host` matching the b32 address,
`features.i2p.{enabled,running,status,hostname,provider}` + `checks.i2p`
(omitted while disabled), the footer/help I2P blocks, `i2p_health` every
10m (registered only when opt-in enabled), and the `I2P: http://{addr}`
banner row.

- DEFERRED (environment): no `i2pd` binary and no SAM bridge exist here,
  so `startI2PD`/`waitForI2PDAddress` and a real tunnel are unexercised.
  The SAM path is tested against a fake in-process SAMv3 server
  (`src/i2p` is at 73.5%).
  Read: AI.md PART 31.2

## Spec-revision follow-ups (2026-08-14 / 2026-08-16 AI.md updates)

- RESOLVED: Daily release identity — the daily release tag is the rolling
  `daily` tag (deleted and recreated nightly) and the daily VERSION is the
  short commit id (`git rev-parse --short HEAD`) — never a timestamp and
  never `release.txt`. `--update branch daily` handles the rolling tag
  (`src/updater/updater.go` keys it by `published_at` against the embedded
  build epoch); `.github/workflows/daily.yml` now publishes that rolling
  tag with a `sha256.txt` asset naming each `shortner-{os}-{arch}` binary,
  matching exactly what `src/updater` verifies against.
  Read: AI.md PART 13 "Version Format", PART 22, PART 27

## Audit follow-ups (2026-08-19 compliance audit)

- RESOLVED: `docs/reference/csjlol/` (6 files) violated AI.md PART 3, which
  reserves `docs/` for the ReadTheDocs documentation set only. Moved to
  top-level `reference/csjlol/`; `IDEA.md`'s "Frontend design reference"
  section and this file's PART 16 home-page item were updated to the new
  path.
  Read: AI.md PART 3, PART 29
- RESOLVED: `docs/` had none of the required ReadTheDocs pages. PART 29 is
  now implemented — `mkdocs.yml`, `.readthedocs.yaml`,
  `docs/requirements.txt`, `docs/stylesheets/{dark,light}.css`, and all
  eight pages (index, installation, configuration, api, cli, security,
  integrations, development). `mkdocs build --strict` passes clean.
  Read: AI.md PART 29
- `CACHE_URL` (and the commented `DATABASE_DRIVER`/`DATABASE_URL`) are set
  in the compose files as PART 26 requires, but no Go code reads any of
  them — the cache/database URL config surface is unimplemented, so the
  documented override silently does nothing. Implement the env-to-config
  wiring in `src/config` when PART 10's storage layer lands.
  Read: AI.md PART 5, PART 10, PART 26

- RESOLVED: the autocert manager's HTTP-01 handler is mounted on the
  cleartext listener, and issued certificates are mirrored into the
  app-managed PEM layout. Successful on-demand renewals emit
  `ssl_renewed` after persistence.
  Read: AI.md PART 15
- `apperr.SendError` never logs `err.Internal`, so 5xx root causes are
  discarded. PART 9 "Error Logging" requires every error logged with
  context (5xx at Error, 4xx at Warn); `apperr` needs a logger dependency
  to do it.
  Read: AI.md PART 9 "Error Logging"
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

- DONE: the `shortner-cli` client binary is implemented.
  `src/client/main.go` (entry point), `src/client/cmd/` (flag parsing,
  dispatch, smart argument detection, output formats, `--shell`
  completions for all 8 shells, `--help`/`--version`, the 6-step CLI
  auto-update flow with SHA-256 verification and re-exec),
  `src/client/config/` (`cli.yml`, precedence CLI > env `SHORTNER_*` >
  file > default, the flag-to-config save rule, 0700 dirs / 0600 files),
  `src/client/paths/` (user-scope XDG paths only),
  `src/client/api/` (typed API client, `shortner-cli/{version}`
  User-Agent), `src/client/tui/` (bubbletea app — keyboard navigation,
  search, `?` help, `q`/ctrl+c quit, window resize, 7-breakpoint
  responsive layout, viewport scrolling, Unicode/ASCII symbol sets, dark
  and light lipgloss themes), and `src/client/setup/` (the wizard, whose
  mode follows PART 32's SSH/Mosh > display > terminal > error order).
  Verified in Docker: `gofmt`, `go build ./...`, `go vet`, and an
  8-platform cross-compile of `./src/client` all pass.
  Read: AI.md PART 32
- PART 32 native GUI uses the required pure-Go `gogpu/ui` toolkit behind
  the `gui` build tag on Linux, macOS, and Windows; BSD remains TUI-only.
  The interactive dashboard supports setup, link create/list/get/update/
  delete, click statistics, and server health, with validation, accessible
  labels/hints, and error dialogs. Full GUI parity with CLI configuration
  and update controls, responsive layout requirements, and native OS screen
  reader adapters remain outstanding. The GUI feature mapping is recorded
  in the parent `TODO.API.md`; the toolkit currently lacks native
  accessibility adapters.
  Read: AI.md PART 32 "GUI Mode Requirements"
- PART 32 server auto-update discovery and colocated binary routes are
  implemented. Audit event reporting still needs the transport and payload
  contract defined in the parent `TODO.API.md`; do not claim client events
  are recorded server-side until then.
