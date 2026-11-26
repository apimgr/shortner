# API Reference

Full URL examples use the official service at `https://csj.lol`; replace
that origin with your deployment URL when self-hosting.

The REST API is the primary interface; the web interface uses the same
server-side operations. CORS follows
`server.cors.allowed_origins`: the default `*` permits public origins, while
an explicit origin list is echoed per request and may be paired with
credentials. Requests without an `Origin` header do not receive CORS
headers.

The version prefix is `https://csj.lol/api/{api_version}`, which is
`https://csj.lol/api/v1` by default and is configurable via
`server.api_version`.

Guest preferences are cookie-only and need no account. The web page at
`/server/preferences` saves appearance, language, consent, and CCPA choices.
`/server/preferences/sync` displays a shareable URL and short code and accepts
an import by normal form POST; `/server/preferences/sync/import` applies a
shared link. The API mirrors are `GET https://csj.lol/api/{api_version}/server/preferences`
and `POST https://csj.lol/api/{api_version}/server/preferences` for snapshots and partial
updates, plus `GET https://csj.lol/api/{api_version}/server/preferences/sync` for export and
`GET https://csj.lol/api/{api_version}/server/preferences/sync/import` for import. Sync data
contains only theme, language, and documented application preference
cookies; consent, build stamps, and credentials are excluded. Imports are
additive and never clear settings that are absent from the link.

Interactive API pages are available at `https://csj.lol/server/docs/swagger` and
`https://csj.lol/server/docs/graphql`. OpenAPI JSON is served directly from
`https://csj.lol/api/{api_version}/server/swagger` (also `https://csj.lol/api/swagger`). The read-only
GraphQL endpoint accepts POST requests at `https://csj.lol/api/{api_version}/server/graphql`
(also `https://csj.lol/api/graphql`).

The Streamable HTTP MCP endpoint is available at
`https://csj.lol/api/{api_version}/server/mcp`, `https://csj.lol/api/mcp`, and `https://csj.lol/mcp`. It reuses API Bearer
tokens (required on every request) and exposes permitted OpenAPI operations
as tools through the same API middleware and ownership checks. It implements
protocol revision `2026-07-28`; OAuth is not implemented. See
[`/server/help/mcp`](https://csj.lol/server/help/mcp) for client
setup and the live public tool list.

GraphQL exposes `link(slug: String!)`, paginated `links(limit, offset)`, and
`linkStats(slug: String!)`, plus query fields for the documented read-only
REST operations. REST-backed fields use the operation ID from the OpenAPI
spec and return the REST response as a JSON value. For example:

```graphql
{
  getHealth(api_version: "v1")
}
```

The schema has no mutation fields. The preference-sync import GET route is
excluded because it changes cookies. GraphQL requests are limited to 100
field and fragment-spread nodes; use REST for create, update, and delete
operations.

## OpenAPI response contracts

The generated OpenAPI document includes concrete request and response
schemas for link creation, listing, lookup, updates, and statistics; health
checks; CLI autodiscovery; preferences; token management; and GraphQL
requests. Link creation returns HTTP `201`; link updates return `200`; and
security-report intake returns `204`. The list-links operation documents
its `page` and `limit` query parameters. Metrics responses are documented
as plain text, and CLI binary downloads as octet streams. Successful
responses that use the API envelope are described with their operation
specific `data` shape. Report endpoints document both legacy
`application/csp-report` and modern `application/reports+json` payloads,
including their object and array forms.

The route annotations cover the API, CLI binary, report, link, and metrics
routes mounted by the server and are the source used by both the OpenAPI
generator and the GraphQL read-field generator. `go generate ./src/swagger`
regenerates both documents from the route annotations and executable
GraphQL schema. The build embeds those generated files; freshness tests fail
when an embedded document no longer matches its source. The OpenAPI endpoint
serves the embedded JSON with the request's resolved server URL, and the
GraphQL page renders the embedded introspection document and query inventory.
Some flexible payloads and failure responses remain generic, and several
operations can use more useful response examples.
Swagger lists every annotated operation and provides browser controls to
inspect parameters, send requests, and view responses; JavaScript enhances
the page while the server-rendered inventory and JSON link remain available
without it. The GraphQL page lists fields from the generated schema and
accepts query, operation name, and JSON variables in either its enhanced
form or a standard form submission. The versioned GraphQL endpoint also
accepts GET requests with `query` and optional JSON `variables` and
`operationName` parameters; omitting `query` returns a short usage message
for the no-JavaScript endpoint link. The enhanced form can send an optional
API token in the `Authorization` header; tokens are not persisted by the
page. Inspect the live generated document for the operations currently
available on a server.

## Authentication

Most endpoints need no credentials. Editing or deleting a link requires a
valid primary or linked `own_` token for its owner, or the `adm_` operator
token from `secrets.yml`. A newly created anonymous owner receives a
primary `owner_token`; later resources created with that token belong to
the same owner and do not return another token. Metrics credentials use
the `mtr_` prefix.

A token may be supplied through any of the following, checked in order,
first found wins:

1. `Authorization: Bearer {token}`
2. `Authorization: {token}` (raw, without the `Bearer` prefix)
3. `X-API-Key: {token}`
4. `API-Key: {token}`
5. `X-Auth-Token: {token}`
6. `X-Access-Token: {token}`
7. `X-Token: {token}`
8. `Token: {token}`
9. `?token={token}` in the query string

Token management is available in the browser at `/server/preferences/tokens`.
When no owner token is present, `POST https://csj.lol/server/preferences/token/create`
issues a primary token and redirects back to the page; restore accepts a
primary token through `POST https://csj.lol/server/preferences/token/restore`. The page
supports reveal, rotate, linked-token create/update/revoke, and primary revoke.
Primary revoke deletes the owner, its linked tokens, and its resources.

The API mirrors primary-token issue and revoke at
`POST https://csj.lol/api/{api_version}/server/preferences/tokens/primary` and
`DELETE https://csj.lol/api/{api_version}/server/preferences/tokens/primary`. Issuing is
public and returns the raw `owner_token` once in the JSON response. Revoking
requires the primary token and returns the counts of deleted tokens and
resources. Neither route accepts cookies. Linked-token actions remain under
`https://csj.lol/api/{api_version}/server/preferences/tokens` and require the primary token.

Tokens use a prefix plus 32 base62 characters and are looked up by SHA-256
hash. An encrypted owner-token copy is retained for the owner's explicit
reveal action. Raw tokens are never included in list
responses or logs. API routes accept credentials from headers only; owner
cookies are used only by web forms.

## Content Negotiation

Every endpoint can answer in JSON, plain text, or (for browser-facing
routes) HTML. The format is chosen in this order:

1. **API `.txt` suffix** forces plain text. There is no `.json` API suffix;
   JSON is the default.
2. **`Accept` header** — explicit `application/json` selects JSON,
   `text/html` selects the browser representation where one exists, and
   `text/plain` selects text.
3. **User-Agent** — non-interactive HTTP tools (`curl`, `wget`, `httpie`,
   common HTTP libraries) and an empty User-Agent get text. The interactive
   `shortner-cli` gets JSON; browser clients get JSON from API routes and
   HTML from frontend routes. Text browsers receive server-rendered HTML.

So `curl` gets readable text without flags, while an ordinary browser gets
HTML from frontend routes and JSON from API routes.

## Response Envelope

Successful API responses wrap their payload:

```json
{
  "ok": true,
  "data": { }
}
```

Errors use the same flat envelope:

```json
{
  "ok": false,
  "error": "VALIDATION_FAILED",
  "message": "Human-readable message"
}
```

Health endpoints return their object directly, without the envelope. The
link list endpoint returns a `data` array alongside a `pagination` object.
Text API responses use `OK: {message}` / `ERROR: {code}: {message}` and end
with one newline; health text keeps its dedicated flat `key: value` format.

## Links

### Create a link

```
POST https://csj.lol/api/v1/links
```

```json
{
  "url": "https://example.com/a/very/long/path",
  "slug": "my-link",
  "expires_at": "2026-02-15T10:30:00Z"
}
```

| Field | Required | Notes |
|-------|:--------:|-------|
| `url` | yes | The destination. Validated server-side. |
| `slug` | no | Custom short code, 3–20 characters, alphanumeric and hyphens. Checked against a reserved-name list. |
| `expires_at` | no | RFC 3339 timestamp. Omit for a link that never expires. |

Without a `slug`, a random 6-character alphanumeric code is generated —
roughly 56 billion possibilities.

Response:

```json
{
  "ok": true,
  "data": {
    "short_code": "abc123",
    "short_url": "https://example.com/abc123",
    "destination_url": "https://example.com/a/very/long/path",
    "created_at": "2026-01-15T10:30:00Z",
    "expires_at": null,
    "click_count": 0,
    "owner_token": "own_..."
  }
}
```

`owner_token` is present only when the request created a new anonymous
owner. Save it to restore that owner's browser identity. It remains
viewable, masked by default, on `/server/preferences/tokens`.

### Get a link

```
GET https://csj.lol/api/v1/links/{slug}
```

Returns the same object without `owner_token`. No authentication.

### List all links

```
GET https://csj.lol/api/v1/links?page=1&limit=250
```

```json
{
  "data": [
    {
      "short_code": "abc123",
      "short_url": "https://example.com/abc123",
      "destination_url": "https://example.com/a/very/long/path",
      "created_at": "2026-01-15T10:30:00Z",
      "expires_at": null,
      "click_count": 42
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 250,
    "total": 500,
    "pages": 2
  }
}
```

!!! warning
    This endpoint is public and unauthenticated, and it lists every link on
    the server including its destination. That is a deliberate design
    decision for this project — an accepted trade-off, not an oversight.
    Do not create links to sensitive URLs on a public instance, and put the
    instance behind access control if that listing is unacceptable for
    your deployment.

### Update a link

```
PATCH https://csj.lol/api/v1/links/{slug}
```

Requires a valid token belonging to the link's owner or the operator token.

```json
{
  "url": "https://example.com/new/destination",
  "expires_at": "2026-06-01T00:00:00Z"
}
```

Both fields are optional; send only what changes.

```json
{
  "ok": true,
  "id": "abc123",
  "message": "Link updated successfully",
  "link": { }
}
```

### Delete a link

```
DELETE https://csj.lol/api/v1/links/{slug}
```

Requires a valid token belonging to the link's owner or the operator token.

```json
{
  "ok": true,
  "id": "abc123",
  "message": "Link deleted successfully"
}
```

### Click analytics

```
GET https://csj.lol/api/v1/links/{slug}/stats
```

No authentication.

```json
{
  "ok": true,
  "data": {
    "short_code": "abc123",
    "total_clicks": 42,
    "referrers": {
      "(direct)": 20,
      "https://example.com": 15
    },
    "time_series": [
      { "date": "2026-01-15", "count": 10 },
      { "date": "2026-01-16", "count": 15 }
    ],
    "recent": [
      {
        "timestamp": "2026-01-16T14:30:00Z",
        "ip": "192.0.2.0",
        "referrer": "https://example.com",
        "country": "US",
        "region": "CA"
      }
    ]
  }
}
```

`recent` holds the most recent clicks, newest first. The `ip` field is
**already anonymized** — the last IPv4 octet, or the last 80 bits of an
IPv6 address, is zeroed before the record is written, so the full address
was never stored. `country` and `region` come from GeoIP and are omitted
when GeoIP is disabled or the address is not found.

Clicks from recognized bot user agents are not counted at all.

## Redirect Behavior

```
GET https://csj.lol/{slug}
```

| Situation | Response |
|-----------|----------|
| Link exists and is live | `302 Found` to the destination, click recorded |
| Link existed but has expired | `410 Gone` |
| Link never existed | `404 Not Found` |

The distinction between `410` and `404` is deliberate — an expired link is
reported as having existed.

## Health

```
GET https://csj.lol/server/healthz
GET https://csj.lol/api/healthz
GET https://csj.lol/api/v1/server/healthz
```

All three are the same handler, never a redirect. A root `/healthz` alias
exists only when `server.healthz.root.enabled` is on. There are no
sub-routes under `healthz`.

Every field is safe for anyone to see. Response:

```json
{
  "project": {
    "name": "Shortner",
    "tagline": "...",
    "description": "..."
  },
  "status": "healthy",
  "version": "1.0.0",
  "go_version": "go1.26.0",
  "build": {
    "commit": "abc1234",
    "date": "2026-01-15T00:00:00Z"
  },
  "uptime": "2d 5h 30m",
  "mode": "production",
  "timestamp": "2026-01-15T10:30:00Z",
  "features": {
    "tor": { "enabled": false, "running": false, "status": "", "hostname": "" },
    "i2p": { "enabled": false, "running": false, "status": "", "hostname": "", "provider": "none" },
    "geoip": true
  },
  "checks": {
    "database": "ok",
    "cache": "ok",
    "disk": "ok",
    "scheduler": "ok"
  },
  "stats": {
    "requests_total": 10000,
    "requests_24h": 250,
    "active_connections": 3
  }
}
```

`checks.tor` and `checks.i2p` are omitted unless those features are enabled.
Tor starts when a Tor binary is available. I2P is opt-in on hosts and enabled
by default in containers; its status reports startup, health, or provider
unavailability.

The plain-text form flattens the same fields, one `key: value` per line,
in a fixed order: `project.*`, `status`, version and build, uptime, mode,
timestamp, `features.*`, `checks.*`, `stats.*`.

The process exit status of `shortner --status` mirrors this: `0` when
running, `1` when not.

## Metrics

```
GET https://csj.lol/server/metrics
GET https://csj.lol/server/metrics/prometheus
GET https://csj.lol/server/metrics/grafana
GET https://csj.lol/server/metrics/loki
```

Mirrored at `https://csj.lol/api/metrics[/{service}]`,
`https://csj.lol/api/v1/server/metrics[/{service}]`, and — when
`server.metrics.root.enabled` is on — bare `https://csj.lol/metrics`. All aliases are the
same handler; none is a redirect.

Each service requires its own bearer token, supplied only in the
`Authorization` header. The query-string fallback is deliberately not
accepted here. Comparison is constant-time.

| Condition | Response |
|-----------|----------|
| No token configured for the service | `403` |
| Token supplied but wrong | `401` |
| Correct token | `200` with the metrics |

See [Integrations](integrations.md#metrics).

## Browser Reporting

```
POST https://csj.lol/api/v1/server/reports/csp
POST https://csj.lol/api/v1/server/reports/nel
POST https://csj.lol/api/v1/server/reports/default
```

Receives Content Security Policy violation reports, Network Error Logging
reports, and the default reporting group. These are unauthenticated by
necessity — the browser sends them.

**They always return `204 No Content` with an empty body.** Nothing the
caller sent is ever echoed back, so the endpoints cannot be used as an
open reflector.

## Web Interface Routes

Server-rendered HTML. Every one of these works with JavaScript disabled.

| Route | Purpose |
|-------|---------|
| `GET https://csj.lol/` | Home page with the link-creation form |
| `POST https://csj.lol/` | Create a link from the form |
| `GET https://csj.lol/{slug}` | Redirect to the destination |
| `GET https://csj.lol/{slug}/stats` | Click analytics page |
| `GET https://csj.lol/list` | Public paginated listing of all links |
| `GET https://csj.lol/server` | Redirects to `/server/about` |
| `GET https://csj.lol/server/about` | About page |
| `GET https://csj.lol/server/help` | Help page |
| `GET https://csj.lol/server/help/mcp` | MCP connection guide |
| `GET https://csj.lol/server/privacy` | Privacy policy |
| `GET https://csj.lol/server/terms` | Terms of service |
| `GET https://csj.lol/server/contact` | Private noindex report-type chooser; the security pointer and abuse-report guidance appear below it |
| `GET https://csj.lol/server/contact?type={type}` | One noindex form containing only the selected type's fields |
| `POST https://csj.lol/server/contact` | Submit the selected report type, then confirm it in the server-rendered review step |
| `GET https://csj.lol/server/security` | Security and vulnerability reporting page |
| `GET https://csj.lol/server/security/policy` | Coordinated disclosure policy |
| `GET https://csj.lol/server/security/thanks` | Researcher acknowledgments |
| `GET https://csj.lol/server/healthz` | Health page |
| `GET https://csj.lol/server/preferences` | Cookie-only interface preferences |
| `GET https://csj.lol/server/preferences/tokens` | Owner-token management page |
| `GET https://csj.lol/api/{api_version}/server/preferences/tokens` | Masked token list (primary Bearer token required) |
| `POST https://csj.lol/api/{api_version}/server/preferences/tokens` | Create a linked token |
| `POST https://csj.lol/api/{api_version}/server/preferences/tokens/primary/reveal` | Reveal primary token |
| `POST https://csj.lol/api/{api_version}/server/preferences/tokens/primary/rotate` | Rotate primary token |
| `POST https://csj.lol/api/{api_version}/server/preferences/tokens/{id}/reveal` | Reveal linked token |
| `POST https://csj.lol/api/{api_version}/server/preferences/tokens/{id}/rotate` | Rotate linked token |
| `PATCH https://csj.lol/api/{api_version}/server/preferences/tokens/{id}` | Rename linked token or change expiry |
| `DELETE https://csj.lol/api/{api_version}/server/preferences/tokens/{id}` | Revoke linked token |
| `GET https://csj.lol/api/{api_version}/server/security/policy` | Coordinated disclosure policy data |
| `GET https://csj.lol/api/{api_version}/server/dpo` | Public DPO contact data |
| `GET https://csj.lol/api/{api_version}/server/contact` | Type-specific report field descriptors, offered types, CAPTCHA descriptor, form token, security URL, and abuse email |
| `POST https://csj.lol/api/{api_version}/server/contact` | Submit a type-specific report for review; repeat its fields with the review token and confirmation to deliver |
| `GET https://csj.lol/api/{api_version}/server/auth/check` | Validate a Bearer token and return its kind |
| `GET https://csj.lol/api/{api_version}/server/help` | Help content and registry-backed list of all registered help pages |
| `GET https://csj.lol/api/{api_version}/server/help/mcp` | MCP help sections and resolved endpoint |
| `GET https://csj.lol/api/{api_version}/server/about` | About content |
| `GET https://csj.lol/api/{api_version}/server/privacy` | Privacy content |
| `GET https://csj.lol/api/{api_version}/server/terms` | Terms content |
| `GET https://csj.lol/api/{api_version}/server/security` | Security overview |
| `GET https://csj.lol/api/{api_version}/server/security/thanks` | Security acknowledgments |
| `GET https://csj.lol/api/{api_version}/server/security/policy` | Disclosure policy |
| `GET https://csj.lol/server/security/report/{tracking_id}` | Researcher report status (requires the one-time status token) |
| `POST https://csj.lol/server/consent` | Cookie consent choice |
| `POST https://csj.lol/server/theme` | Theme preference |
| `POST https://csj.lol/announcements/dismiss` | Dismiss a configured announcement without JavaScript |
| `GET https://csj.lol/static/*` | Embedded CSS, JS, and images |
| `GET https://csj.lol/manifest.json` | PWA manifest |
| `GET https://csj.lol/sw.js` | Build-stamped service worker |
| `GET https://csj.lol/favicon.ico` | Embedded default favicon (the page head may use a configured HTTPS or same-origin branding icon) |
| `GET https://csj.lol/sitemap.xml` | Public-page sitemap or sitemap index |
| `GET https://csj.lol/sitemap-{part}.xml` | A numbered sitemap page when the URL limit is exceeded |
| `GET https://csj.lol/feed.xml` | Atom 1.0 feed of recent public links |
| `GET https://csj.lol/rss.xml` | RSS 2.0 feed of the same links |
| `GET https://csj.lol/api/autodiscover` | Server and available CLI update metadata |
| `GET https://csj.lol/cli/binaries/{project}-cli-{os}-{arch}` | Download a verified CLI build from `{data_dir}/cli/` (public unless `cli.binary_download.require_auth` is enabled) |

Two routes are conditional: `/server/ccpa` exists only when
`server.privacy.data.sold` is true, and `/server/dpo` only when compliance
settings or a configured DPO name require it.

A trailing slash on any path other than the root redirects once to the
canonical path with `301`.

## Rate Limiting

Limits are per client IP, tracked in a sliding window with separate
counters per class:

| Class | Methods | Default |
|-------|---------|---------|
| Read | `GET`, `HEAD` | 120 per 60 seconds |
| Write | `POST`, `PUT`, `PATCH`, `DELETE` | 10 per 60 seconds |
| Health | health endpoints | 120 per 60 seconds |
| Global burst | all requests | 240 per minute |

When a limit is exceeded the server responds `429 Too Many Requests` with
a `Retry-After` header giving whole seconds until the window reopens (at
least 1).

The client IP is resolved through the trusted proxy chain, so put your
reverse proxy in `server.trusted_proxies.additional` or every request will
be attributed to the proxy.

`X-RateLimit-*` response headers are not currently emitted on successful
requests.

Rate limiting is always enabled. There is no configuration switch to turn
it off. See [Configuration](configuration.md#rate-limiting).

## Errors

| Status | Meaning |
|--------|---------|
| `400` | Malformed request or invalid JSON |
| `403` | Missing or wrong token for a protected operation |
| `404` | No such link, or a well-known path that is not enabled |
| `405` | Method not allowed on this route |
| `410` | The link existed and has expired |
| `422` | Validation failed |
| `429` | Rate limit exceeded |
| `500` | Internal error |

Error messages never reveal filesystem paths, internal hostnames, database
details, or token values, in any mode — including debug.
