# Features Rules (PART 17-22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use an external cron for scheduled work — internal scheduler only
  (PART 18)
- Skip GeoIP-based IP anonymization for click analytics (IDEA.md business
  logic requires anonymized IPs)
- Queue email, retry it, or log "would have sent" when SMTP is missing —
  PART 17 says no SMTP means email is simply off

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
| No SMTP | email completely disabled — no send, no queue, no "would have sent" log | PART 17 |
| Email templates | embedded defaults, custom override in `{config_dir}/template/email/`, reset = delete the file | PART 17 |
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
- PART 22 (Update Command) is implemented: `src/updater/` (GitHub Releases
  lookup, cumulative stable/beta/daily channels, `defer_days` eligibility,
  mandatory SHA-256 verification against the release's `sha256.txt`,
  build-tagged binary replacement — Unix `os.Rename` over the running
  binary, Windows rename-to-`.old` + `MOVEFILE_DELAY_UNTIL_REBOOT` — and
  per-platform service restart), `src/update.go`
  (`--update [check|yes|branch {stable|beta|daily}]`, bare `--update` =
  `yes`, `--maintenance update` alias), `src/scheduler/update.go`
  (`update_check` task: notify-only unless `server.update.auto_install`,
  fires once per version). Deferred sub-items — the `update_available`
  email event (needs PART 17), download progress output, and the Windows
  reboot-cleanup notice — see `TODO.AI.md`.
- PART 17 (Email & Notifications) is implemented: `src/notify/`
  (`template.go` the `Subject:`/`---`/body wire format + `{variable}`
  substitution, `events.go` all 12 events and their variable tables,
  `store.go` custom `{config_dir}/template/email/` over embedded
  `src/server/template/email/` defaults with live reload and
  reset-by-deletion, `validate.go` errors/warnings + "Did you mean {x}?",
  `detect.go` priority-ordered SMTP auto-detection with an EHLO handshake
  test, `smtp.go` RFC 5322 messages over `net/smtp` + `crypto/tls`
  (auto/starttls/tls/none), `notify.go` the nil-safe `Notifier`),
  `config.ApplySMTPEnv` (`SMTP_*` overrides), `src/notifications.go`
  (startup check + auto-detected server persisted), and `src/email_cli.go`
  (`email test|list|preview|validate|reset`). Every event is wired to a
  real call site: startup/shutdown, `security_alert` (abuse detection),
  the PART 11 security-report emails, backup complete/failed,
  `scheduler_error` (with PART 17's suppression rule), SSL expiring/
  renewal-failed, and update available/installed. Deferred sub-items —
  `ssl_renewed` has no in-process observer until PART 15's autocert
  bridging lands, the PART 11 maintainer email uses inline AES armor
  instead of a PGP MIME attachment, and Web-UI toasts are configured but
  not rendered — see `TODO.AI.md`.
- PART 18's overlay health tasks are live: `tor_health` is registered
  unconditionally (Tor is auto-enabled whenever the `tor` binary exists)
  and `i2p_health` only when `server.i2p.enabled` is true. Both are in
  `src/scheduler/overlay.go` and probe the real PART 31 managers,
  restarting a provider that stopped answering.

---
For complete details, see AI.md PART 17, 18, 19, 20, 21, 22
