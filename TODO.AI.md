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
- GPG keypair management (`--maintenance pgp generate|rotate|publish|
  export|import|delete`) is not implemented: it needs an OpenPGP
  implementation (not in `go.mod`) and the keyserver publish flow. Today
  `/.well-known/pgp-key.asc` serves `{config_dir}/security/pgp.pub.asc`
  when an operator places one there and 404s otherwise, and security.txt
  emits `Encryption:` only when that file exists.
  Read: AI.md PART 11 "GPG Keypair Management"
- DONE: Security-report notification emails (Submission Flow steps 4 and
  5 — maintainer notification and researcher acknowledgment) are sent by
  `src/httpserver/securityreport.go` now that PART 17 exists. AI.md calls
  these "the CC path, never the primary channel", so a send failure never
  affects the submission: the tracking id is still issued and shown
  server-side. The maintainer copy carries the report body as inline
  AES-256-GCM armor rather than a PGP MIME attachment — see the PART 17
  deferred sub-items below and the GPG keypair item above.
  Read: AI.md PART 11 "Submission Flow", PART 17
- `/server/security/report/{tracking_id}` researcher status page (one-shot
  token, triage state machine, maintainer comments) is not implemented —
  triage state has no writer until the maintainer notification path above
  exists.
  Read: AI.md PART 11 "Public Pages"
- `src/db` only supports `modernc.org/sqlite`; libsql/Turso remote-DB
  support depends on the full `server.yml` database schema.
  Read: AI.md PART 10, PART 12
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
- DONE: Contact-form email delivery — `contactPost` in `frontend.go`
  validates the form (including the static math-captcha check) and
  `relayContactMessage` forwards it to `server.contact.general.email`
  (falling back to `.admin.email`) through the PART 17 notifier. The
  relay is best-effort by design: with no working SMTP the message is
  simply not relayed, per PART 17's no-queue rule.
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

- Scheduler review item: `src/scheduler/scheduler.go` wraps
  `github.com/go-co-op/gocron/v2`, an in-process Go library compiled
  into the binary (not an external cron daemon or systemd timer) — this
  satisfies the letter of "no external cron", but a go-lint pass flagged
  it as a forbidden third-party cron dependency. Re-verify against AI.md
  PART 18 whether an in-process library is acceptable or whether PART 18
  requires a hand-rolled scheduler with zero cron-shaped dependencies,
  and act accordingly. Found during the PART 23-24 lint pass; logged
  here rather than fixed inline since it is unrelated to PART 23-24 and
  PART 18 is already committed.
  Read: AI.md PART 18
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
  - `ssl_renewed` has an embedded template and a config switch but no
    sender: AI.md PART 15 leaves re-issuance inside autocert's on-demand
    TLS-handshake path, so nothing in-process observes a successful
    renewal. It gets wired when PART 15's deferred
    autocert-to-spec-layout bridging lands.
    Read: AI.md PART 15, PART 17 "ssl_renewed"
  - The PART 11 maintainer notification carries the report body as
    inline AES-256-GCM armor rather than a real PGP MIME attachment —
    OpenPGP is still absent from `go.mod` (see the PART 9-11 GPG keypair
    item). The email remains "the CC path, never the primary channel".
    Read: AI.md PART 11 "Submission Flow", PART 17
  - Web-UI toast notifications (`server.notifications.webui.*`) are
    configured and validated but not yet rendered by any template; only
    the email channel is delivered.
    Read: AI.md PART 17 "Notification Systems"
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
- `make build` and `make local` fail whenever `src/client/` exists without
  a `package main` — both targets build `./src/client` unconditionally on
  directory existence. `tests/common.sh` works around it with
  `has_client_main()`; the Makefile should use the same guard.
  Read: AI.md PART 25
- The client binary `shortner-cli` still does not exist (`src/client` has
  only `doc.go`), so every CLI check in `tests/suite.sh` reports SKIP.
  Read: AI.md PART 8, 32
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
  described as deferred rather than shipped — no `shortner-cli`, no
  Swagger/GraphQL, no Tor/I2P, no DNS-01, no PWA/sitemap, no hot reload,
  no cache in the request path, unpopulated business metrics, no
  `ssl_renewed` trigger, no backup `list`/`delete`. `mkdocs build
  --strict` verified clean inside `python:alpine` (built in 2.25s, zero
  warnings, all nav links resolve). `site_url` uses the org-project RTD
  format `https://apimgr-shortner.readthedocs.io` per PART 29's own rule
  for organization accounts; no pre-existing RTD project name was found
  anywhere in the repo.
  Read: AI.md PART 29
- README.md is stale against the shipped code: it lists GeoIP under
  "Planned (not yet shipped)" although PART 19 is implemented, and it
  documents `shortner-cli` with `--server`/`--token`/`--output` as though
  the client binary exists (it does not — `src/client` is a stub).
  Reconcile README.md against `docs/` now that PART 29 is written.
  Read: AI.md PART 29, PART 32
- PART 30 (I18N & A11Y) is DONE for the server binary and the Web UI:
  `src/common/i18n/` (embedded `locales/*.json` for all 7 spec languages,
  `Translate`/`TranslateFormat`/`TranslatePlural`/`IsSupported`/
  `LangFromRequest`/`SetLanguageCookie`/`Direction`/`RawLocale`/
  `Languages`/`CLILanguage`), `cmd/i18n-validate` + the `i18n-validate`
  Makefile target, `src/httpserver/i18n.go` (`LanguageMiddleware`, the
  `t`/`tf`/`tp` FuncMap funcs, the `/locales/{lang}.json` route),
  `src/config/i18n.go` (`server.i18n.*`), `src/lang.go` (the CLI output
  language). 284 `t`/`tf`/`tp` call sites across 22 templates; RTL via
  `dir` on `<html>` plus logical-property CSS; skip links, ARIA
  landmarks, live regions, `.sr-only`, focus-visible and 44px touch
  targets in the CSS.
  Read: AI.md PART 30
- PART 30 deferred sub-item: client-binary i18n. `src/client` is a stub
  and `shortner-cli` does not exist yet, so the client's `--lang` flag,
  its help text, and its own catalog are not built. Do this as part of
  PART 32, reusing `src/common/i18n` unchanged (it imports no project
  package, so the client can embed the same catalog).
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
- PART 30 deferred sub-item: email templates are deliberately NOT part of
  the i18n catalog. PART 17 gives `src/server/template/email/` its own
  `Subject:`/`---` wire format with `{variable}` substitution and a
  custom-override directory, and that system is already shipped;
  multi-language email would need a per-language override directory
  (`{config_dir}/template/email/{lang}/`) that PART 17 does not define.
  Read: AI.md PART 30, PART 17
- PART 30 sub-items with no strings to translate yet, because the feature
  itself is deferred: PWA install/offline strings (no PWA), Swagger and
  GraphQL doc-page strings (neither exists), announcements-banner and
  Web-UI toast strings (rendered nowhere yet), and multi-user
  roles/moderation/registration strings (this project has no user
  accounts). No keys were fabricated for any of them; add the keys when
  the feature lands.
  Read: AI.md PART 30, PART 16

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
