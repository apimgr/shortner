# API, Health & TLS Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Add sub-routes under `/server/healthz` — no `/server/healthz/db` etc.
- Keep legacy/removed endpoints — delete completely, no shims, no redirects,
  no deprecation period
- Restrict auth-token extraction to a single header — accept any header from
  PART 8's list, plus `?token=`, first-found-wins

## CRITICAL - ALWAYS DO
- `/server/healthz` (frontend), `/api/{api_version}/server/healthz` (API,
  JSON by default) and the `/api/healthz` alias all exist; the `/healthz`
  root alias only if explicitly enabled via config
- `Access-Control-Allow-Origin: *` for API endpoints
- Keep all healthz fields public-safe (Tier 1/2/3 rule from PART 11)
- Health response carries `features.tor.*` (PART 31.1) AND `features.i2p.*`
  (PART 31.2 — `enabled/running/status/hostname/provider`, provider `none`
  while disabled); `checks.tor`/`checks.i2p` are omitted unless enabled
- Plain-text health output uses the canonical flattened field order of
  PART 13 ("Plain Text"): project.* → status → version/go_version/build.* →
  uptime/mode/timestamp → features.* → checks.* → stats.*

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Health endpoint | `/server/healthz` + API mirror | PART 13 |
| Legacy endpoint policy | delete, never keep for compat | PART 14 |
| External-service compat endpoints | implement per named service spec | PART 14 |
| Auth token sources | any supported header + `?token=` | PART 8, 14 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Compatibility endpoint | mimics an external service's API shape |
| Legacy endpoint | this project's own removed/old route — always deleted |

## QUICK REFERENCE
- API version prefix: `/api/{api_version}/...`
- TLS/Let's Encrypt handling: PART 15 (not yet implemented — see TODO.AI.md)

---
For complete details, see AI.md PART 13, 14, 15
