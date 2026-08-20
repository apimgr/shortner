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
21. Enable I2P by default → PART 31.2 is opt-in (`features.i2p.enabled`)

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
- Last read AI.md: 2026-08-20 (PART 17 Email & Notifications — template
  storage/format/variables, SMTP auto-detection, environment variable
  priority, the SMTP requirement, template preview/validation, test send)
- Current task: implemented PART 17 (Email & Notifications) — `src/notify/`
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
- Previous task: implemented PART 11's HTTP layer — `src/httpserver/`
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
- Relevant PARTs: 0-6, 9-22 done; 7-8, 23-32 tracked in
  TODO.AI.md (PART 11 has two deferred sub-items — GPG keypair
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
  and the Windows reboot-cleanup notice; and a pre-existing unrelated
  `gofmt` violation in `src/metrics/metrics.go` — all logged in
  TODO.AI.md rather than silently dropped)
