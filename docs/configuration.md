# Configuration

Configuration is merged from `server.yml`, optional ordered files in
`yaml.d/`, and `secrets.yml` (which has the final precedence). `server.yml`
contains this node's listener and identity settings. At startup, legacy area
settings are migrated into `yaml.d/`, with the original preserved as
`server.yml.bak`. See
[Installation](installation.md#filesystem-paths) for where it lands on each
platform.

Area files are named by purpose: `branding.yml`, `web.yml`, `pages.yml`,
`contact.yml`, `privacy.yml`, `security.yml`, `logs.yml`, `metrics.yml`,
`notifications.yml`, `tor.yml`, `i2p.yml`, `maintenance.yml`, `accounts.yml`,
and `app.yml`. `secrets.yml` contains credentials only. Files are private
(`0600`; directories `0700`).

## Precedence

For any setting that has more than one source:

```
command-line flag  >  environment variable  >  merged config files  >  built-in default
```

File precedence is built-in defaults, `server.yml`, `yaml.d/` files in
filename order, and `secrets.yml`. Drop-in maps merge recursively; later
scalar or list values replace earlier values. Generated operator, encryption,
and metrics credentials are stored in `secrets.yml`. Credentials found in
`server.yml` or drop-ins are moved there before the merged configuration is
loaded.

## Validation Never Blocks Startup

Invalid individual values are logged as warnings and replaced with their
defaults. A drop-in or secrets file that cannot be parsed is ignored and its
last valid configuration remains active; when no prior valid version exists,
the built-in defaults apply. Configuration errors never prevent startup.

A second class of warning is informational: the value is kept, but you are
told it may not do what you expect. For example, an HSTS `max_age_seconds`
below one year is accepted but flagged as ineligible for the HSTS preload
list.

## Hot Reload

Changes to `server.yml`, `yaml.d/`, `secrets.yml`, and `assets/` trigger a
configuration reload. Invalid values use their defaults; an unparseable file
keeps the last valid configuration. Listener address and port changes require
a restart. `SIGHUP` is ignored.

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

## Language

Internationalization is always enabled. Only `default_language` and
`available_languages` are configurable; fallback to English and the `lang`
cookie name and one-year lifetime are fixed.

```yaml
server:
  i18n:
    default_language: en
    available_languages: [en, es, fr, de, it, ar, ja, zh]
```

Language selection follows `?lang=`, the explicit `lang` cookie,
`Accept-Language`, then the configured default. A supported `?lang=` choice
and an explicit selection in `/server/preferences` write the one-year
cookie; automatic detection does not. `/server/preferences/sync` can carry
theme, language, and application preference cookies to another device, but
never consent or credential cookies.

## Core Server Settings

```yaml
server:
  # Operator API token; generated on first run
  token: "adm_..."
  address: "[::]"
  port: "8090"
  daemonize: false
  baseurl: "/"
  api_version: "v1"
```

`server.port` accepts one port or an HTTP,HTTPS pair such as `"80,443"`.
`server.daemonize` is a startup-only setting for manual starts; `--daemon`
overrides it, and service-managed or container starts remain in the foreground.
In dual-port mode the first listener serves HTTP (including ACME HTTP-01
challenges) and the second serves HTTPS; the startup banner prints both
URLs. A single port of `443` enables TLS automatically. With a single
non-443 port, set `server.ssl.enabled: true` to serve HTTPS on that port.

The default address `[::]` listens on IPv4 and IPv6. If the kernel does not
support IPv6 sockets, Shortner falls back to `0.0.0.0` and logs one warning.
Setting `server.address` or `LISTEN` explicitly to `0.0.0.0` keeps the listener
IPv4-only; it is never widened to all interfaces or another address.

`api_version` must not contain `/`, `?`, or `#`; an invalid value falls
back to `v1`. It determines the API path prefix,
`https://csj.lol/api/v1` by default.

### Owner tokens

```yaml
server:
  tokens:
    max_linked: 25
    cookie_max_age: 1y
    idle_purge_days: 0
```

The primary owner token is issued when a new anonymous owner is created.
Linked tokens can be named and given an expiry from the owner-token page.
The primary-token cookie name is the fixed IDEA.md project variable
`shortner_owner_token_zgnygola`. It is HttpOnly, Secure, SameSite=Strict,
and its configured lifetime slides forward on valid use. `idle_purge_days: 0`
disables idle purging; owners with links are never purged.

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

### Log configuration

Each named log inherits `custom`, `rotate`, and `keep` from `server.logs.defaults`.
Per-kind values override those defaults. Log filenames are inferred from the
kind and are not configurable. The access log supports `apache`, `nginx`, and
`json` formats.

```yaml
server:
  logs:
    level: warn
    defaults:
      custom: ""
      rotate: weekly,50MB
      keep: none
    access:
      format: apache
      rotate: monthly
    server:
      format: text
    error:
      format: text
    audit:
      enabled: true
      format: json
      rotate: daily
      compress: false
    security:
      format: fail2ban
    debug:
      enabled: false
      format: text
```

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
    Redis and Valkey share atomic rate-limit counters across server
    instances. Cache metrics report rate-limit hits and misses; item count and
    bytes are sampled for the in-process cache and for Redis/Valkey when a
    cache prefix is configured. Other application data paths do not currently
    use the cache.

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
  ssl:
    enabled: false
    cert: ""
    key: ""
    min_version: TLS1.2
    dns_provider: ""
    dns_credentials: {}
```

When enabled, certificates are obtained automatically via ACME using the
HTTP-01 and TLS-ALPN-01 challenges.

Set `cert` and `key` together to use a manually managed certificate. Paths
inside `config_dir/ssl/` are preserved by backup; paths outside it are backed
up under `ssl-external/` and rewritten into `config_dir/ssl/` on restore.

For DNS-01, set the lego provider name and a flat map of that provider's
environment variable names to values. For example, Cloudflare accepts
`CF_DNS_API_TOKEN`; credentials are stored in `secrets.yml` and never
exposed by an API or view.
The complete provider list and each provider's variable names are maintained
by [lego](https://go-acme.github.io/lego/dns/). A configured DNS provider is
validated at startup and before certificate issuance. Failed validation
reports the variable name and does not fall back to HTTP-01 or TLS-ALPN-01.

## Branding, Contact, and Pages

```yaml
server:
  contact:
    general:
      email: ""
      webhooks:
        telegram: ""
        discord: ""
        slack: ""
        mattermost: ""
        pushover: ""
        gotify: ""
        generic: ""
    admin:
      email: ""
      webhooks: {}
    abuse:
      email: ""
      webhooks: {}
    antispam:
      honeypot: true
      min_fill_seconds: 3
      max_age_seconds: 43200
      review_min_seconds: 2
      review_max_seconds: 900
      per_ip: 5
      per_email: 3
      global: 60
      window: 1h
      max_links: 10
      reject_markup: true
      blocked_words: []
      blocked_email_domains: []
      strikes: 3
      ban_hours: 24
      duplicate_window_hours: 24
    security:
      email: ""
      webhooks: {}
    dpo:
      name: ""
      email: ""
      address: ""
```

`server.contact.admin.email` is the required recipient for operator email
notifications. If it is empty, startup warns and no operator email recipient
is selected; the general contact address is not used for admin events.

Webhook endpoint URLs can be set under each role's `webhooks` map. Built-in
adapters support Telegram, Discord, Slack, Mattermost, Pushover, Gotify, and
generic HTTPS JSON. The server generates a per-endpoint HMAC signing secret
and stores it with the webhook URL in `secrets.yml`. Configure the generated
secret on the receiving side. Security report webhooks receive metadata
summaries only, never the report body or reporter contact details.

Branding, SEO, and the default visitor theme belong in
`{config_dir}/yaml.d/branding.yml`. The server does not generate this file;
create it with mode `0600` to override embedded defaults:

```yaml
server:
  branding:
    title: "Shortner"
    tagline: ""
    description: ""
    favicon: ""
    logo: ""
    content:
      about:
        format: md
        body:
          default: "Run by the Example team."
      header:
        format: html
        body: ""
      privacy:
        format: md
        body: ""
      footer:
        format: html
        body: ""
      terms:
        format: md
        body: ""
  seo:
    keywords: []
    author: ""
    og_image: ""
    twitter_handle: ""
    title_separator: " - "
    meta_tags: {}
    canonical: ""
    noindex: false
    opengraph:
      enabled: true
      type: website
      site_name: ""
    twitter:
      enabled: true
      card: summary_large_image
      creator: ""
    verification:
      google: ""
      bing: ""
      yandex: ""
      baidu: ""
      pinterest: ""
      facebook: ""
      custom: []
web:
  ui:
    theme: dark
    accent: ""
```

Image values may be HTTPS URLs, `data:` URIs (up to 256 KiB), or file names
in `{config_dir}/assets/`. Local files must use `.svg`, `.png`, `.ico`,
`.webp`, or `.jpg` and be no larger than 2 MiB. An empty value uses the
embedded default. SEO settings, including feed and sitemap settings, are
assigned to `yaml.d/branding.yml` by the area-file schema.

Page content supports `md`, `html`, or `txt`; header and footer content use
HTML only. Language maps select the request language, then `default`.
Configured content is sanitized before rendering. `web.ui.accent` accepts a
six-digit hex color; the server adjusts lightness separately for dark and
light themes to meet the contrast rule.

The sitemap is generated dynamically at `/sitemap.xml`. It contains public
pages and unexpired short-link redirect and stats URLs; API and management
routes are excluded. When its URL count exceeds `max_urls`, the root file
becomes a sitemap index and refers to numbered sitemap pages. The protocol
maximum of 50,000 URLs per sitemap page is always enforced. `include_images`
currently has no effect because this product does not associate image assets
with links.

Public HTML pages emit route-specific robots directives, canonical URLs,
Open Graph and Twitter metadata, and JSON-LD. SEO verification values are
validated before rendering; invalid entries are omitted with a startup
warning. `canonical` overrides the request origin, `noindex` adds the
`X-Robots-Tag` header across responses, and disabled Open Graph or Twitter
settings omit their tag groups. Same-origin image paths are expanded to request-specific absolute
URLs. Remote branding images are fetched over HTTPS with DNS, redirect,
content-type, and size checks, then served from a daily cache on the same
origin. Logo, favicon, and OpenGraph variants are resized locally, so Tor and
I2P visitors do not contact third-party image hosts.

Logo variants preserve their aspect ratio at 200px and 50px widths. Favicon
variants (16, 32, 48, 180, 192, and 512 square pixels) fit inside a transparent
square without cropping. The 1200×630 OpenGraph variant covers the full frame
and is center-cropped.

Atom 1.0 and RSS 2.0 feeds are always available at `/feed.xml` and
`/rss.xml`. Both contain the same newest public, unexpired links.
`feed.max_items` is clamped to 1–200 during validation; `feed.title`
defaults to the site name.

The `admin` address is internal only and is never rendered on a page. The
`security` address feeds `/.well-known/security.txt`; when empty it
resolves to `security@` at the detected FQDN. The `dpo` block drives the
`/server/dpo` page, which is only routed when compliance settings or a DPO
name require it.

The help page content is configured under `server.pages:`. About, privacy, terms,
header, and footer extensions are configured as
`server.branding.content.*` in `yaml.d/branding.yml` and sanitized before
rendering. The terms entry replaces the built-in page; about and privacy
extend their built-in content.

Help `content` may be one Markdown string for every language or a map keyed by
language code. The request language is selected first, then the map's `default`
entry, then its first entry.

```yaml
server:
  pages:
    help:
      content:
        default: ""
        es: ""
      mcp: ""
    contact:
      enabled: true
      captcha: "simple"
      captcha_site_key: ""
      success_message: "Thank you for your message. We'll respond soon."
```

The CAPTCHA secret key belongs in `secrets.yml` at
`server.pages.contact.captcha_secret_key`, not in a shared configuration file.

The `simple` CAPTCHA creates a localized arithmetic challenge that works
without JavaScript and over Tor or I2P. `recaptcha` and `hcaptcha` require both
keys; submissions fail closed if the selected provider cannot be verified.
Overlay requests always use the simple challenge. The antispam settings reload
when `server.yml` changes. Rate counters, strike counts and active bans stay
in memory. Tor and I2P submissions do not share a per-IP counter or strike
ban. Rejected submissions are logged with only the reason and client IP.

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

All fourteen core built-in tasks required by AI.md are registered; Tor and
I2P health tasks are added when those networks are enabled:

| Task | Default schedule | Status |
|------|------------------|--------|
| `token_cleanup` | every 15 minutes | Active |
| `log_rotation` | daily at 00:00 | Active |
| `healthcheck_self` | every 5 minutes | Active |
| `disk_check` | every 5 minutes | Active |
| `database_check` | every minute | Active — probes both database handles and triggers the database outage notification |
| `ssl_renewal` | daily at 03:00 | Active — checks the renewal window and sends alerts |
| `geoip_update` | weekly, Sunday at 03:00 | Active |
| `backup_daily` | daily at 02:00 | Active |
| `backup_hourly` | hourly | Disabled by default |
| `update_check` | daily at 06:00 | Active |
| `blocklist_update` | daily at 04:00 | Active — refreshes the built-in and configured IP/domain feeds; retries failures after one hour |
| `cve_update` | daily at 05:00 | Active — downloads the NVD feed and optionally the Trivy OCI database |
| `tor_health` | every 10 minutes | Active when Tor is installed |
| `i2p_health` | every 10 minutes | Active when I2P is enabled |

If the process exits while a task is running, its stored state becomes
`interrupted`; the next start retries it immediately without counting a
failure or changing the previous error. Tasks that cannot do real work skip with a logged reason rather than
pretending to succeed. NVD data is stored under `data.security_dir/cve/`;
`data.cve.api_key` is kept in `secrets.yml`, and Trivy updates are opt-in via
`data.trivy.enabled`. See [CLI Reference](cli.md#scheduler) for management
commands.

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

Each metrics service is gated by its own `mtr_` bearer token. Missing
tokens are generated and written to `secrets.yml` on first run.
`allow_unauthenticated` is an escape
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

Retention categories are exclusive: January 1 backups are yearly, other
first-of-month backups are monthly, Sundays are weekly, and all others are
daily. The newest backups within each category fill that category's retention
limit; an older backup that exceeds its category limit is deleted rather than
reclassified into another category. A backup is skipped when disk usage
exceeds `disk_threshold` percent. See [CLI Reference](cli.md#backup-and-restore).

## Updates

```yaml
server:
  update:
    branch: "stable"
    auto_install: false
    defer_days: 0
    cli_min_version: ""
```

Valid branches are `stable`, `beta`, and `daily`. With `auto_install`
off — the default — the `update_check` task only notifies. `defer_days`
(0–365) makes the scheduled check ignore releases younger than that many
days; an explicit `shortner --update` bypasses the defer window.
`cli_min_version` is returned by
`https://csj.lol/api/autodiscover` and is empty by default, so no minimum CLI
version is enforced.

Client binary publishing options live in `yaml.d/maintenance.yml`:

```yaml
cli:
  binary_download:
    dir: "{data_dir}/cli"
    require_auth: false
```

The default directory is `{data_dir}/cli`. The files in it are operator
provisioned; the server verifies their SHA-256 values before advertising
them. Downloads are public unless `require_auth` is enabled.

## Security

```yaml
server:
  security:
    encryption_key: ""
    reports:
      retention_days: 365
    allowlist: []
    blocked_ips: []
    blocklists:
      enabled: true
      extra_sources: []
    abuse_detection:
      enabled: true
      request_flood:
        multiplier: 10
        block_duration: "1h"
      auto_block_ip: true
      auto_alert: true
```

`encryption_key` is generated on first run and is used to seal sensitive
records at rest. Closed security reports are removed after `retention_days`
by the daily `security_cleanup` task; open reports and researcher thanks
entries are retained. `allowlist` entries bypass blocking; `blocked_ips` are
permanent, config-file-only blocks. Entries with an invalid or dangerously
broad CIDR (an IPv4 prefix shorter than `/8`, or an IPv6 prefix shorter
than `/16`) are dropped with a warning. See [Security](security.md).

Allowlist and permanent block entries accept `cidr` and an optional
`description`; an omitted or empty description is stored as `(no description)`.

`blocklists` always includes the built-in `firehol_level1` IP feed. Add
operator feeds under `extra_sources`; each requires a unique lowercase
`id`, a `type` of `ip` or `domain`, an HTTPS `url`, and an optional
`enabled` flag (default true). Files are stored under
`{data_dir}/security/blocklists/`, validated before atomic replacement, and
kept at their last good copy when an update fails, and the check reports
degraded while serving that copy. Feeds older than 72 hours are also degraded
in health output. IP feeds exclude private, trusted,
loopback, link-local, and CGNAT ranges. Domain feeds match submitted URL and
email hosts plus the request Origin (or Referer when Origin is absent).

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
    disk_low:
      enabled: true
      threshold_percent: 85
      check_interval: 5m
      repeat: 24h
    database_error:
      enabled: true
      failures: 3
      check_interval: 1m
      repeat: 24h
```

`disk_low` checks the filesystems containing the data, log, and backup
directories every five minutes. It logs and emails when a volume first
crosses the threshold, repeats at most once per configured interval, and
logs recovery after usage falls five percentage points below the threshold.

`database_error` probes both read and write handles every minute. Three
consecutive failures produce a warning and an email when SMTP is available;
the alert repeats at most once per 24 hours while the outage continues.
Recovery is logged and resets the failure and repeat state. Probe errors are
redacted before logging or emailing.

`email.enabled` is set automatically from whether a working SMTP server
was found — it is not a manual toggle. With `host` empty, the server
auto-detects a relay at startup; with `host` set, it tests that server and
disables email if the test fails. **If no SMTP server is available, email
is simply off.** Nothing is queued, nothing is retried, and nothing is
logged as "would have sent". See
[Integrations](integrations.md#email-and-smtp).

The Web UI renders form feedback in server-generated pages and uses toast
feedback for clipboard actions. Core forms submit without JavaScript;
JavaScript enhances validation and presentation.

## Web Headers and Content Security Policy

```yaml
web:
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

The `Permissions-Policy` header is always emitted from the project's fixed
feature policy; it has no `server.yml` setting. `web.robots` controls the generated `robots.txt`.
`web.announcements.enabled` and `web.announcements.messages` control
server-rendered site banners. Messages with an active RFC 3339 `start` and
`end` window appear in configuration order; visitors can dismiss entries
marked `dismissible: true` without JavaScript. A whitespace-only
`server.branding.content.footer.body` removes the custom branding block;
the built-in application footer remains visible.

```yaml
web:
  announcements:
    enabled: true
    messages:
      - id: maintenance-2026-11
        type: warning
        title: Scheduled maintenance
        message: Service will be unavailable during the maintenance window.
        start: "2026-11-20T02:00:00Z"
        end: "2026-11-20T04:00:00Z"
        dismissible: true
```

Supported types are `info`, `warning`, `error`, and `success`. Dismissal is
stored in the `dismissed_announcements` browser cookie keyed by the message
ID; changing an ID shows the notice again.

## Well-Known and Discovery

```yaml
web:
  llms:
    enabled: true
    include_endpoints: true
    include_schemas: false
    custom_sections: []
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
    expires: ""
    keyservers:
      - "https://keys.openpgp.org"
    publish_pgp_key: true
    preferred_languages: "en"
    policy: ""
    thanks: []
```

Each `custom_sections` item has `title` and `content` fields. Titles are
1–80 characters, content is Markdown up to 4096 characters, and links may
use HTTPS or relative paths. HTML is escaped; invalid entries are skipped.

Public key publishing uses the keyserver-specific upload protocol. Failed
submissions retain per-server retry state in the data directory; the built-in
`keyserver_retry` task retries temporary failures every 15 minutes with
exponential backoff. Permanent client errors are kept for operator review
instead of being retried indefinitely.

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
| `server.security.reports.retention_days` at or below zero | `365` |
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
