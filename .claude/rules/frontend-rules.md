# Web Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Require JavaScript for any core feature — progressive enhancement only
- Use client-side rendering frameworks (React/Vue) — server-side Go
  templates only
- Ship a frontend that skips mobile/responsive support

## CRITICAL - ALWAYS DO
- Every feature works fully in-browser, not just via API
- Frontend validates the same rules as the backend (defense in depth)
- WCAG 2.1 AA accessibility compliance
- PWA support — installable, offline-capable
- `Access-Control-Allow-Origin: *` on API endpoints
- Mobile-first responsive CSS, touch-friendly targets, word-break on long
  strings

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Rendering | Server-side Go templates | PART 1, 16 |
| API relationship | API is source of truth; frontend consumes it | PART 16 |
| System-only endpoints | API-only, no frontend page | PART 14, 16 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Progressive enhancement | JS improves UX but nothing breaks without it |

## QUICK REFERENCE
- Vanity routing: `/{slug}` → `/{resource}/{slug}` → `/api/{api_version}/{resource}/{slug}`
- Nested sub-resource pattern: `/{slug}/{sub}` (e.g. `/{slug}/stats` for
  click analytics, per IDEA.md business logic)
- Implemented: `src/httpserver/frontend.go` (home + `/server/*` pages +
  consent/CCPA + HTML variants of healthz/stats), `src/httpserver/
  render.go` (buffered template execution), `src/httpserver/pagedata.go`
  (shared `PageData`), templates under `src/server/template/`, CSS/JS
  under `src/server/static/`. Deferred sub-items (PWA, sitemap.xml,
  favicon.ico, announcements-banner rendering, GeoIP on stats, contact-form
  email delivery, Swagger/GraphQL docs) are tracked in `TODO.AI.md`.

---
For complete details, see AI.md PART 16
