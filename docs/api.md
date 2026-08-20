# API Reference

The REST API is the source of truth for the application; the web interface
consumes the same logic. Every API endpoint sends
`Access-Control-Allow-Origin: *`.

The version prefix is `/api/{api_version}`, which is `/api/v1` by default
and is configurable via `server.api_version`.

!!! note
    There is no Swagger UI, no OpenAPI document, and no GraphQL endpoint.
    This page is the API reference.

## Authentication

Most endpoints need no credentials. Editing or deleting a link requires
either that link's `owner_token` — returned once, when the link is
created — or the operator token from `server.yml`.

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

Tokens are stored as SHA-256 hashes; the raw value is never written to the
database or to a log. A lost `owner_token` cannot be recovered — there is
no account it belongs to.

## Content Negotiation

Every endpoint can answer in JSON, plain text, or (for browser-facing
routes) HTML. The format is chosen in this order:

1. **Path suffix** — a `.json` suffix forces JSON, a `.txt` suffix forces
   plain text.
2. **`Accept` header** — `application/json`, `text/plain`, or `text/html`.
3. **User-Agent** — recognized command-line HTTP clients (`curl`, `wget`,
   `httpie`, `python-requests`, `go-http-client`) and an empty User-Agent
   get plain text. Anything else gets HTML on frontend routes and JSON on
   API routes.

So `curl` gets readable text without any flags, and a browser gets a page,
from the same URL.

## Response Envelope

Successful API responses wrap their payload:

```json
{
  "ok": true,
  "data": { }
}
```

Errors use a matching shape:

```json
{
  "ok": false,
  "error": {
    "code": "validation_failed",
    "message": "Human-readable message",
    "details": {
      "field": "url",
      "rule": "format"
    }
  }
}
```

Two documented exceptions: the health endpoints return their object
directly, without the envelope, and the link list endpoint returns a
`data` array alongside a `pagination` object.

## Links

### Create a link

```
POST /api/v1/links
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
    "owner_token": "tok_..."
  }
}
```

**`owner_token` is returned exactly once, here.** Save it. It is the only
way to edit or delete this link later, and there is no recovery path.

### Get a link

```
GET /api/v1/links/{slug}
```

Returns the same object without `owner_token`. No authentication.

### List all links

```
GET /api/v1/links?page=1&limit=250
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
PATCH /api/v1/links/{slug}
```

Requires the link's `owner_token` or the operator token.

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
DELETE /api/v1/links/{slug}
```

Requires the link's `owner_token` or the operator token.

```json
{
  "ok": true,
  "id": "abc123",
  "message": "Link deleted successfully"
}
```

### Click analytics

```
GET /api/v1/links/{slug}/stats
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
GET /{slug}
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
GET /server/healthz
GET /api/healthz
GET /api/v1/server/healthz
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

`checks.tor` and `checks.i2p` are omitted unless those features are
enabled. Since neither overlay network is implemented in this build, they
always report disabled with `provider: none`.

The plain-text form flattens the same fields, one `key: value` per line,
in a fixed order: `project.*`, `status`, version and build, uptime, mode,
timestamp, `features.*`, `checks.*`, `stats.*`.

The process exit status of `shortner --status` mirrors this: `0` when
running, `1` when not.

## Metrics

```
GET /server/metrics
GET /server/metrics/prometheus
GET /server/metrics/grafana
GET /server/metrics/loki
```

Mirrored at `/api/metrics[/{service}]`,
`/api/v1/server/metrics[/{service}]`, and — when
`server.metrics.root.enabled` is on — bare `/metrics`. All aliases are the
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
POST /api/v1/server/reports/csp
POST /api/v1/server/reports/nel
POST /api/v1/server/reports/default
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
| `GET /` | Home page with the link-creation form |
| `POST /` | Create a link from the form |
| `GET /{slug}` | Redirect to the destination |
| `GET /{slug}/stats` | Click analytics page |
| `GET /list` | Public paginated listing of all links |
| `GET /server` | Redirects to `/server/about` |
| `GET /server/about` | About page |
| `GET /server/help` | Help page |
| `GET /server/privacy` | Privacy policy |
| `GET /server/terms` | Terms of service |
| `GET /server/contact` | Contact form |
| `POST /server/contact` | Submit the contact form |
| `GET /server/security` | Security and vulnerability reporting page |
| `GET /server/security/policy` | Coordinated disclosure policy |
| `GET /server/security/thanks` | Researcher acknowledgments |
| `GET /server/healthz` | Health page |
| `POST /server/consent` | Cookie consent choice |
| `POST /server/theme` | Theme preference |
| `GET /static/*` | Embedded CSS, JS, and images |

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

Rate limiting can be disabled entirely with
`server.rate_limit.enabled: false`. See
[Configuration](configuration.md#rate-limiting).

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
