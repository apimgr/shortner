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
- PART 17, 20-22 are NOT implemented yet — tracked in `TODO.AI.md`

---
For complete details, see AI.md PART 17, 18, 19, 20, 21, 22
