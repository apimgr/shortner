# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never retain project-owned legacy endpoints, aliases, redirects, or deprecation shims when AI.md requires removal.
- Never advertise internal-only metrics or control endpoints in OpenAPI, discovery, or public feature metadata.
- Never let route handlers diverge from response envelopes, content negotiation, authentication, or status rules in AI.md.
- Never redirect overlay HTTP requests to HTTPS or apply HSTS to them.

## CRITICAL - ALWAYS DO
- Keep health/version output and its exceptional schema exactly aligned with PART 13.
- Register every public route in the route contract, OpenAPI, tests, and docs; exclude internal routes.
- Validate requests and return the exact error/status formats required by PART 14.
- Keep REST, GraphQL, Swagger UI/spec, and documented API surfaces synchronized.
- Apply SSL/TLS and certificate behavior from PART 15 without weakening overlay rules.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Where are versioned routes? | `/api/{api_version}/...` with only specified aliases. | PART 14 |
| Is `/server/metrics` public API metadata? | No; it is internal. | PART 20 |

## QUICK REFERENCE
Generate embedded OpenAPI after schema/annotation changes; update route coverage and prose docs with every API change.

---
For complete details, see AI.md PART 13, 14, 15
