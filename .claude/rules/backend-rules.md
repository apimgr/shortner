# Backend Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never store passwords with bcrypt; use Argon2id where the spec requires password hashing.
- Never store API tokens in plaintext; store SHA-256 hashes and reveal plaintext only at creation/reveal flows specified by AI.md.
- Never add an external runtime dependency beyond available Tor and i2pd overlay daemons.
- Never expose overlay addresses through HTTPS redirects, HSTS, or insecure-request upgrades.
- Never add an external cron scheduler.

## CRITICAL - ALWAYS DO
- Validate inputs and use consistent error, cache, database, security, and audit patterns.
- Use transactional, additive migrations that preserve existing user data.
- Auto-enable Tor when available; compute I2P's default from the host/container context in PART 31.2.
- Protect credentials and tokens in logs and errors; retain security auditability.
- Keep the built-in scheduler responsible for periodic work.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| API token storage? | SHA-256 hash; never plaintext at rest. | PART 11 |
| Tor default? | Auto-enable when available. | PART 31.1 |
| I2P default? | Off on hosts/VMs; on by computed default in application containers. | PART 31.2 |

## QUICK REFERENCE
Database changes require safe migrations, tests, data-flow review, and matching operator documentation.

---
For complete details, see AI.md PART 9, 10, 11, 31
