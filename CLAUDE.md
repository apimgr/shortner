# shortner — Claude Code Quick Reference

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## Self-Check Before ANY Code Change
1. Have I read the relevant PART in AI.md? (If no → read it)
2. Does this follow the spec EXACTLY? (If unsure → check spec)
3. Am I guessing or do I KNOW from the spec? (If guessing → read spec)
4. Would this pass the compliance checklist? (AI.md FINAL section)

**WHEN IN DOUBT: READ THE SPEC. DO NOT GUESS.**

## Binary Terminology
- **server** = `shortner` (main binary, runs as service)
- **client** = `shortner-cli` (REQUIRED companion, CLI/TUI/GUI)

## NEVER Do (Top 21) - VIOLATIONS ARE BUGS
1. Use bcrypt for config/backup passwords → Use Argon2id
2. Put Dockerfile in root → `docker/Dockerfile`
3. Use CGO → CGO_ENABLED=0 always
4. Hardcode dev values → Detect at runtime
5. Use external cron → Internal scheduler (PART 18)
6. Store config/backup passwords plaintext → Argon2id (API tokens use SHA-256)
7. Create premium tiers → All features free, no paywalls
8. Use Makefile in CI/CD → Explicit commands only
9. Guess or assume values a command can produce → Run the command
10. Skip platforms → Build all 8 (linux/darwin/windows/freebsd × amd64/arm64)
11. Client-side rendering (React/Vue) → Server-side Go templates
12. Require JavaScript for core features → Progressive enhancement only
13. Let long strings break mobile → Use word-break CSS
14. Skip validation → Server validates EVERYTHING
15. Implement without reading spec → Read relevant PART first
16. Modify AI.md content → READ-ONLY SPEC. Project changes go in IDEA.md.
17. Edit `## Project variables` in IDEA.md without confirming with the user
18. Read an image larger than 1000×1000 directly into context
19. Use a non-conforming IDEA.md without migration
20. Serve an overlay address over HTTPS → `.onion`/`.b32.i2p` are always
    `http://`; no cert, no HSTS, no redirect, no upgrade-insecure-requests
21. Enable I2P by default → PART 31.2 is opt-in (`server.i2p.enabled`)

## ALWAYS Do - NON-NEGOTIABLE
1. Read AI.md before implementing ANY feature
2. Server-side processing (server does the work, client displays)
3. Mobile-first responsive CSS
4. All features work without JavaScript
5. Tor hidden service support (auto-enabled if Tor found)
6. Built-in scheduler, GeoIP, metrics, email, backup, update
7. All settings configurable via API and config file
8. Client binary for the project
9. Commit often via `gitcommit <command>` — small, focused commits, each
   with a fresh accurate `.git/COMMIT_MESS`. Subagents do not commit —
   complete edits and report back; the parent owns the commit.

## File Locations
- Config: `{config_dir}/server.yml`
- Data: `{data_dir}/`
- Logs: `{log_dir}/`
- Source: `src/`
- Docker: `docker/`

## Where to Find Details
- AI behavior: `.claude/rules/ai-rules.md` (PART 0, 1)
- Project structure: `.claude/rules/project-rules.md` (PART 2, 3, 4)
- Config/modes: `.claude/rules/config-rules.md` (PART 5, 6, 12)
- Binaries: `.claude/rules/binary-rules.md` (PART 7, 8, 32)
- Backend: `.claude/rules/backend-rules.md` (PART 9, 10, 11, 31 — 31.1 Tor,
  31.2 I2P)
- API: `.claude/rules/api-rules.md` (PART 13, 14, 15)
- Frontend/WebUI: `.claude/rules/frontend-rules.md` (PART 16)
- Features: `.claude/rules/features-rules.md` (PART 17-22)
- Service: `.claude/rules/service-rules.md` (PART 23, 24)
- Makefile: `.claude/rules/makefile-rules.md` (PART 25)
- Docker: `.claude/rules/docker-rules.md` (PART 26)
- CI/CD: `.claude/rules/cicd-rules.md` (PART 27)
- Testing/docs/i18n: `.claude/rules/testing-rules.md` (PART 28, 29, 30)
- Project intent (WHAT): `IDEA.md`
- Full spec (HOW, ~48k lines): `AI.md` ← **SOURCE OF TRUTH**

## Current Project State
- Last read AI.md: 2026-08-20 (PART 32 CLIENT in full — overview, binary
  naming, open-API access, config-file permissions, CLI auto-update and
  the flag-to-config save rules, automatic mode detection, display
  environment detection, the first-run flow, GUI requirements, theming,
  responsive layout, professional UI/UX standards, configuration and
  `cli.yml`, standard flags, shell completions, `--help`/`--version`
  output, commands, authentication, HTTP client identity, URL encoding,
  output formats, smart argument detection, build integration, and the
  TUI requirements including Minimum Features)
- Current task: implemented PART 32 (Client). New binary `shortner-cli`
  under `src/client/`: `main.go` (entry point), `cmd/` (flag parsing,
  dispatch with smart argument detection, table/json/yaml/plain output,
  `--shell` completions for all 8 shells, translated `--help`/
  `--version`, and the 6-step CLI auto-update — `/api/autodiscover`
  discovery with a TTL'd on-disk cache and negative caching,
  `cli_min_version` refusal gate, download, mandatory SHA-256
  verification, atomic `replaceBinary()` per OS, re-exec with the
  original argv), `config/` (`cli.yml`, precedence CLI > env `SHORTNER_*`
  > file > compiled default, the flag-persists-only-when-empty-or-invalid
  rule), `paths/` (user-scope XDG paths, 0700 dirs / 0600 files, never a
  system path), `api/` (typed client, `shortner-cli/{version}`
  User-Agent, `src/common/urlutil` encoding), `tui/` (bubbletea app with
  keyboard navigation, search, `?` help, `q`/ctrl+c quit, resize
  handling, the 7-breakpoint responsive layout, viewport scrolling,
  Unicode/ASCII symbol sets, and both the ANSI terminal palette and the
  dark/light lipgloss themes), and `setup/` (the wizard, mode order
  SSH/Mosh → TUI, display → GUI, terminal → TUI, else error). A new
  `client.*` i18n section (22 keys) was added to all 7 locales. Verified
  in Docker: `gofmt -l .` clean, `go build ./...`, `go vet ./...`, and an
  8-platform cross-compile of both `./src` and `./src/client`. Deferred:
  the native GUI (cgo vs `CGO_ENABLED=0`, so it stays behind a `gui`
  build tag and falls back to the TUI) and the server side of CLI
  auto-update (`/api/autodiscover`, `/cli/binaries/*`, and PART 32's
  `cli.*` audit events) — both logged in TODO.AI.md.
- Previous task: implemented PART 31 (Overlay Networks — Tor & I2P) in
  full. New packages `src/tor/` (bine-driven dedicated Tor process, v3
  hidden service, SafeLogging, dedicated loopback backend port, HAProxy
  PROXY-protocol circuit-ID ingest, vanity search/apply, key import,
  health monitor), `src/i2p/` (Model A i2pd subprocess with a regenerated
  `tunnels.conf`, Model B external SAMv3 bridge over a raw `net.Conn`,
  persisted destination, `.b32.i2p` derivation, provider resolution
  i2pd → SAM → warn-and-continue), and `src/common/netutil/`. CLI:
  `src/tor_cli.go` (`tor status|validate|restart|regenerate|vanity
  start|vanity apply|import-keys <path>`), `src/i2p_cli.go`, both listed
  in `--help` (`cli.cmd_tor`/`cli.cmd_i2p` added to all 7 locales).
  HTTP layer: `src/httpserver/overlay.go` + `urlvars.go` (priority-0
  overlay detection ahead of proxy headers, `tor:{circuit_id}` identity
  so `127.0.0.1` is never logged as a Tor client, I2P trusted-proxy-gate
  exception, `BuildURL` always `http://`), `headers.go`/`middleware.go`
  (never HSTS, never `upgrade-insecure-requests`, never an HTTPS
  redirect on an overlay; `Secure` cookies still set; `Onion-Location`
  on clearnet HTML 2xx top-level navigations only), `pagedata.go` (UTC
  timestamps for Tor requests, footer/help addresses, and the PART 16
  footer-variable expansion incl. `{onion_address}`/`{i2p_address}`),
  `health.go` (`features.tor.*`/`features.i2p.*` + `checks.tor`/
  `checks.i2p`). Config `src/config/overlay.go` (`server.tor.*`,
  `server.i2p.*`, opt-in I2P default false) + validation warnings.
  Scheduler `src/scheduler/overlay.go` (`tor_health` every 10m always,
  `i2p_health` every 10m only when opt-in). Wiring `src/overlay.go` +
  `src/main.go` (managers built before the HTTP server, started in the
  background so Tor bootstrap never delays the clearnet listener,
  `Tor: {onion_address}` printed once, overlay rows in the startup
  banner, stop hooks), `src/status.go` (`--status` Tor/I2P blocks).
  Frontend: footer I2P block, `/server/help#i2p-access`, CSS, and 9 new
  i18n keys per locale. Verified in Docker: `gofmt -l .` clean,
  `go build ./...`, `go vet ./...`, `go test ./... -cover` all pass;
  `src/tor` 63.9%, `src/i2p` 73.5%, `src/common/netutil` 87.0%,
  `src/httpserver` 72.6%. Deferred is environment-only: no `tor`,
  `i2pd`, or SAM bridge exists in this container, so bootstrap,
  descriptor publication, a live circuit-ID PROXY header, and a real
  eepsite tunnel are unexercised — logged in TODO.AI.md.
- Previous task: implemented PART 30 (I18N & A11Y) — `src/common/i18n/`
  (embedded 7-language catalog, literal `{token}` interpolation, CLDR
  plurals, request/CLI language resolution), `src/cmd/i18n-validate` + the
  `i18n-validate` Makefile target, `src/httpserver/i18n.go`
  (`LanguageMiddleware`, `t`/`tf`/`tp` template funcs,
  `/locales/{lang}.json`), `src/config/i18n.go` (`server.i18n.*`),
  `src/lang.go` + `--lang` on the server binary, 284 translation call
  sites across 22 templates, RTL/`dir` support and the a11y pass
  (skip links, landmarks, live regions, `.sr-only`, focus-visible,
  44px targets). Verified in Docker: `gofmt -l .` clean, `go vet ./...`,
  `go build ./...`, `go test ./... -cover` all pass;
  `src/common/i18n` 74.7%, `src` 61.6%; `i18n-validate` reports
  7 locales valid, 497 keys each. Deferred (client binary, subcommand
  help text, Go `error` strings, email templates, PWA/Swagger/GraphQL/
  toast strings) are logged in TODO.AI.md.
- Previous task: implemented PART 27 (CI/CD Workflows), GitHub Actions only
  per the provider-detection rule — `.github/workflows/ci.yml` (`lint`,
  `secret-scan` with the before/after-SHA range logic, `workflow-policy`
  SHA-pin grep, `test` with the 60%-coverage gate, `build`, `vuln-scan`,
  `image-scan` gated on `docker/Dockerfile` existing; security jobs also
  run on the Monday 06:00 UTC cron, build/test/lint skip on schedule),
  `release.yml` (tag push `v*`/semver, tag-ref concurrency, 8-platform
  build matrix, conditional CLI build gated on `src/client/**`, SBOM via
  `cyclonedx-gomod`, sha256/sha512 checksums, `attest-build-provenance`,
  `softprops/action-gh-release`), `beta.yml` (push to `beta`, prerelease
  tagged with the computed version), `daily.yml` (3am UTC cron + push to
  main/master, rolling `daily` tag deleted and recreated each run — this
  resolves the TODO.AI.md "Daily release identity" follow-up, since
  `src/updater` already expected exactly this rolling-tag/sha256.txt
  shape), `docker.yml` (`build-standard` skipped on schedule,
  `build-devel` built on schedule/dispatch/non-tag push, `ghcr.io`,
  QEMU+buildx for linux/amd64+arm64, OCI labels/annotations). Every
  third-party Action SHA (11 total across the 5 files) was independently
  verified against GitHub's tag refs via `gh api` before pinning —
  annotated tags dereferenced to their underlying commit — and all 11
  matched AI.md's example SHAs exactly, so no example was blindly
  transcribed without verification. `act --list -W {file}` passes for all
  5 files; the `workflow-policy` SHA-pin grep also passes when run
  locally. Not yet exercised: an actual triggered run (no push has
  happened against these files yet) — see TODO.AI.md.
- Previous task: implemented PART 23 + PART 24 (Privilege Escalation &
  Service Support) — new `src/service/` package: `escalate.go` /
  `escalate_windows.go` (per-OS escalation chains — Linux
  root→sudo→su→pkexec→doas, macOS root→sudo→osascript, BSD
  root→doas→sudo→su, Windows Administrator→UAC→runas — plus
  `IsElevated`, `CanEscalate`, `ExecElevated`), `sysuser.go` (the
  dedicated `{internal_name}` user/group with matching UID==GID chosen
  899→200, 399→200 on macOS, skipping the verbatim reserved-ID map;
  no-login shell, no password, home = config dir; Linux
  `groupadd`/`useradd --system`, macOS `dscl` with IsHidden 1, FreeBSD
  `pw`; a `.service-account` marker so uninstall only deletes an account
  this binary created), `detect.go` (init-system detection — SysVinit
  only when both `openrc-run` and `systemctl` are absent), `template.go`
  (systemd, OpenRC, SysVinit, runit `run` + `log/run`, rc.d, launchd),
  the per-manager verb files (`systemd.go`, `openrc.go`, `sysvinit.go`,
  `runit.go`, `rcd.go`, `launchd.go`, `windows.go` — Virtual Service
  Account via an empty `ServiceStartName`), and
  `reload_unix.go`/`reload_windows.go` (SIGHUP fallback so reload never
  silently becomes a restart). `src/service.go` wires
  `--service start|stop|restart|reload|--install|--disable|--uninstall|
  --help` into `src/main.go`; `--install` installs the service file,
  enables, and starts (user/group/directory creation stays in normal
  startup), and `--uninstall` always prompts "This will delete ALL data,
  configs, and the system user. Continue? [y/N]" before anything
  destructive. Verified in Docker: `go build`/`go vet`/`go test ./...
  -cover` all pass; `src/service` 64.6%, `src` 60.7%; all 8 target
  platforms cross-compile. Deferred (environment limits, not unbuilt
  work): no live init system exists in the build container, so
  end-to-end install/uninstall is asserted at the command-line and
  install-path level plus one real systemd `--user` cycle over a
  temporary HOME; account creation is command-line asserted only (a test
  must not create a real system user); Windows SCM registration
  cross-compiles but never runs — all logged in TODO.AI.md.
- Previous task: implemented PART 17 (Email & Notifications) — `src/notify/`
  (`template.go` the `Subject:`/`---`/body wire format and `{variable}`
  substitution, `events.go` all 12 events plus their variable tables and
  config switches, `store.go` custom `{config_dir}/template/email/` over
  embedded `src/server/template/email/` defaults with live reload and
  reset-by-deletion, `validate.go` PART 17's error/warning tables with
  "Did you mean {x}?" suggestions, `detect.go` the priority-ordered SMTP
  auto-detection with an EHLO handshake test, `smtp.go` RFC 5322 messages
  over `net/smtp` + `crypto/tls` with CRLF-injection and dot-stuffing
  guards, `notify.go` the nil-safe `Notifier` that makes "no SMTP = no
  email, ever" true by construction), plus `src/config/notifications.go`
  (`server.notifications.*`, `ApplySMTPEnv` for the `SMTP_*` overrides),
  `src/notifications.go` (startup connection test, auto-detected server
  persisted, graceful disable-on-failure), `src/email_cli.go` (the
  positional `email test|list|preview|validate|reset` subcommand), and
  the 12 embedded default templates. Every event has a real call site:
  startup/shutdown (`src/main.go`), `security_alert`
  (`src/httpserver/ipblock.go`), the PART 11 Submission Flow steps 4/5
  emails (`src/httpserver/securityreport.go`), contact-form relay
  (`src/httpserver/frontend.go`), backup complete/failed and
  `scheduler_error` with PART 17's suppression rule
  (`src/scheduler/notify.go`), SSL expiring/renewal-failed
  (`src/scheduler/tasks.go`), and update available/installed
  (`src/scheduler/update.go`). Verified in Docker: `go build`/`go vet`/
  `go test ./... -cover` all pass; `src/notify` 78.8%, `src` 64.6%,
  `src/config` 78.1%, `src/scheduler` 80.3%. Deferred: `ssl_renewed`
  (no in-process observer until PART 15's autocert bridging), the PART 11
  maintainer email using inline AES armor instead of a PGP MIME
  attachment, and Web-UI toast rendering — all logged in TODO.AI.md.
- Earlier task: implemented PART 11's HTTP layer — `src/httpserver/`
  (`headers.go` header matrix/CSP/Permissions-Policy/COOP-COEP-CORP/HSTS/
  Clear-Site-Data/Server-Timing, `privacy_signals.go` DNT+GPC,
  `secfetch.go`, `reports.go` Reporting API always-204,
  `wellknown.go` robots.txt/security.txt/llms.txt/pgp-key.asc under the
  allowlist-only well-known contract, `securitypages.go`
  `/server/security[/policy|/thanks]` + `/server/dpo`,
  `securityreport.go` the `/server/contact?security_id=` mode switch,
  `ipblock.go` allowlist/temporary+permanent blocks/abuse detection wired
  into the PART 5 middleware order), plus `src/security/seal.go`
  (AES-256-GCM at-rest sealing), `src/security/securityid.go`,
  `src/security/bot.go`, `src/db/securityreport.go` (`security_reports`
  table, sealed bodies only), `server.security.*` +
  `server.contact.security`/`.dpo` config, and the `ip_block_release`
  per-minute scheduler task registered in `src/main.go`. Deferred: GPG
  keypair CLI and `/server/security/report/{tracking_id}` — logged in
  TODO.AI.md. (The two security-report notification emails were deferred
  here and are now sent, via PART 17.)
- Earlier task: implemented Update Command (AI.md PART 22) — `src/updater/`
  (`updater.go` GitHub Releases lookup + cumulative channel selection +
  `defer_days` eligibility + SHA-256 verification; `state.go` cached
  `update.json`; build-tagged `update_unix.go`/`update_windows.go` binary
  replacement and `service_{linux,darwin,bsd,windows,other}.go` restart),
  `src/update.go` (`check`/`yes`/`branch`), `src/scheduler/update.go`
  (`update_check` task, notify-once-per-version, `auto_install` off by
  default), `server.update.*` config + `validateUpdate` warnings.
  Verified in Docker: `go build`/`go vet`/`go test ./... -cover` pass;
  all 8 target platforms cross-compile.
- Earlier task: implemented Backup & Restore (AI.md PART 21) — `src/backup/`
  (`crypt.go` AES-256-GCM sealed under an Argon2id-derived key reusing
  `src/security`'s existing parameters; `archive.go` tar+gzip with
  manifest.json + path-traversal guard; `backup.go` create/verify/
  delete-on-failure; `verify.go` the full 7-check suite including SQLite
  integrity check; `retention.go` yearly>monthly>weekly>daily tiering with
  disk-space/threshold checks; `restore.go` the authorization table (empty
  DB/root+confirm/service user+token/denied) and atomic file placement;
  `audit.go` all 8 PART 21 audit events). Wired into
  `src/scheduler/backup.go` (`backup_daily` 8-step flow, `backup_hourly`),
  `src/backup_cli.go` (interactive password prompt, no password flag ever),
  `src/maintenance.go` (`backup`/`restore` dispatch), `src/main.go` (audit
  logger + BackupDeps), `src/config/config.go`/`limits.go`
  (`server.backup.*`, `server.compliance.enabled`, warn-don't-error
  validation). Verified in Docker: `go build`/`go vet`/`go test ./...
  -cover` all pass; `src/backup` 77.6%, `src/config` 86.0%,
  `src/scheduler` 81.8%, `src` 61.5% coverage (gate is 60%); go-lint clean.
- Relevant PARTs: 0-6, 9-24, 27, 31, 32 done; 7-8, 25-26, 28-30 tracked in
  TODO.AI.md (PART 32 has two deferred sub-items — the native GUI behind
  a `gui` build tag, and the server side of CLI auto-update; PART 11 has
  two deferred sub-items — GPG keypair
  management CLI and the `/server/security/report/{tracking_id}` status
  page; PART 15 has deferred sub-items — DNS-01 provider matrix,
  credential encryption at rest, autocert-to-spec-layout bridging; PART 16
  has deferred sub-items — PWA, sitemap.xml, favicon.ico,
  announcements-banner rendering, Web-UI toast rendering,
  Swagger/GraphQL doc pages; PART 17 has three deferred sub-items —
  `ssl_renewed` has no in-process observer until PART 15's autocert
  bridging lands, the PART 11 maintainer email carries inline AES armor
  instead of a PGP MIME attachment, and Web-UI toasts are configured but
  not rendered; PART 20 has three deferred sub-items — business metrics
  (`LinksTotal`/`LinksCreated24h`/`LinksClicked24h`/`APITokensActive`)
  unpopulated, cache metrics inert since `src/cache` is unwired, Tor/I2P
  metrics deferred to PART 31; PART 21 has five deferred sub-items —
  `--maintenance backup list`/`delete` subcommands, a backup-metadata
  table in `server.db`, the encryption hint not surfaced at restore,
  restore not stopping/restarting the running server or snapshotting
  first; PART 22 has two deferred sub-items — download progress output
  and the Windows reboot-cleanup notice; PART 23/24 are fully built but
  three things cannot be exercised in this environment — a live
  init-system install/uninstall cycle, real system account creation, and
  Windows SCM registration; PART 27 is fully built for GitHub Actions
  (Gitea/Forgejo/GitLab/Jenkins do not apply, this repo's only provider is
  GitHub) and has now been verified end-to-end with a real triggered run —
  the first push surfaced 3 pre-existing staticcheck/gofmt violations
  (`src/metrics/dbdriver.go` deprecated `Begin()` fallback, unused
  `gcLast` field, a `gofmt` misalignment, and an identical-expression
  `!=` comparison in `src/security/security_test.go`) which were fixed
  in a follow-up commit; `ci.yml`/`daily.yml`/`docker.yml` all completed
  `success` on the second push; and the previously-logged `gocron`
  external-cron-dependency review item under PART 18 — all logged in
  TODO.AI.md rather than silently dropped)
