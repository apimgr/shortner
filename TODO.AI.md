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

- Embed static assets via Go `embed`.
  Read: AI.md PART 7
- Implement `--service`, `--maintenance`, `--status`, `--update`, `--daemon`
  server subcommands (currently absent from `src/main.go` by design — out
  of PART 0-6 scope).
  Read: AI.md PART 8, 22, 23, 24
- First-run banner with URLs/version, PID file support.
  Read: AI.md PART 7

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
- Backup & restore (Argon2id-protected archives).
  Read: AI.md PART 21
- `--update` self-update command.
  Read: AI.md PART 22

## PART 23-24: Privilege escalation & service

- `--service` install/uninstall (systemd, launchd, Windows service).
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
