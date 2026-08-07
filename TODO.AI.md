# TODO.AI.md

AI-owned task list for `shortner`. PART 0-6 bootstrap (directory layout,
project files, build system, dependencies, config, metadata) is complete.
Everything below is deferred work, in dependency order.

## Foundation follow-ups (small, do first)

- Windows admin-token detection in `src/paths/paths_windows.go` is a
  minimal probe (`\\.\PHYSICALDRIVE0` open test) — verify against real
  Windows UAC behavior and harden if needed.
  Read: AI.md PART 4
- `go.sum`/LICENSE.md third-party attribution section is currently a
  placeholder — populate once real dependencies (DB driver, HTTP router,
  etc.) are added in PART 9/10.
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

- Error handling & caching layer.
  Read: AI.md PART 9
- Database schema for `Link` and `Click` models (see IDEA.md Business
  logic), using `modernc.org/sqlite`.
  Read: AI.md PART 10
- Owner token generation/storage (SHA-256 hashed, one-time reveal),
  `server.token` operator auth, Argon2id for any stored passwords.
  Read: AI.md PART 11
- Apply the Tier 1/2/3 Public Endpoint Safety Principle to every new
  endpoint as it's built.
  Read: AI.md PART 11

## PART 12: Server configuration

- Full `server.yml` schema beyond the bootstrap defaults in
  `src/config/config.go`.
  Read: AI.md PART 12
- URL & FQDN detection (reverse-proxy headers preferred: `X-Forwarded-Host`/
  `-Proto`/`-Port`/`-Prefix`, `X-Real-Host`, `X-Original-Host`, etc.),
  Request ID middleware, and auth-token header parsing — all require
  `http.Request` handling, which doesn't exist until the HTTP server
  (PART 9+) is built.
  Read: AI.md PART 12 "URL & FQDN Detection", PART 14

## PART 13-15: Health, API, TLS

- `/server/healthz` + `/api/{api_version}/server/healthz` handlers.
  Read: AI.md PART 13
- Core API routes: create link, resolve slug, `/{slug}/stats` click
  analytics sub-resource, owner-token-gated management routes.
  Read: AI.md PART 14, IDEA.md Business logic
- Let's Encrypt / TLS support.
  Read: AI.md PART 15

## PART 16: Web frontend

- Server-rendered pages: create-link form, redirect landing, `/stats/{id}`
  analytics view, mobile-first CSS, WCAG 2.1 AA, PWA manifest.
  Read: AI.md PART 16
- Home page (`/`) body layout must match the prior Node.js app
  (`github.com/casjaydns/csj.lol`): centered logo, single-column create-link
  card, success state with short URL + "create another", Dracula color
  scheme via CSS custom properties, matching nav/footer structure. Adapt
  to server-rendered `<form>` (no CDN JS dependency, must work with JS
  disabled) — do not copy the Vue/CDN implementation verbatim.
  Reference assets: `docs/reference/csjlol/`.
  Read: IDEA.md "Frontend design reference", AI.md PART 16

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
