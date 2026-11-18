## Project description

A self-hosted URL shortening service with REST, OpenAPI, and read-only GraphQL
interfaces, a server-rendered web interface, and companion CLI/TUI with optional
native GUI. Anyone can shorten a URL anonymously (rate-limited); created links
belong to a persistent anonymous owner whose primary token can be restored and
managed. Owners may create linked tokens for integrations. Every link has a full
click-analytics page, with anonymized IPs, so link owners can see traffic without
exposing visitor identity.

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

### Product scope & non-goals
- Self-hosted URL shortener: create short links (auto-generated code or custom slug),
  anonymous redirect resolution, per-link click analytics.
- Target users: individuals/small teams self-hosting a shortener; developers integrating
  via API + Bearer token; anyone wanting basic click analytics without a full account
  system.
- Non-goals: no user accounts, no passwords, no login sessions, no per-user/per-tenant custom
  domains (single fixed domain: `official_site` — there is no owner entity to scope a
  custom domain to), no team/multi-tenant features, no destination-URL content
  moderation beyond operator manual takedown (see "Security decisions & exceptions").

### Roles & permissions
- **Anonymous (public)**: may create a link (subject to write rate limits), resolve/
  redirect any link, and view any link's click-stats page — all without authentication.
- **Resource owner** (holds a primary or linked owner token): may update destination/
  expiration or delete any link belonging to that anonymous owner. The primary token
  also manages linked tokens and can restore browser access. There are no accounts or
  passwords; the token is the owner's credential.
- **Operator** (holds `server.token` from `server.yml`): global access — may moderate/
  delete any link and revoke an owner's primary token, which revokes all its tokens
  (`--maintenance token revoke <prefix>` / `token list`, PART 8/11).
- No other roles exist; no admin web UI (PART 16 public-nav rule — nav is app-focused
  only).

### Data model & sensitivity
- Link: id, short_code (or custom slug), destination_url, created_at, expires_at,
  click_count, owner_id. Tier 2/3 (PART 11) — destination_url and click_count are shown
  on the link's public stats page; owner_id identifies an anonymous owner, not a person.
- Click: id, link_id, timestamp, ip (anonymized — IPv4: last octet zeroed; IPv6: last 80
  bits zeroed — before any write; the raw IP is never persisted), user_agent, referrer,
  country/region (GeoIP, derived from the already-anonymized IP where possible).
- `owners` (PART 8): random owner id, created and last-seen timestamps. Owners with
  resources are never idle-purged.
- `api_tokens` (PART 8): owner id, primary/linked kind, optional linked-token name,
  SHA-256 token hash, masked prefix, encrypted copy for explicit reveal, creation,
  rotation, expiry, last-used, and revocation timestamps. Raw tokens never appear in
  logs or list responses.
- No Tier 1 data is ever collected: no passwords, no accounts, no raw visitor IPs after
  anonymization.

### Trust boundaries & external services
- GeoIP database (PART 19): local/offline lookup only, used to derive approximate click
  location from the already-anonymized IP — not a live third-party API call per click.
- No identity provider, no OAuth, no payment processor, no webhook integrations.
- User-supplied `destination_url` is untrusted input: validated for well-formed URL
  syntax only. No SSRF exposure server-side, since redirects are 302 responses sent to
  the client's browser — the server never fetches, renders, or proxies the destination.

### Threat model & abuse cases
- Primary assets: integrity of the link → destination-URL mapping, visitor privacy in
  click-analytics data (via IP anonymization), and resource owner tokens (link
  management authority).
- Untrusted inputs: `destination_url`, custom slug, and the `User-Agent`/`Referer`
  headers on both link creation and click events.
- Attacker/abuser goals: mass-create links for spam/phishing redirect abuse, brute-force
  or guess a resource owner token to hijack a link, scrape/enumerate short codes to
  discover all live links, deanonymize a visitor from click data.
- Abuse cases & defenses:
  - Spam/phishing redirect abuse → write-rate-limited anonymous creation (PART 9/11);
    no proactive destination-URL blocklist/scanning is implemented (see below).
  - Resource owner token brute-force/hijack → `tok_` + 32 random base62 chars keyspace,
    SHA-256 hashed at rest, constant-time compare (PART 11).
  - Short-code enumeration/scraping → 62^6 random keyspace for auto-generated codes makes
    guessing infeasible; a public, paginated list-all-links endpoint/page does exist
    (`/list`, `GET /api/{api_version}/links` — see "Security decisions & exceptions"),
    which is a faster enumeration path than guessing but an accepted trade-off, not an
    oversight, since every link is public data by design (no accounts to scope
    visibility to).
  - Visitor deanonymization via click data → IP anonymized before any write, the public
    stats page exposes only Tier 2/3 fields (never a raw visitor IP), and known bot/
    crawler user agents are excluded from click counts.

### Security decisions & exceptions
- Anonymous, unauthenticated link creation is an intentional design choice (no account
  system, per "Product scope & non-goals"); the only abuse mitigation is rate limiting
  plus operator-initiated takedown via `server.token` — there is no automated
  destination-URL content/malware scanning. This is a deliberate scope exception, not an
  oversight.
- Resource owner tokens are bearer credentials with no recovery path if lost —
  intentional, since there are no accounts to recover through; the operator can still
  delete a link via `server.token` if it needs removing without its owner token.
- Single fixed domain, no per-tenant custom domains — there is no user/owner entity to
  scope a custom domain to, so custom domains are out of scope entirely, not merely
  deferred.
- A public, paginated listing of every created link (`/list` page, mirrored by
  `GET /api/{api_version}/links`) is intentional, not an oversight: since there are no
  accounts, every link is already public data (its stats page is reachable by anyone who
  has or guesses the short code), so aggregating that same public data into a list adds
  discoverability but no new information disclosure. No rate limit beyond the standard
  read-rate limits (PART 9) is applied to list requests specifically.

**Features:**
- **URL shortening**: generate a short code (auto) or a custom slug on create
- **Redirect**: resolving a short link 302-redirects to the destination URL
- **Analytics**: per-link click statistics page — total clicks, referrers, time series,
  approximate location (GeoIP), all with the visitor's IP anonymized before storage
- **Management**: update destination, set/clear expiration, delete — via resource owner
  token (issued at creation) or the operator's `server.token`

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
- List all created links, paginated (public, anonymous) — `/list` page and
  `GET /api/{api_version}/links`

### API routes

These project-defined link routes extend the template API. All JSON mutation
responses use the standard `{ok, data}` / RFC 7807 error shapes; list returns
`{data, pagination}`. There are no compatibility aliases under `/api/links`.

| Method | Path | Auth | Purpose | Request / response summary |
|--------|------|------|---------|-----------------------------|
| GET | `/api/{api_version}/links` | None | List links | Optional `page` and `limit`; returns paginated public link summaries |
| POST | `/api/{api_version}/links` | None | Create link | JSON `url`, optional `slug`, optional RFC3339 `expires_at`; returns link and one-time `owner_token` |
| GET | `/api/{api_version}/links/{slug}` | None | Read link | Returns public link summary |
| PATCH | `/api/{api_version}/links/{slug}` | Owner or operator Bearer token | Update link | JSON destination and/or expiration changes; returns updated link |
| DELETE | `/api/{api_version}/links/{slug}` | Owner or operator Bearer token | Delete link | Returns successful empty action result |
| GET | `/api/{api_version}/links/{slug}/stats` | None | Read analytics API | Returns click totals, time series, referrers, and approximate locations |
| GET | `/{slug}` | None | Native short-link redirect | Compatibility route for browsers and clients; returns 302 to the destination, or 404/410 |
| GET | `/{slug}/stats` | None | Native analytics page/API | HTML for browsers, JSON or text for API clients; returns stats or 404 |

**Data sources:**
- Database for links and clicks — see PART 10
- GeoIP for approximate click location — see PART 19

### Frontend design reference

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
