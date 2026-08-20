# Features Rules (PART 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use an external cron for scheduled work — internal scheduler only
  (PART 18)
- Skip GeoIP-based IP anonymization for click analytics (IDEA.md business
  logic requires anonymized IPs)

## CRITICAL - ALWAYS DO
- Built-in scheduler, GeoIP, metrics, email/notifications, backup, and
  update-command support (PART 17-22) are all in scope for the full build
- Backup/restore uses Argon2id-protected archives, never plaintext
  credentials

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Cron | internal scheduler (PART 18), never external cron/systemd timers | PART 1, 18 |
| GeoIP use | click-analytics IP anonymization | PART 19, IDEA.md |
| Update mechanism | `--update` CLI flag path | PART 22 |
| Overlay health tasks | `tor_health` every 10m; `i2p_health` every 10m (only when I2P opt-in enabled) | PART 18, 31 |

## QUICK REFERENCE
- PART 18 (Scheduler) is implemented: `src/scheduler/`, `src/db/scheduler.go`,
  `src/scheduler_cli.go`, wired into `src/main.go`. All 12 required built-in
  tasks are registered; 5 do real work (token_cleanup, log_rotation,
  healthcheck_self, ssl_renewal, geoip_update), 7 honestly skip pending
  their own subsystem (PART 9/11, 9, 22, 21 x2, 31.1, 31.2) — see
  `TODO.AI.md`.
- PART 19 (GeoIP) is implemented: `src/geoip/` (Manager/Lookup/IsBlocked/
  Download, `oschwald/maxminddb-golang` against `sapics/ip-location-db`),
  `server.geoip.*` config, country-blocking middleware, click-analytics
  country/region enrichment, `geoip_update` scheduler task, CC BY 4.0
  attribution (`page/about.tmpl`, `LICENSE.md`). One deferred sub-item
  (allowlist bypass, waiting on PART 11) — see `TODO.AI.md`.
- PART 20 (Metrics) is implemented: `src/metrics/` (Prometheus registry +
  HTTP/DB/scheduler/system/runtime metrics, instrumented `sql` driver),
  `src/httpserver/metrics.go` (`metricsAuth` per-service bearer-token
  auth, `RegisterMetricsRoutes`/`RegisterVersionedMetricsRoutes` mounting
  `/server/metrics[/prometheus|grafana|loki]` + versioned/unversioned/root
  aliases — same handler, never a redirect), `src/applog` in-memory ring
  buffer backing the `loki` service. Deferred sub-items — business
  metrics unpopulated, cache metrics inert (`src/cache` unwired), Tor/I2P
  metrics deferred to PART 31 — see `TODO.AI.md`.
- PART 21 (Backup & Restore) is implemented: `src/backup/` (AES-256-GCM
  encryption keyed via `src/security`'s existing Argon2id parameters,
  tar+gzip archive with manifest.json, 7-check verification suite incl.
  SQLite integrity check, yearly>monthly>weekly>daily retention with
  disk-space checks, restore authorization table, all 8 PART 21 audit
  events), `src/scheduler/backup.go` (`backup_daily`/`backup_hourly`
  tasks), `src/backup_cli.go` (`--maintenance backup`/`restore`,
  interactive password prompt only — never a CLI flag). Deferred
  sub-items — `backup list`/`delete` subcommands, backup-metadata table
  in `server.db`, encryption hint not surfaced at restore, restore not
  stopping/restarting the server or snapshotting first — see
  `TODO.AI.md`.
- PART 17, 22 are NOT implemented yet — tracked in `TODO.AI.md`

---
For complete details, see AI.md PART 17, 18, 19, 20, 21, 22
