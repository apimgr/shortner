# Configuration

All settings live in a single YAML file, `server.yml`, in the
configuration directory. It is created with defaults on first run. See
[Installation](installation.md#filesystem-paths) for where it lands on each
platform.

## Precedence

For any setting that has more than one source:

```
command-line flag  >  environment variable  >  server.yml  >  built-in default
```

## Validation Never Blocks Startup

An invalid value is **never** a fatal error. Every setting is validated on
load; anything invalid is logged as a warning and replaced with its
default, and the server starts. A typo in `server.yml` degrades one
setting — it does not take the service down.

A second class of warning is informational: the value is kept, but you are
told it may not do what you expect. For example, an HSTS `max_age_seconds`
below one year is accepted but flagged as ineligible for the HSTS preload
list.

## Hot Reload

**Configuration is not currently hot-reloadable.** Changes to `server.yml`
take effect on the next restart. `SIGHUP` is deliberately ignored by the
process — it is reserved for a future file-watcher-driven reload — so
sending it will not reload anything.

## Modes and Debug

Mode and debug are two orthogonal settings.

| Setting | Values | Default |
|---------|--------|---------|
| Mode | `production`, `development` | `production` |
| Debug | boolean | `false` |

Resolution order:

- **Mode**: `--mode` flag → `MODE` environment variable → `production`.
- **Debug**: `--debug` flag → `DEBUG` environment variable → the
  `--mode debug` alias → `false`.

`--mode debug` (or `MODE=debug`) is an alias that expands to development
mode *with* debug enabled. An explicit `--debug` or `DEBUG` still wins over
that alias, so `MODE=debug DEBUG=false` gives you debug mode with debug
output off.

That yields four operational states: production, production with debug,
development, and development with debug.

Boolean values everywhere in the configuration — environment variables,
YAML, and flags — go through one shared parser, so `yes`, `on`, `1`,
`true`, `enabled`, and their negatives are all accepted consistently.

## Directory Overrides

Each directory can be overridden by flag or environment variable. Flags
win.

| Flag | Environment variable | Overrides |
|------|----------------------|-----------|
| `--config` | `CONFIG_DIR` | Configuration directory |
| `--data` | `DATA_DIR` | Data directory |
| `--cache` | `CACHE_DIR` | Cache directory |
| `--log` | `LOG_DIR` | Log directory |
| `--backup` | `BACKUP_DIR` | Backup directory |
| `--pid` | `PID_FILE` | PID file path |
| — | `DATABASE_DIR` | Database directory |

## Environment Variables

Beyond the directory overrides above, the following are read:

| Variable | Effect |
|----------|--------|
| `MODE` | Operational mode |
| `DEBUG` | Debug flag |
| `CONTAINER` | Forces container detection |
| `SMTP_HOST` | Overrides `server.notifications.email.smtp.host` |
| `SMTP_PORT` | Overrides the SMTP port |
| `SMTP_USERNAME` | Overrides the SMTP username |
| `SMTP_PASSWORD` | Overrides the SMTP password |
| `SMTP_TLS` | Overrides the SMTP TLS mode |
| `SMTP_FROM_NAME` | Overrides the From display name |
| `SMTP_FROM_EMAIL` | Overrides the From address |

An unset or empty variable changes nothing, so a container can override
exactly the fields it needs and leave the rest to `server.yml`.

## Core Server Settings

```yaml
server:
  # Operator API token; generated on first run
  token: "tok_..."
  listen: "0.0.0.0"
  port: "8090"
  baseurl: "/"
  api_version: "v1"
```

`api_version` must not contain `/`, `?`, or `#`; an invalid value falls
back to `v1`. It determines the API path prefix, `/api/v1` by default.

### Database

```yaml
server:
  database:
    driver: "sqlite"
    url: "{data_dir}/db/server.db"
```

Valid drivers are `sqlite` (the default), `libsql`, and `none`. The driver
is pure Go — there is no CGO and no system SQLite dependency.

### Limits and timeouts

```yaml
server:
  limits:
    max_body_size: "10MB"
    read_timeout: "30s"
    write_timeout: "30s"
    idle_timeout: "120s"
```

### Compression

```yaml
server:
  compression:
    enabled: true
    level: 5
    types:
      - "text/html"
      - "text/css"
      - "text/javascript"
      - "application/json"
      - "application/xml"
```

Level is clamped to 1–9.

### Trusted proxies

```yaml
server:
  trusted_proxies:
    additional: []
```

Private ranges are always trusted. Add public proxy addresses or CIDRs
here so forwarded client IPs are honored.

### Rate limiting

```yaml
server:
  rate_limit:
    enabled: true
    read:
      requests: 120
      window: 60
    write:
      requests: 10
      window: 60
    health:
      requests: 120
      window: 60
    global_burst: 240
```

Limits are per client IP, counted in a sliding window, with independent
counters for reads (`GET`/`HEAD`), writes
(`POST`/`PUT`/`PATCH`/`DELETE`), and health checks. `global_burst` is a
ceiling across all classes. Exceeding a limit returns `429 Too Many
Requests` with a `Retry-After` header. See
[API Reference](api.md#rate-limiting).

### Cache

```yaml
server:
  cache:
    type: "memory"
    prefix: "shortner:"
    ttl: "1h"
```

Valid types are `memory`, `valkey`, `redis`, and `none`. Connection
settings (`url`, `host`, `port`, `username`, `password`, `db`, `tls`,
`pool_size`, `min_idle`, `timeout`) are also available.

!!! note
    The cache package exists and is configurable, but it is not yet wired
    into the request path. Changing these values has no observable effect
    today, and the cache metrics report zero.

### CORS and CSRF

```yaml
server:
  cors:
    allowed_origins:
      - "*"
    allow_credentials: false
    max_age: 600
  csrf:
    enabled: true
    token_length: 32
    cookie_name: "csrf_token"
    header_name: "X-CSRF-Token"
    secure: "auto"
    exempt_paths: []
```

CSRF uses a double-submit cookie on HTML form routes. It does not apply to
`GET`/`HEAD` or to token-authenticated API requests.

### TLS

```yaml
server:
  tls:
    enabled: false
```

When enabled, certificates are obtained automatically via ACME using the
HTTP-01 and TLS-ALPN-01 challenges.

!!! warning
    DNS-01 challenges are not implemented. The `dns_provider` and
    `dns_credentials` keys are present in the schema but no provider
    matrix exists behind them, and DNS credentials are not encrypted at
    rest. Do not rely on them.

## Branding, Contact, and Pages

```yaml
server:
  branding:
    site_name: ""
    tagline: ""
    logo_url: ""
  seo:
    description: ""
    keywords: ""
  contact:
    general:
      email: ""
    admin:
      email: ""
    abuse:
      email: ""
    security:
      email: ""
    dpo:
      name: ""
      email: ""
      address: ""
```

The `admin` address is internal only and is never rendered on a page. The
`security` address feeds `/.well-known/security.txt`; when empty it
resolves to `security@` at the detected FQDN. The `dpo` block drives the
`/server/dpo` page, which is only routed when compliance settings or a DPO
name require it.

Static page content is Markdown under `pages:`:

```yaml
pages:
  about:
    content: ""
  privacy:
    content: ""
  help:
    content: ""
  terms:
    content: ""
  contact:
    enabled: true
    captcha: "simple"
    success_message: "Thank you for your message. We'll respond soon."
```

## Privacy and Consent

```yaml
server:
  privacy:
    data:
      sold: false
      stored_on_server: false
      sharing: []
    consent:
      message: "We use essential cookies..."
      buttons:
        decline: "Decline"
        accept: "Accept"
    cookies:
      essential:
        enabled: true
      preferences:
        enabled: true
      analytics:
        enabled: false
    retention:
      export_available: false
      deletion_available: false
    third_party:
      services: []
```

Setting `data.sold: true` routes the `/server/ccpa` page and swaps in the
alternate consent message.

## Compliance

```yaml
server:
  compliance:
    enabled: false
    gdpr: false
    ccpa: false
    hipaa: false
    soc2: false
    pci_dss: false
    iso27001: false
    fedramp: false
    lgpd: false
    pipeda: false
    appi: false
    pdpa: false
```

Enabling compliance forces backup encryption — an unencrypted backup is
refused while it is on.

## Scheduler

```yaml
server:
  scheduler:
    timezone: "America/New_York"
    catch_up_window: "1h"
    tasks:
      token_cleanup:
        schedule: "@every 15m"
        enabled: true
```

The scheduler is internal. External cron and systemd timers are never
used. Schedules accept cron expressions or `@every`/`@hourly` notation,
and the timezone is any IANA name. A task whose scheduled run was missed
while the process was down is executed on startup if the miss falls inside
`catch_up_window`.

Twelve built-in tasks are registered:

| Task | Default schedule | Status |
|------|------------------|--------|
| `token_cleanup` | every 12 hours | Active |
| `log_rotation` | daily | Active |
| `healthcheck_self` | every 10 minutes | Active |
| `ssl_renewal` | daily | Active — checks the renewal window and sends alerts |
| `geoip_update` | weekly, Sunday | Active |
| `backup_daily` | daily | Active |
| `backup_hourly` | hourly | Disabled by default |
| `update_check` | daily | Active |
| `blocklist_update` | daily | Registered; skips — the subsystem is not built |
| `cve_update` | daily | Registered; skips — the subsystem is not built |
| `tor_health` | every 10 minutes | Registered; skips — Tor is not implemented |
| `i2p_health` | every 10 minutes | Disabled; I2P is not implemented |

Tasks that cannot do real work skip with a logged reason rather than
pretending to succeed. See [CLI Reference](cli.md#scheduler) for the
management commands.

## GeoIP

```yaml
server:
  geoip:
    enabled: true
    dir: ""
    deny_countries: []
    allow_countries: []
    databases:
      asn: true
      country: true
      city: true
```

Databases are downloaded from a public CDN on first run in the background,
and refreshed weekly by the `geoip_update` task. `deny_countries` and
`allow_countries` take ISO 3166-1 alpha-2 codes and drive optional
country-blocking middleware. GeoIP is also what populates the country and
region fields in click analytics. See
[Integrations](integrations.md#geoip).

## Metrics

```yaml
server:
  metrics:
    enabled: true
    root:
      enabled: true
    auth:
      allow_unauthenticated: false
      tokens:
        prometheus: ""
        grafana: ""
        loki: ""
    include_system: true
    include_runtime: true
```

Each metrics service is gated by its own bearer token. An empty token
means that service is unavailable. `allow_unauthenticated` is an escape
hatch for a firewalled deployment and should stay off otherwise. See
[Integrations](integrations.md#metrics).

## Backup

```yaml
server:
  backup:
    encryption:
      enabled: false
      hint: ""
    retention:
      max_backups: 1
      keep_weekly: 0
      keep_monthly: 0
      keep_yearly: 0
      max_total_size: "10%"
    disk_threshold: 90
```

`encryption.enabled` is set automatically when a password is configured —
do not toggle it by hand. The password is only ever supplied through an
interactive prompt; there is no password flag, so it cannot leak into
shell history or a process listing. `hint` is a non-secret reminder.

Retention is tiered: yearly beats monthly beats weekly beats daily. A
backup is skipped when disk usage exceeds `disk_threshold` percent. See
[CLI Reference](cli.md#backup-and-restore).

## Updates

```yaml
server:
  update:
    branch: "stable"
    auto_install: false
    defer_days: 0
```

Valid branches are `stable`, `beta`, and `daily`. With `auto_install`
off — the default — the `update_check` task only notifies. `defer_days`
(0–365) makes the scheduled check ignore releases younger than that many
days; an explicit `shortner --update` bypasses the defer window.

## Security

```yaml
server:
  security:
    encryption_key: ""
    allowlist: []
    blocked_ips: []
    abuse_detection:
      enabled: true
      request_flood:
        multiplier: 10
        block_duration: "1h"
      auto_block_ip: true
      auto_alert: true
```

`encryption_key` is generated on first run and is used to seal sensitive
records at rest. `allowlist` entries bypass blocking; `blocked_ips` are
permanent, config-file-only blocks. Entries with an invalid or dangerously
broad CIDR (an IPv4 prefix shorter than `/8`, or an IPv6 prefix shorter
than `/16`) are dropped with a warning. See [Security](security.md).

## Notifications

```yaml
server:
  notifications:
    webui:
      position: "top-right"
      duration: 5
    email:
      enabled: false
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
      events:
        startup: false
        shutdown: false
        backup_complete: false
        backup_failed: true
        ssl_expiring: true
        ssl_renewed: false
        ssl_renewal_failed: true
        security_alert: true
        scheduler_error: true
        update_available: false
        update_installed: true
```

`email.enabled` is set automatically from whether a working SMTP server
was found — it is not a manual toggle. With `host` empty, the server
auto-detects a relay at startup; with `host` set, it tests that server and
disables email if the test fails. **If no SMTP server is available, email
is simply off.** Nothing is queued, nothing is retried, and nothing is
logged as "would have sent". See
[Integrations](integrations.md#email-and-smtp).

!!! note
    The Web UI toast settings are read and validated, but toasts are not
    yet rendered in the interface.

## Web Headers and Content Security Policy

```yaml
web:
  theme: "dark"
  hsts:
    enabled: true
    max_age_seconds: 63072000
    include_subdomains: true
    preload: true
  headers:
    coop: "unsafe-none"
    coep: "unsafe-none"
    corp: "cross-origin"
    origin_agent_cluster: true
    cross_domain_policies: "none"
    dns_prefetch_control: ""
    honor_sec_gpc: true
    honor_dnt: false
    sec_fetch_validation: true
    server_timing_in_debug_only: true
    clear_site_data:
      on_token_revocation: true
      on_consent_withdrawal: true
      execution_contexts: false
    nel:
      enabled: true
      max_age_seconds: 2592000
      include_subdomains: true
      sample_rate: 1.0
  csp:
    enabled: true
    mode: "enforce"
    reports_enabled: true
    reports_sample_rate: 1.0
```

The CSP is assembled from a secure baseline. Each of the fourteen
directives has both an `_extra` key, which appends sources to the
baseline, and an `_override` key, which replaces the directive outright —
for example `script_src_extra` and `script_src_override`. Prefer `_extra`.

In development mode the CSP downgrades to report-only unless `mode` was
set explicitly.

`web.permissions_policy` holds a map of 25 browser features, all locked
down by default. `web.robots` controls the generated `robots.txt`.
`web.announcements.items` and `web.footer.custom_html` control page
chrome; a footer value of a single space disables the footer, while an
empty string keeps the default branding.

!!! note
    Configured announcements are stored and validated but are not yet
    rendered as a banner in the web interface.

## Well-Known and Discovery

```yaml
web:
  llms:
    enabled: true
    include_endpoints: true
    include_schemas: false
  well_known:
    webfinger:
      enabled: false
    openid_configuration:
      enabled: false
    assetlinks:
      enabled: false
    apple_app_site_association:
      enabled: false
    mta_sts:
      enabled: false
  security:
    report_url: "https://github.com/apimgr/shortner/security/advisories/new"
    contact: ""
    expires: ""
    keyservers:
      - "https://keys.openpgp.org"
    publish_pgp_key: true
    preferred_languages: "en"
    policy: ""
    thanks: []
```

The `/.well-known/` namespace is allowlist-only. Anything not explicitly
enabled returns `404` — never a redirect and never a generic handler. See
[Security](security.md#the-well-known-namespace).

## Reference: Settings That Fall Back on Invalid Input

A representative selection of the validation rules:

| Setting | Replaced with |
|---------|---------------|
| `server.port` | `8090` |
| `server.limits.*` | The documented default duration or size |
| `server.compression.level` outside 1–9 | `5` |
| `server.rate_limit.*` at or below zero | The class default |
| `server.cache.type` not in the valid set | `memory` |
| `server.api_version` containing `/`, `?`, `#` | `v1` |
| `server.security.allowlist[*]` invalid or too broad | Entry removed |
| `server.backup.retention.max_backups` below 1 | `1` |
| `server.backup.disk_threshold` outside 1–100 | `90` |
| `server.update.branch` not `stable`/`beta`/`daily` | `stable` |
| `server.update.defer_days` outside 0–365 | `0` |
| `web.headers.coop` / `coep` / `corp` invalid | The documented default |
| `web.csp.mode` not `enforce`/`report-only` | `enforce` |
| Any sample rate outside 0.0–1.0 | `1.0` |
| `server.notifications.email.smtp.port` outside 1–65535 | `587` |
| `server.notifications.email.smtp.tls` invalid | `auto` |
| A malformed From or Reply-To address | Empty, and the header is omitted |
