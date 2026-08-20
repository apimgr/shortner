# Security

## Reporting a Vulnerability

Report security issues privately. Do not open a public issue.

- **GitHub Security Advisory** —
  <https://github.com/apimgr/shortner/security/advisories/new>
- **`/.well-known/security.txt`** — every running instance serves an
  RFC 9116 file listing its own contact address, policy URL, preferred
  languages, and expiry.
- **The instance's own form** — `/server/contact?security_id={id}` switches
  the contact form into security-report mode.

A running server also serves human-readable pages: `/server/security` for
reporting information, `/server/security/policy` for the coordinated
disclosure policy, and `/server/security/thanks` for researcher
acknowledgments.

Security reports are **sealed with AES-256-GCM before being written** to
the database, under the key in `server.security.encryption_key`. Report
bodies are never stored in plaintext.

Submitting a report returns a tracking ID and triggers two notifications —
one to the maintainer and one acknowledging the researcher. Those emails
are always a courtesy copy, never the primary channel: if email is
unavailable or the send fails, the submission still succeeds and the
tracking ID is still issued.

!!! note
    A status page for looking up a report by tracking ID
    (`/server/security/report/{tracking_id}`) is not implemented. The
    maintainer notification currently carries an inline AES-armored body
    rather than a PGP MIME attachment, and there is no CLI for generating
    or rotating the PGP keypair — `/.well-known/pgp-key.asc` serves a key
    if one has been placed on disk and returns `404` otherwise.

## The Public Endpoint Safety Principle

Anything reachable without authentication must be safe for anyone in the
world to read. Every field on a public endpoint is classified before it is
exposed:

| Tier | Rule | Examples |
|------|------|----------|
| **Tier 1** | Never public — not even in debug mode | Database credentials, internal IPs and hostnames, tokens, private keys, other users' data, filesystem paths, exact rate-limit values |
| **Tier 2** | Always safe to show | Version, uptime, generic health status, build commit |
| **Tier 3** | Public only when the privacy configuration permits | Optional operational detail |

This is why the health endpoint carries a version and uptime but never a
path or a hostname, and why error messages never name a file.

## Authorization Model

There are three roles and no user accounts.

| Role | Holds | Can do |
|------|-------|--------|
| Anonymous | nothing | Create links, resolve links, view any link's stats |
| Resource owner | that link's `owner_token` | Update or delete **that one link** |
| Operator | `server.token` from `server.yml` | Anything, on any link; access metrics |

The `owner_token` is returned exactly once, in the create response. There
is no account it belongs to and therefore no recovery path — losing it
means the link can no longer be edited or deleted by anyone but the
operator.

Tokens are stored as **SHA-256 hashes**. The raw value never reaches the
database or a log line. Comparisons are constant-time.

Configuration and backup passwords use **Argon2id**, not bcrypt. The two
uses are deliberately different: SHA-256 for high-entropy random tokens
that need fast lookup, Argon2id for human-chosen secrets that need to
resist offline cracking.

## Privacy of Click Data

The click record is designed so that the data you would not want to leak
is never collected in the first place.

- **IP addresses are anonymized before the write.** The last octet of an
  IPv4 address and the last 80 bits of an IPv6 address are zeroed. The
  full address is not stored anywhere, so no later breach can reveal it.
- **Bots are not counted.** Requests with a recognized bot user agent are
  excluded from click counts and analytics entirely.
- **GeoIP resolution is local.** Country and region come from a local
  MaxMind-format database. No third party is contacted, and no visitor
  data leaves the server.
- **No third-party analytics, no telemetry, no tracking scripts.**

Because there is no login, there is no session to hijack and no password
database to leak.

## Privacy Signals

The server honors browser privacy signals:

| Signal | Default | Setting |
|--------|:-------:|---------|
| `Sec-GPC` (Global Privacy Control) | honored | `web.headers.honor_sec_gpc` |
| `DNT` (Do Not Track) | not honored | `web.headers.honor_dnt` |

`Clear-Site-Data` is sent on token revocation and on consent withdrawal by
default.

## HTTP Security Headers

Applied to responses according to a per-response-type matrix.

### Content Security Policy

Built from a locked-down baseline. Each of the fourteen directives is
adjustable in two ways: an `_extra` key appends sources to the baseline,
and an `_override` key replaces the directive outright. Prefer `_extra`;
an override discards the baseline protections for that directive.

In development mode the policy downgrades to report-only unless
`web.csp.mode` was set explicitly, so a development session does not
silently ship a weaker policy to production.

Violations are posted by the browser to
`/api/v1/server/reports/csp`, which always answers `204` with an empty
body.

### HSTS

```yaml
web:
  hsts:
    enabled: true
    max_age_seconds: 63072000
    include_subdomains: true
    preload: true
```

Only sent over HTTPS. A `max_age_seconds` below one year is accepted but
warned about, since it makes the site ineligible for the preload list.

### Other headers

| Header | Default | Setting |
|--------|---------|---------|
| `Cross-Origin-Opener-Policy` | `unsafe-none` | `web.headers.coop` |
| `Cross-Origin-Embedder-Policy` | `unsafe-none` | `web.headers.coep` |
| `Cross-Origin-Resource-Policy` | `cross-origin` | `web.headers.corp` |
| `Origin-Agent-Cluster` | on | `web.headers.origin_agent_cluster` |
| `X-Permitted-Cross-Domain-Policies` | `none` | `web.headers.cross_domain_policies` |
| `X-DNS-Prefetch-Control` | omitted | `web.headers.dns_prefetch_control` |
| `Permissions-Policy` | 25 features locked down | `web.permissions_policy` |
| `NEL` | enabled, 30 days | `web.headers.nel` |
| `Server-Timing` | debug mode only | `web.headers.server_timing_in_debug_only` |

`Sec-Fetch-*` request headers are validated by default
(`web.headers.sec_fetch_validation`), rejecting cross-site requests that
claim a navigation context they should not have.

## CSRF

HTML form routes use a double-submit cookie. The token defaults to 32
bytes in a `csrf_token` cookie, echoed in an `X-CSRF-Token` header or a
form field. `GET` and `HEAD` are exempt, as are token-authenticated API
requests, which are not cookie-authenticated and therefore not vulnerable
to CSRF. Individual paths can be exempted with
`server.csrf.exempt_paths`.

The `secure` attribute defaults to `auto`, which sets the flag when the
request arrives over HTTPS.

## IP Blocking and Abuse Detection

Three mechanisms, evaluated in order:

1. **Allowlist** — `server.security.allowlist` entries bypass all blocking.
2. **Permanent blocks** — `server.security.blocked_ips`, configuration-file
   only, each with a reason.
3. **Temporary blocks** — issued automatically by abuse detection and
   released by the `ip_block_release` scheduler task, which sweeps every
   minute.

```yaml
server:
  security:
    abuse_detection:
      enabled: true
      request_flood:
        multiplier: 10
        block_duration: "1h"
      auto_block_ip: true
      auto_alert: true
```

A client exceeding the normal request rate by `multiplier` times is
temporarily blocked and raises a `security_alert` notification.

Entries with an invalid CIDR, or one broad enough to be an operational
hazard (an IPv4 prefix shorter than `/8`, an IPv6 prefix shorter than
`/16`), are dropped at load time with a warning rather than silently
applied.

## Rate Limiting

Per-IP sliding windows with separate counters for reads, writes, and
health checks, plus a global burst ceiling. Exceeding a limit returns
`429` with `Retry-After`. Defaults and tuning are in
[Configuration](configuration.md#rate-limiting).

Note that the exact configured limits are Tier 1 data and are never
disclosed on a public endpoint.

## The Well-Known Namespace

`/.well-known/` is **allowlist-only**. A path that is not explicitly
enabled returns `404` — never a redirect, never a catch-all handler, never
a directory listing. Only `GET` and `HEAD` are accepted.

| Path | Default |
|------|---------|
| `/.well-known/security.txt` | Always served |
| `/.well-known/pgp-key.asc` | Always routed; `404` when no key exists |
| `/robots.txt` | Always served, generated from `web.robots` |
| `/llms.txt` and `/.well-known/llms.txt` | Enabled |
| `/.well-known/webfinger` | Disabled |
| `/.well-known/openid-configuration` | Disabled |
| `/.well-known/assetlinks.json` | Disabled |
| `/.well-known/apple-app-site-association` | Disabled |
| `/.well-known/mta-sts.txt` | Disabled |

## Input Validation

Everything is validated on the server. Client-side validation is a
convenience only and is never the enforcement point.

- Destination URLs are parsed and validated before storage.
- Custom slugs must be 3–20 characters of alphanumerics and hyphens, and
  are checked against a reserved-name list so a link can never shadow
  `/server`, `/api`, `/static`, or another route.
- All database access uses parameterized queries.
- User-supplied HTML is sanitized before rendering.
- Archive extraction rejects path-traversal entries.

## Transport

Built-in ACME issuance supports the HTTP-01 and TLS-ALPN-01 challenges.

!!! warning
    DNS-01 is not implemented — the `dns_provider` and `dns_credentials`
    settings have no provider matrix behind them, and DNS credentials are
    not encrypted at rest. Use HTTP-01, TLS-ALPN-01, or terminate TLS at a
    reverse proxy.

## Backup Security

Encrypted backups use AES-256-GCM with a key derived by Argon2id from the
operator's password. The salt and nonce are stored in the clear ahead of
the ciphertext, as their design requires. The password is only ever read
from an interactive prompt — never a flag — so it cannot leak through
shell history or a process listing.

Enabling `server.compliance.enabled` forces encryption; an unencrypted
backup is refused while it is on.

Every archive is verified immediately after creation, and any archive that
fails verification is deleted rather than left on disk. Restore is
authorized by context: freely into an empty database, with a confirmation
as root, with the operator token as the service user, and denied
otherwise.

## Not Implemented

State these plainly rather than assuming they are covered:

- **Tor hidden service and I2P eepsite support.** Neither is built. The
  health endpoint reports both as disabled with `provider: none`, and the
  `tor_health` and `i2p_health` scheduler tasks skip with a logged reason.
- **IP and domain blocklist feeds** (`blocklist_update`) and **CVE
  database integration** (`cve_update`). Both tasks are registered and
  both skip.
- **PGP keypair management.** No CLI for generation or rotation.
- **Security report status lookup** by tracking ID.

## Operational Recommendations

- Keep the operator token out of shell history and environment dumps; it
  is a root-equivalent credential.
- Put the instance behind access control if the public `/list` page and
  `GET /api/v1/links` endpoint — which expose every link and destination —
  are unacceptable for your deployment.
- Configure `server.trusted_proxies.additional` when running behind a
  proxy, or every request will be attributed to the proxy's IP and rate
  limiting will be ineffective.
- Set a backup encryption password and verify that restores work before
  you need one.
- Run `shortner --update check` regularly, or leave the `update_check`
  task enabled so you are notified.
