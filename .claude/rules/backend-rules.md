# Backend Rules (PART 9, 10, 11, 31 — Overlay Networks: Tor & I2P)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Expose Tier 1 secrets publicly, ever (not even in debug mode): DB
  credentials, internal IPs/hostnames, tokens, private keys, other users'
  PII, filesystem paths, account-existence signals, exact rate-limit values
- Use bcrypt for config/backup passwords — Argon2id (API tokens: SHA-256)
- Use CGO database drivers — `modernc.org/sqlite` (pure Go) only
- Serve an overlay address over HTTPS — `.onion`/`.b32.i2p` are ALWAYS
  `http://`; never issue an HTTPS redirect, HSTS header, or
  `upgrade-insecure-requests` on an overlay request, and never let a
  clearnet HTTPS-only (port 443) config propagate to an overlay
- Log or display `127.0.0.1` as the client IP of a Tor request — use
  `tor:{circuit_id}`, or the literal `tor` when circuit-ID export is off
- Enable I2P by default — PART 31.2 is opt-in (`server.i2p.enabled`,
  default false): no provider, no port, no generated config unless enabled

## CRITICAL - ALWAYS DO
- Public Endpoint Safety Principle: anything reachable with no auth must be
  safe for anyone in the world to see — classify every field Tier 1/2/3
  before exposing it
- Tier 2 operational info (version, uptime, generic health) is fine to show
  unauthenticated
- Tor hidden service auto-enabled if Tor is found on the host (PART 31.1) —
  no toggle; I2P eepsite (PART 31.2) is opt-in and off by default
- Tor parity principle: a Tor request behaves exactly like the equivalent
  clearnet request, except that clearnet-identifying data is swapped/omitted
  and genuinely impossible operations fail loudly with a Tor-specific
  message — never silently, never by falling back to a clearnet URL
- Render user-facing timestamps in UTC for Tor requests (the server's local
  timezone leaks the operator's location)
- Advertise the hidden service with `Onion-Location` on clearnet HTML
  document responses only (never on the onion's own responses, API/JSON,
  static assets, or redirects)

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| DB driver | `modernc.org/sqlite` (no CGO) | PART 10 |
| Password hash (config/backup) | Argon2id | PART 11 |
| Token hash (API tokens) | SHA-256 | PART 11 |
| Public endpoint rule | 3-tier safe/unsafe classification | PART 11 |
| Tor | REQUIRED, auto-enabled when `tor` binary found | PART 31.1 |
| I2P | OPTIONAL, opt-in via `server.i2p.enabled` (default off) | PART 31.2 |
| Overlay protocol | always `http://`, no cert, no HSTS, no upgrade | PART 12, 31 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Tier 1 | never public, not even in debug |
| Tier 2 | always public, operational/safe |
| Tier 3 | public only per user privacy config |

## QUICK REFERENCE
- Before adding any new public field/endpoint, classify it against the
  Tier 1/2/3 table in AI.md PART 11 before writing the handler
- PART 11's HTTP layer is implemented: `src/httpserver/headers.go`
  (header matrix, CSP, Permissions-Policy, COOP/COEP/CORP, HSTS,
  Clear-Site-Data, Server-Timing), `privacy_signals.go` (DNT/GPC),
  `secfetch.go`, `reports.go` (`/api/{api_version}/server/reports/*`,
  always 204), `wellknown.go` (robots.txt, security.txt, llms.txt,
  pgp-key.asc — allowlist-only, GET/HEAD only, never redirect),
  `securitypages.go` (`/server/security[/policy|/thanks]`, `/server/dpo`),
  `securityreport.go` (`/server/contact?security_id=` mode switch,
  AES-256-GCM sealed at rest via `server.security.encryption_key`),
  `ipblock.go` (allowlist / temporary+permanent blocks / abuse detection,
  swept every minute by the `ip_block_release` scheduler task)
- PART 11's Submission Flow steps 4 and 5 (maintainer notification and
  researcher acknowledgment) are sent via the PART 17 notifier and are
  always "the CC path, never the primary channel" — a send failure never
  affects the submission or the tracking id
- PART 31 is implemented for both overlays:
  - `src/tor/` — dedicated Tor process via `github.com/cretz/bine`, v3
    hidden service, SafeLogging, dedicated loopback backend port,
    HAProxy PROXY-protocol circuit-ID ingest
    (`github.com/pires/go-proxyproto`), vanity search/apply, key import,
    health monitor; CLI in `src/tor_cli.go`
  - `src/i2p/` — OPT-IN (`server.i2p.enabled`, default false): Model A
    i2pd subprocess with a regenerated `{config_dir}/i2p/tunnels.conf`,
    Model B external SAMv3 bridge over a raw `net.Conn`, destination
    persisted at `{data_dir}/i2p/site/`, `.b32.i2p` derivation, provider
    resolution i2pd → SAM → warn-and-continue; CLI in `src/i2p_cli.go`
  - `src/httpserver/overlay.go` + `urlvars.go` — priority-0 overlay
    detection AHEAD of proxy headers, `tor:{circuit_id}` client identity
    (so `127.0.0.1` is never logged for a Tor request, and rate limits and
    blocklists key on the circuit), the I2P trusted-proxy-gate exception,
    `BuildURL` always `http://`, and `Onion-Location` on clearnet HTML
    2xx top-level navigations only
  - `src/config/overlay.go` (`server.tor.*`, `server.i2p.*` + validation),
    `src/scheduler/overlay.go` (`tor_health`, `i2p_health`),
    `src/overlay.go` + `src/main.go` (start/stop wiring, background
    bootstrap so Tor never delays the clearnet listener)
  - Deferred under PART 31 is environment-only: this container has no
    `tor`, no `i2pd`, and no SAM bridge, so live bootstrap, descriptor
    publication, a real circuit-ID PROXY header, and a real eepsite
    tunnel are unexercised — see `TODO.AI.md`
- Deferred under PART 11: GPG keypair management CLI, the maintainer
  email carrying inline AES armor instead of a PGP MIME attachment, and
  `/server/security/report/{tracking_id}` — see `TODO.AI.md`

---
For complete details, see AI.md PART 9, 10, 11, 31 (31.1 Tor, 31.2 I2P)
