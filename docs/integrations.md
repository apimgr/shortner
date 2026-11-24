# Integrations

This page covers the external systems `shortner` talks to, the discovery
and protocol surfaces it exposes, and the ones it deliberately does not.

## Summary

| Surface | Status |
|---------|--------|
| Prometheus / Grafana / Loki metrics | Available, token-gated |
| GeoIP database | Available, local lookups only |
| SMTP email notifications | Available, auto-detected |
| GitHub Releases (self-update) | Available |
| CLI update discovery and binary downloads | Available when CLI builds are colocated with the server binary |
| Reverse proxies | Supported |
| `robots.txt`, `security.txt`, `llms.txt` | Served |
| Atom and RSS feeds | Served at `/feed.xml` and `/rss.xml` |
| WebFinger, OpenID configuration, app association, MTA-STS | Present but disabled by default |
| Browser reporting (CSP, NEL) | Available |
| Role-based webhooks | Available for operator events and contact/security reports |
| Federation, OAuth/OIDC provider, machine identity | **None. Not implemented.** |
| Swagger / OpenAPI / GraphQL | Available at `/server/docs/swagger`, `/server/docs/graphql`, `/api/swagger`, and `/api/graphql` |
| Tor v3 hidden service | Available when a Tor binary is detected |
| I2P eepsite | On by default in containers; opt-in on hosts; needs i2pd or a SAM provider |
| PGP key management | Available through the maintenance CLI |

## Metrics

Prometheus-format metrics, a Grafana-oriented JSON view, and a Loki log
stream are served from:

```
/server/metrics
/server/metrics/prometheus
/server/metrics/grafana
/server/metrics/loki
```

with identical handlers mirrored at `/api/metrics[/{service}]`,
`/api/v1/server/metrics[/{service}]`, and — when
`server.metrics.root.enabled` is on — bare `/metrics`. None of the aliases
is a redirect.

The importable Grafana dashboard includes HTTP, database, cache, system,
scheduler run/duration/health, and shortener business panels for total links,
links created in 24 hours, and clicks in 24 hours.

### Authentication

Each service has its own bearer token, configured separately:

```yaml
server:
  metrics:
    enabled: true
    auth:
      allow_unauthenticated: false
      tokens:
        prometheus: "..."
        grafana: "..."
        loki: "..."
```

The token must be in the `Authorization` header. The `?token=` query
fallback that other endpoints accept is deliberately not honored here, so
a scrape URL in a config file does not carry the credential. Comparison is
constant-time. A service with no configured token returns `403`; a wrong
token returns `401`.

`allow_unauthenticated` exists only for a fully firewalled deployment.
Leave it off.

### Prometheus scrape config

```yaml
scrape_configs:
  - job_name: shortner
    metrics_path: /server/metrics/prometheus
    authorization:
      credentials: "your-prometheus-token"
    static_configs:
      - targets: ["csj.lol:8090"]
```

### What is exported

All metrics carry the `shortner_` prefix.

| Family | Covers |
|--------|--------|
| HTTP | Request counts, latency histograms, response sizes, status codes |
| Database | Connection pool state, query latency |
| Scheduler | Task runs, failures, durations |
| System | CPU, memory, disk — when `include_system` is on |
| Go runtime | Goroutines, GC, heap — when `include_runtime` is on |

Histogram buckets for durations and sizes are configurable via
`server.metrics.duration_buckets` and `server.metrics.size_buckets`.

The Loki service replays a bounded in-memory ring buffer of recent log
lines, sized by `server.metrics.loki.max_entries` and
`server.metrics.loki.max_age`.

Business gauges cover total links, links created and clicked in 24 hours,
and active API tokens. Tor circuit and request gauges are collected when
Tor is configured. Cache metrics cover rate-limit counter hits and misses,
evictions, item count, and bytes. Redis and Valkey report size only when a
cache prefix scopes the keys; the in-process limiter reports its active
counter state. I2P metrics are not defined by the current AI.md contract.

## GeoIP

GeoIP is used for two things: enriching click analytics with a country and
region, and optional country-based access blocking.

```yaml
server:
  geoip:
    enabled: true
    deny_countries: []
    allow_countries: []
    databases:
      asn: true
      country: true
      city: true
```

Databases are MaxMind-format `.mmdb` files fetched from the public
`ip-location-db` distribution over a CDN. They are downloaded in the
background on first run — startup is never blocked on it — and refreshed
weekly by the `geoip_update` scheduler task.

All lookups are local. **No visitor IP is ever sent to a third party.**
Lookups fail open: if a database is missing or an address is not found,
the request proceeds and the country and region fields are simply omitted.

Country data is licensed CC BY 4.0; the attribution appears on the
instance's own about page and in `LICENSE.md`.

Country blocking should be treated as a coarse risk signal, not as an
access-control boundary — it is trivially bypassed with a VPN.

## Email and SMTP

`shortner` sends operational notifications by SMTP. There is nothing to
configure in the common case.

```yaml
server:
  notifications:
    email:
      smtp:
        host: ""
        port: 587
        username: ""
        password: ""
        tls: "auto"
      from:
        name: ""
        email: ""
      reply_to: ""
```

### Auto-detection

With `host` empty the server probes for a relay at startup, in priority
order:

1. `127.0.0.1` — a relay on the same machine
2. `172.17.0.1` — the Docker bridge gateway
3. The default gateway address (Linux)
4. The detected FQDN
5. The host's global IPv4 address
6. `mail.{fqdn}`
7. `smtp.{fqdn}`

Each candidate is tried on ports 25, 465, and 587 in that order, and the
first successful EHLO handshake wins. The detected server is written back
into `server.yml`.

With `host` set explicitly, that server is tested at startup instead. If
the test fails, email is disabled gracefully and the server still starts.

### No SMTP means no email

If nothing is found, email is **off**. Messages are not queued, not
retried, and not logged as "would have sent". This is deliberate: a silent
queue that never drains is worse than no email at all.

### TLS modes

| Value | Behavior |
|-------|----------|
| `auto` | Negotiate the best available (default) |
| `starttls` | Require STARTTLS |
| `tls` | Implicit TLS from connect |
| `none` | Plaintext |

### Environment overrides

`SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`,
`SMTP_FROM_NAME`, and `SMTP_FROM_EMAIL` override the corresponding
settings. Empty values change nothing, so a container can supply only what
it needs.

### Events and templates

Thirteen email events are supported. The operator event switches are under
`server.notifications.email.events`: `startup`, `shutdown`,
`backup_complete`, `backup_failed`, `ssl_expiring`, `ssl_renewed`,
`ssl_renewal_failed`, `security_alert`, `scheduler_error`,
`update_available`, `update_installed`, and the operator-triggered `test`.
The `database_error` notification has its own enable, failure threshold,
check interval, and repeat settings under `server.notifications.database_error`.

Defaults are conservative — failures notify, successes mostly do not.

Templates use a simple wire format: a `Subject:` line, a `---` separator,
then the body, with `{variable}` substitution. Custom templates go in
`{config_dir}/template/email/{event}.tmpl` and override the embedded
defaults; they reload live, and deleting one restores the default.

Manage them with the `email` subcommand — see
[CLI Reference](cli.md#email).

Messages are RFC 5322 and are built with explicit guards against CRLF
header injection and dot-stuffing errors.

!!! note
    `ssl_renewed` is emitted after autocert renews a certificate and the
    app-managed certificate copy is saved. Initial certificate issuance
    does not count as a renewal.

### Report form relay

The private report form accepts abuse, DMCA, and privacy reports after a
server-rendered review and confirmation step. Abuse and DMCA reports route to
the abuse recipient; privacy requests route to the DPO recipient. Each falls
back to the general recipient and then the administrator recipient. Delivery
is best-effort and never queued; a failure to send does not fail the
submission. Security reports use their dedicated encrypted reporting flow.

## Self-Update via GitHub Releases

The updater queries the GitHub Releases API for `apimgr/shortner`,
selecting from the `stable`, `beta`, or `daily` channel. The downloaded
artifact's SHA-256 is verified against the release's `sha256.txt` before
anything is replaced. See [CLI Reference](cli.md#update).

Other outbound connections include GeoIP database downloads, ACME
issuance, GitHub release checks, Tor bootstrap, and configured SMTP
delivery.

## Reverse Proxies

Terminate TLS at a proxy if you are not using the built-in ACME support.
Then declare the proxy so client IPs resolve correctly:

```yaml
server:
  trusted_proxies:
    additional:
      - "203.0.113.10"
      - "10.20.0.0/16"
```

Private ranges are always trusted; you only need to add public addresses.
Without this, rate limiting, GeoIP, and click analytics all see the
proxy's address instead of the visitor's. Configured hostnames are resolved
again every five minutes, so proxy address changes take effect without a
server restart.

### nginx

```nginx
location / {
  proxy_pass http://127.0.0.1:8090;
  proxy_set_header Host $host;
  proxy_set_header X-Real-IP $remote_addr;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  proxy_set_header X-Forwarded-Proto $scheme;
}
```

`X-Forwarded-Proto` matters: HSTS is only sent when the request is
recognized as HTTPS.

### Caddy

```
csj.lol {
  reverse_proxy 127.0.0.1:8090
}
```

## Browser Reporting Endpoints

Modern browsers post structured reports to:

```
POST /api/v1/server/reports/csp
POST /api/v1/server/reports/nel
POST /api/v1/server/reports/default
```

These are unauthenticated because the browser sends them, and they
**always return `204` with an empty body** — nothing the caller submits is
ever echoed, so they cannot be turned into an open reflector.

Network Error Logging is enabled by default with a 30-day max age and full
sampling; both are tunable under `web.headers.nel`.

## Discovery Files

| Path | Default | Content |
|------|:-------:|---------|
| `/robots.txt` | on | Generated from `web.robots.allow` and `web.robots.deny` |
| `/.well-known/security.txt` | on | RFC 9116: contact, policy, expiry, preferred languages, keyservers |
| `/.well-known/pgp-key.asc` | routed | The PGP public key, or `404` when none exists |
| `/llms.txt`, `/.well-known/llms.txt` | on | AI-agent-oriented description of the service |

`llms.txt` content is controlled by `web.llms.include_endpoints` and
`web.llms.include_schemas`, plus up to ten `custom_sections` entries in
`{title, content}` form. Titles are 1–80 characters and content is Markdown
up to 4096 characters. HTML is escaped; links may target HTTPS or relative
paths. Invalid entries are skipped with one warning.

## Optional Well-Known Endpoints

These are recognized but **disabled by default**. Enabling one only routes
it; the content still has to make sense for your deployment.

| Path | Setting |
|------|---------|
| `/.well-known/webfinger` | `web.well_known.webfinger.enabled` |
| `/.well-known/openid-configuration` | `web.well_known.openid_configuration.enabled` |
| `/.well-known/assetlinks.json` | `web.well_known.assetlinks.enabled` |
| `/.well-known/apple-app-site-association` | `web.well_known.apple_app_site_association.enabled` |
| `/.well-known/mta-sts.txt` | `web.well_known.mta_sts.enabled` |

The namespace is allowlist-only: anything not enabled returns `404`, never
a redirect.

Serving `openid-configuration` does **not** make this an OpenID provider —
no OAuth or OIDC flow is implemented. The endpoint exists so an operator
who fronts the domain with a real identity provider can publish metadata
at the expected path.

## Webhooks

Outbound webhooks use the role configuration under
`server.contact.{admin,security,abuse,general}.webhooks`. Supported adapters
are Telegram, Discord, Slack, Mattermost, Pushover, Gotify, and generic HTTPS
JSON. Events include operator notifications and accepted contact/security
reports; shortened-link creation and click events are not emitted.

Every request carries an HMAC-SHA256 `X-Webhook-Signature`, an
`X-Webhook-Timestamp`, a stable retry `X-Webhook-ID`, the event name, and a
project/version user agent. Configure each generated per-endpoint signing
secret from `secrets.yml` at the receiver. Failed deliveries retry after
1 minute, 5 minutes, 15 minutes, 1 hour, 6 hours, and 24 hours, then log a
redacted failure without exposing endpoint URLs or payload contents. Failure
logs are limited to one line per role and transport per minute.

Pushover priorities are 0 for info/warning and 1 for critical. Gotify uses 2,
5, and 8 for info, warning, and critical. Security report webhooks contain
only tracking ID, severity, component, and operator review instructions;
full report content and reporter contact details are never sent by webhook.

## Integrations Not Yet Implemented

Stated explicitly so they are not assumed:

- **No federation.** No ActivityPub, no Matrix, no cross-instance
  protocol.
- **No OAuth or OIDC**, as provider or as client. There are no user
  accounts to authenticate.
- **No machine or agent identity framework** beyond bearer tokens.
- **OpenAPI and GraphQL** are available through the API documentation pages
  and the versioned and current-version API endpoints. GraphQL is read-only.
- **MCP endpoint and help page** expose eligible API operations as tools over
  Streamable HTTP, reuse API Bearer tokens, and are documented at
  `/server/help/mcp`.
- **Cache scope.** Redis and Valkey provide shared atomic rate-limit
  counters. General page, API-response, GeoIP, and blocklist caching is
  not implemented as a shared cache feature. IP and domain blocklist feeds
  are loaded into the blocklist manager and checked by request middleware;
  their feed files use independent atomic refresh and freshness handling.
- **No third-party analytics and no telemetry**, by design and
  permanently.

## Progressive Web App

The server serves an embedded manifest at `/manifest.json` and a root-scoped
service worker at `/sw.js`. The worker synthesizes its offline document from
translated strings stored by the first page load; there is no static
`/offline.html` route or asset.
Service workers require HTTPS in browsers. The install prompt, iOS install
instructions, user-confirmed update prompt, and online/offline indicator
enhance the pages. The worker caches version-stamped static assets and only
HTML navigations explicitly marked `X-Offline-Cache: public`; it removes a
cached copy when a later response is private, fails, or redirects. Forms and
personalized pages stay network-only, and API responses are always network-only.
Background Sync and geolocation are not implemented: the product-specific
offline actions, replay/privacy rules, and location use case are undefined in
the current API template.

## Tor and I2P

Tor support is enabled automatically when a Tor executable is available.
The server manages a v3 hidden service and serves it over HTTP on the
dedicated `.onion` address. Install Tor separately; the built-in manager
does not turn the service into a Tor relay or exit node.

The maintenance CLI controls Tor through loopback-only internal endpoints.
These endpoints are absent from the public API and feature listings. Mutating
operations require the server to be running; vanity searches run as
background jobs in the server and can be cancelled or inspected through the
CLI.

Publishing either overlay address through `/api/autodiscover` is disabled
by default. Set `server.tor.expose: true` or `server.i2p.expose: true` to
advertise the corresponding address; enabling the overlay alone does not
publish it.

I2P support is off by default on bare metal and VMs, and on by default in
containers. The binary computes that default; the Docker image does not set
`I2P_ENABLED`. Runtime precedence is `--i2p`, `I2P_ENABLED`, an explicit
`server.i2p.enabled` value, then the environment-derived default. The server
can use a managed `i2pd` process or an external SAMv3 bridge. Its `.b32.i2p`
address is served over HTTP.

Both overlays have health checks and scheduler tasks. Metrics for overlay
state remain unpopulated.
