# Features Rules (PART 17, 18, 19, 20, 21, 22)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never add premium tiers, license keys, paywalls, or feature gating.
- Never use external cron or duplicate the built-in scheduler.
- Never expose internal metrics outside their specified loopback/internal visibility.
- Never weaken backup encryption, restore validation, or update integrity checks.

## CRITICAL - ALWAYS DO
- Keep notifications, scheduler jobs, GeoIP, metrics, backups, and updates complete and configurable as specified.
- Use the built-in scheduler for periodic tasks and make task execution safe to retry.
- Encrypt backups using the specified password-based protection and verify before restore.
- Keep update downloads, checksums/signatures, and release metadata validated.
- Keep feature settings, localized UI, docs, and tests synchronized.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| External cron? | No, use the built-in scheduler. | PART 18 |
| Paid feature tiers? | No, all features are free and available. | PART 1 |

## QUICK REFERENCE
Feature changes cross config, storage, scheduler, security, i18n, pages, docs, and API surfaces as applicable.

---
For complete details, see AI.md PART 17, 18, 19, 20, 21, 22
