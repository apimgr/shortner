# Backend Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Expose Tier 1 secrets publicly, ever (not even in debug mode): DB
  credentials, internal IPs/hostnames, tokens, private keys, other users'
  PII, filesystem paths, account-existence signals, exact rate-limit values
- Use bcrypt for config/backup passwords — Argon2id (API tokens: SHA-256)
- Use CGO database drivers — `modernc.org/sqlite` (pure Go) only

## CRITICAL - ALWAYS DO
- Public Endpoint Safety Principle: anything reachable with no auth must be
  safe for anyone in the world to see — classify every field Tier 1/2/3
  before exposing it
- Tier 2 operational info (version, uptime, generic health) is fine to show
  unauthenticated
- Tor hidden service auto-enabled if Tor is found on the host (PART 31)

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| DB driver | `modernc.org/sqlite` (no CGO) | PART 10 |
| Password hash (config/backup) | Argon2id | PART 11 |
| Token hash (API tokens) | SHA-256 | PART 11 |
| Public endpoint rule | 3-tier safe/unsafe classification | PART 11 |

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
For complete details, see AI.md PART 9, 10, 11, 31
