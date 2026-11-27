# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never use client-side rendering, SPA routing, or JavaScript for core functionality.
- Never put business logic in JavaScript or inline CSS/JavaScript.
- Never use placeholder, stub, or “coming soon” server pages.
- Never let long tokens, hashes, or addresses break mobile layouts.

## CRITICAL - ALWAYS DO
- Render pages with Go templates and progressively enhance forms and interactions.
- Use mobile-first responsive CSS and WCAG 2.1 AA accessible markup.
- Keep touch targets at least 44x44px and provide keyboard-visible focus.
- Source about/help content from IDEA.md and render accurate privacy, terms, contact, and status pages.
- Ensure all core forms and routes work without JavaScript.

## PAGE CONTENT SOURCING
| Page | Source |
|---|---|
| About/help | IDEA.md, real routes and examples |
| Privacy | Built-in privacy text plus documented deployment facts/config |
| Terms/contact | Configured page and contact settings with valid defaults |

## QUICK REFERENCE
Any page change requires SSR, no-JS, mobile, keyboard, contrast, and translation review.

---
For complete details, see AI.md PART 16
