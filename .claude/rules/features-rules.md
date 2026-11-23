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
- PART 17: `src/notify/` owns email templates, event schemas, validation,
  SMTP delivery, and the notifier. Email callers are wired from startup,
  security reports, backup, certificate lifecycle, and update operations.
  Missing SMTP means no delivery and no queue.
- PART 18: `src/scheduler/` and `src/db/scheduler.go` implement the built-in
  scheduler. Registered work includes token/security cleanup, log rotation,
  disk and health checks, TLS renewal, GeoIP/CVE/blocklist updates, PGP
  keyserver retries, backups, updates, and conditional Tor/I2P health checks.
- PART 19: `src/geoip/` manages local database downloads and lookup; HTTP
  middleware applies country controls and click recording stores approximate
  location only after IP anonymization.
- PART 20: `src/metrics/` exposes Prometheus, Grafana, and Loki formats and
  records HTTP, database, scheduler, runtime, system, link, click, and token
  metrics. Cache counter types exist, but the shared cache is not instrumented.
- PART 21: `src/backup/`, `src/scheduler/backup.go`, and `src/backup_cli.go`
  implement encrypted archive create, verify, list, delete, restore, retention,
  and scheduled backup operations. Passwords are entered interactively.
- PART 22: `src/updater/`, `src/update.go`, and `src/scheduler/update.go`
  implement release lookup, channel/defer policy, checksum verification,
  replacement, service restart, and scheduled checks.
- Tor and I2P lifecycle tasks are implemented in `src/scheduler/overlay.go`
  and their managers under `src/tor/` and `src/i2p/`; I2P health is only
  scheduled when enabled.

Use AI.md and the implementation as the status source. Do not copy deferred
feature lists or TODO references from an old audit snapshot into this guide.

---
For complete details, see AI.md PART 17, 18, 19, 20, 21, 22
