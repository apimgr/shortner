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
- Last read AI.md: 2026-08-20 (PART 20 Metrics, full section, to
  implement and verify the built-in Prometheus/Grafana/Loki metrics
  endpoints)
- Current task: implemented Metrics (AI.md PART 20) — `src/metrics/`
  (Prometheus registry + HTTP/DB/scheduler/system/runtime metrics,
  instrumented `sql` driver wrapper), `src/httpserver/metrics.go`
  (`metricsAuth` mandatory per-service constant-time bearer-token check,
  `RegisterMetricsRoutes`/`RegisterVersionedMetricsRoutes` mounting
  `/server/metrics[/prometheus|grafana|loki]` +
  `/api/{api_version}/server/metrics` + `/api/metrics` + root `/metrics`
  aliases — same handler, never a redirect — Grafana dashboard JSON,
  Loki JSON handler), `src/applog/logger.go` (bounded in-memory ring
  buffer + `Recent()` backing the `loki` service). Wired into
  `src/httpserver/server.go`, `middleware.go` (HTTP/rate-limit/auth
  metrics), `src/scheduler/scheduler.go` (task metrics). Verified in
  Docker: `go build`/`go vet`/`go test ./... -cover` all pass;
  `src/metrics` 68.7%, `src/httpserver` 75.2%, `src/applog` 84.5%
  coverage (gate is 60%).
- Relevant PARTs: 0-6, 12-16, 18-20 done; 7-11, 17, 21-32 tracked in
  TODO.AI.md (PART 15 has deferred sub-items — DNS-01 provider matrix,
  credential encryption at rest, autocert-to-spec-layout bridging; PART 16
  has deferred sub-items — PWA, sitemap.xml, favicon.ico,
  announcements-banner rendering, contact-form email delivery,
  Swagger/GraphQL doc pages; PART 19 has one deferred sub-item — allowlist
  bypass of country blocking, waiting on PART 11's allowlist backing
  store; PART 20 has three deferred sub-items — business metrics
  (`LinksTotal`/`LinksCreated24h`/`LinksClicked24h`/`APITokensActive`)
  unpopulated, cache metrics inert since `src/cache` is unwired, Tor/I2P
  metrics deferred to PART 31 — all logged in TODO.AI.md rather than
  silently dropped)
