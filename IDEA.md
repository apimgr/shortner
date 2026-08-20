## Project description

A self-hosted URL shortening service with an API and web interface. Anyone can shorten a
URL anonymously (rate-limited); each created link gets a resource owner token for later
management. Every link has a full click-analytics page, with anonymized IPs, so link
owners can see traffic without exposing visitor identity.

## Project variables

project_name:    shortner
project_org:     apimgr
# FROZEN — equals project_name on first install, never changes
internal_name:   shortner
app_name:        Shortner
official_site:   https://shortner.example.com
maintainer_name: casjay
maintainer_email: casjay@yahoo.com
owner_token:     shortner_owner_token_Lu2YwQRQ

## Business logic

**Target users:**
- Individuals and small teams wanting a self-hosted URL shortener
- Developers integrating link shortening via API + Bearer token
- Anyone sharing links who wants basic click analytics without a full account system

**Features:**
- **URL shortening**: generate a short code (auto) or a custom slug on create
- **Redirect**: resolving a short link 302-redirects to the destination URL
- **Analytics**: per-link click statistics page — total clicks, referrers, time series,
  approximate location (GeoIP), all with the visitor's IP anonymized before storage
- **Management**: update destination, set/clear expiration, delete — via resource owner
  token (issued at creation) or the operator's `server.token`
- **API tokens**: every created link is a "resource" per PART 11's API Token Model —
  no user accounts, no passwords, no sessions. Anonymous POST creates a link (subject to
  write rate limits) and returns a one-time `owner_token`; that token (Bearer header or
  the `owner_token` cookie for the web UI) authorizes later edits/deletes on that link
  only. The operator's global `server.token` can moderate/delete any link.

**Data models:**
- Link: id, short_code (or custom slug), destination_url, created_at, expires_at,
  click_count
- Click: id, link_id, timestamp, ip (anonymized — last octet/segment zeroed before
  storage), user_agent, referrer, country/region (GeoIP, derived from anonymized IP
  where possible)

**Business rules:**
- Short codes: 6-char alphanumeric, auto-generated (62^6 keyspace)
- Custom slugs: 3–20 chars, alphanumeric + hyphens, checked against the reserved-names
  list (PART 16 → "Reserved Names") before registration
- Expired links resolve with 410 Gone instead of redirecting
- Click tracking excludes known bot/crawler user agents
- IP addresses are anonymized (IPv4: zero last octet; IPv6: zero last 80 bits) before
  any write to the Click table — the raw IP is never persisted
- Single fixed domain (`official_site`) — no per-user custom domains; the framework has
  no user accounts, so there is no owner to scope a custom domain to (PART 11)

**Endpoints (WHAT, not paths — see PART 14):**
- Create short link with auto-generated or custom code (anonymous, rate-limited) →
  returns the link plus a one-time owner token
- Resolve/redirect short link (public, anonymous)
- Get link click statistics — totals, time series, referrers, approximate locations
  (public GET; per the `/{slug}/{sub}` vanity convention in PART 16, exposed as the
  link's `stats` sub-resource, e.g. short link `/{slug}` pairs with its stats page at
  `/{slug}/stats`)
- Update destination URL, expiration (owner token or operator token required)
- Delete link (owner token or operator token required)

**Data sources:**
- Database for links and clicks — see PART 10
- GeoIP for approximate click location — see PART 19

## Frontend design reference

The prior implementation of this project was a Node.js app
(`github.com/casjaydns/csj.lol`). The new Go frontend's home page (`/`) body
must match that app's LAYOUT ONLY: centered logo, single-column create-link
card (URL input, optional custom-slug input, submit), success state showing
the resulting short URL with a "create another" action. Nav (Home/List/
Domains/About) and footer (attribution/links) follow the same structure,
adapted to this project's actual routes and reserved names.

Theming does NOT come from the reference app — use AI.md PART 16's own CSS
Variable Reference and `theme-dark`/`theme-light`/`theme-auto` system
(`--color-bg`, `--color-text`, `--color-primary`, etc. in `common.css`,
toggled via the `theme` cookie), not reference-app-specific custom
properties or hardcoded hex. AI.md's default dark palette already derives
from the same Dracula colors the reference app used, so visually the
result should be close — but the CSS variables and theme-toggle mechanism
must be AI.md's own, not copied from the reference app.

The layout parity described above has been implemented and verified against
the reference app's markup (`reference/csjlol/`, since removed — see
TODO.AI.md "PART 16" for the comparison record).

**Adapt, do not copy verbatim** — the reference app used Vue 2 loaded from
`unpkg.com` CDN and Tailwind-generated `styles.css`. This project is a
self-contained single binary with embedded static assets (PART 7) and MUST
work with JavaScript disabled (PART 16 "No JavaScript-Disabled Broken State"):
the create-link form must be a real HTML `<form method="POST">` that works via
server-rendered response, with any Vue/AJAX behavior layered on top as
progressive enhancement only — no CDN script tags, no JS-only form.
