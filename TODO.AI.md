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

## PART 7-8: Binary requirements & server CLI

- `src/common/theme` (Unified Color Palette / `ThemePalette`) is deferred —
  it depends on the PART 16 web frontend theme system, which does not
  exist yet.
  Read: AI.md PART 7 "Theme Package", PART 16 "Unified Color Palette"
- Concrete `src/data/*.json` application data files — no later PART has
  defined their schema/content yet; `src/data/embed.go` embeds the
  directory (currently only `.gitkeep`) so real files just need to be
  added once a PART specifies them.
  Read: AI.md PART 7 "Embedded Assets"
- `src/signal` installs OS signal handlers (`Start()`, non-blocking) and
  the PID-file-removal shutdown hook is already registered in
  `src/main.go`'s `run()`. Still missing: actually closing HTTP
  listeners/flushing logs on shutdown, and making `run()` block until a
  shutdown signal arrives — both require the HTTP server (PART 9+) to
  exist first; `run()` currently returns immediately after startup since
  there's nothing yet to keep the process alive.
  Read: AI.md PART 7 "Default Behavior", PART 8 "Signal Handling &
  Graceful Shutdown"

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
  are wired but always empty until PART 19 GeoIP lands.
  Read: AI.md PART 19

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
- GeoIP location data on `/{slug}/stats` — `page/stats.tmpl` already
  renders "unknown" gracefully when `ClickInfo.Country`/`.Region` are
  empty; the actual GeoIP lookup that would populate those fields is
  PART 19 work, not done here.
- Contact-form email delivery — `contactPost` in `frontend.go` accepts and
  validates the form (including a static math-captcha check) and shows a
  success message, but nothing is sent or persisted; real delivery depends
  on PART 17 (SMTP/notifications), documented as a deliberate no-op in the
  function's doc comment.
- `/server/docs/swagger`, `/server/docs/graphql` — confirmed still out of
  scope per the AI.md `/server` routes table (PART 16); no Swagger/GraphQL
  doc UI exists.
- Home page (`/`) body layout should still be diffed against the prior
  Node.js app (`github.com/casjaydns/csj.lol`) reference assets in
  `docs/reference/csjlol/` for final visual parity — the current
  `page/home.tmpl` was written to the Dracula/light CSS variables and the
  IDEA.md "Frontend design reference" description, but a pixel-level
  comparison against the reference has not been done.

## PART 17-22: Features

- Email & notifications.
  Read: AI.md PART 17
- Internal scheduler (link expiry sweep, cleanup jobs — never external
  cron).
  Read: AI.md PART 18
- GeoIP integration for click-analytics IP anonymization.
  Read: AI.md PART 19, IDEA.md Business logic
- Metrics endpoint.
  Read: AI.md PART 20
- Backup & restore (Argon2id-protected archives). `--maintenance backup`,
  `restore`, `data`, `compliance` flag parsing/dispatch/`--help` are
  already implemented in `src/maintenance.go`; each action currently
  prints "not yet available" and exits 1 — implement the real archive
  create/extract logic here.
  Read: AI.md PART 21
- `--maintenance mode`, `setup` actions (persist app mode, reset config to
  defaults) — flag surface done in `src/maintenance.go`, actions still
  "not yet available".
  Read: AI.md PART 11, 12
- `--maintenance pgp`, `secret`, `token` actions (PGP key management,
  stored-secret management, operator token management) — flag surface
  done in `src/maintenance.go`, actions still "not yet available".
  Read: AI.md PART 11, 21
- `--update` self-update command. Flag parsing/dispatch/`--help` for
  `check`/`yes`/`branch {stable|beta|daily}` are already implemented in
  `src/update.go`; each action currently prints "not yet available" and
  exits 1 — implement the real update-check/download/channel-switch logic
  here.
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

## PART 31: Tor hidden service

- Auto-enable Tor hidden service when Tor is present on the host.
  Read: AI.md PART 31

## PART 32: Client

- `shortner-cli` client binary (currently only a `doc.go` placeholder in
  `src/client/`) — setup wizard, `--server`/`--token`/`--output` flags.
  Read: AI.md PART 32

## Repo-level (calling session, not this bootstrap)

- `git init` and initial commit — intentionally left to the calling
  session; this bootstrap pass did not run any git command.
