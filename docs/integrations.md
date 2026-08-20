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
| Reverse proxies | Supported |
| `robots.txt`, `security.txt`, `llms.txt` | Served |
| WebFinger, OpenID configuration, app association, MTA-STS | Present but disabled by default |
| Browser reporting (CSP, NEL) | Available |
| Webhooks, federation, OAuth/OIDC provider, machine identity | **None. Not implemented.** |
| Swagger / OpenAPI / GraphQL | **None. Not implemented.** |
| Tor hidden service, I2P eepsite | **None. Not implemented.** |

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
      - targets: ["shortner.example.com:8090"]
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

!!! note
    Business metrics — total links, links created in 24 hours, links
    clicked in 24 hours, active API tokens — are defined but not yet
    populated. Cache metrics are inert because the cache is not wired into
    the request path. Tor and I2P metrics do not exist, since neither
    feature is implemented.

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

Twelve events are supported, each independently switchable under
`server.notifications.email.events`: `startup`, `shutdown`,
`backup_complete`, `backup_failed`, `ssl_expiring`, `ssl_renewed`,
`ssl_renewal_failed`, `security_alert`, `scheduler_error`,
`update_available`, `update_installed`, and the operator-triggered `test`.

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
    `ssl_renewed` has no trigger in this build — there is no in-process
    observer of a successful renewal yet — so that event never fires even
    when enabled.

### Contact form relay

The web contact form relays submissions to the configured contact address
through the same notifier. Delivery is best-effort and never queued; a
failure to send does not fail the submission.

## Self-Update via GitHub Releases

The updater queries the GitHub Releases API for `apimgr/shortner`,
selecting from the `stable`, `beta`, or `daily` channel. The downloaded
artifact's SHA-256 is verified against the release's `sha256.txt` before
anything is replaced. See [CLI Reference](cli.md#update).

This is the only outbound network call the server makes on its own besides
the GeoIP database download and ACME certificate issuance.

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
proxy's address instead of the visitor's.

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
shortner.example.com {
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
`web.llms.include_schemas`, plus any `custom_sections`.

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

## Integrations That Do Not Exist

Stated explicitly so they are not assumed:

- **No webhooks.** There is no outbound callback on link creation, click,
  or any other event.
- **No federation.** No ActivityPub, no Matrix, no cross-instance
  protocol.
- **No OAuth or OIDC**, as provider or as client. There are no user
  accounts to authenticate.
- **No machine or agent identity framework** beyond bearer tokens.
- **No Swagger UI, OpenAPI document, or GraphQL endpoint.**
  [API Reference](api.md) is the API documentation.
- **No Tor hidden service and no I2P eepsite.** The configuration and
  health fields exist and honestly report both as disabled.
- **No cache backend in the request path.** The Valkey and Redis settings
  are accepted, and the production compose file even starts a Valkey
  sidecar, but nothing reads from or writes to it yet.
- **No third-party analytics and no telemetry**, by design and
  permanently.
