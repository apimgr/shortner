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
- Enable I2P by default — PART 31.2 is opt-in (`features.i2p.enabled`,
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
| I2P | OPTIONAL, opt-in via `features.i2p.enabled` (default off) | PART 31.2 |
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

---
For complete details, see AI.md PART 9, 10, 11, 31 (31.1 Tor, 31.2 I2P)
