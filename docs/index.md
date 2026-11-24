# shortner

Self-hosted URL shortener with an API, a JavaScript-optional web
interface, and privacy-preserving per-link click analytics.

`shortner` is a single static Go binary with no runtime dependencies. It
ships its own HTTP server, SQLite database, task scheduler, metrics
exporter, backup system, GeoIP lookup, email notifier, and self-updater.
Point it at a directory and run it — it creates its own configuration,
generates its own operator token, and starts serving.

## What It Does

- Shortens a long URL to a 6-character code, or to a custom slug of 3–20
  alphanumeric characters and hyphens.
- Redirects `/{slug}` to the destination, recording an anonymized click.
- Serves per-link click analytics at `/{slug}/stats` — totals, referrers, a
  daily time series, and recent clicks with GeoIP country/region.
- Exposes the same functionality over a REST API under `/api/v1/links` and
  over server-rendered HTML pages.
- Returns `410 Gone` for an expired link and `404 Not Found` for one that
  never existed.

## Design Principles

**No accounts.** There is no registration, no login, and no password
reset. Anyone can create a link. Links belong to an anonymous owner with a
primary token; linked tokens can be made for integrations. The primary
token can be restored on a new browser and rotated without changing link
ownership. The operator token in the private `secrets.yml` grants global
authority.

**Privacy by default.** Client IP addresses are anonymized *before* they
are written — the last octet of an IPv4 address and the last 80 bits of an
IPv6 address are zeroed. The full address is never persisted. Requests
from known bot user agents are excluded from click counts entirely.

**Works without JavaScript.** Every feature is reachable through
server-rendered HTML forms. JavaScript enhances the interface but is never
required for anything.

**Everything is public-safe or authenticated.** Any endpoint reachable
without a token has been classified against a three-tier safety model:
Tier 1 data (tokens, credentials, filesystem paths, internal hostnames) is
never exposed, not even in debug mode.

**No paywalls.** Every feature is available in every build. There are no
tiers, no license keys, and no telemetry.

## Getting Started

```bash
shortner
```

That is the whole first run. The binary creates its configuration and data
directories, writes `server.yml` and private `secrets.yml`, generates an
operator token, opens the database, and starts listening on port `8090`.

Then create a link:

```bash
curl -X POST http://localhost:8090/api/v1/links \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://example.com/a/very/long/path"}'
```

Continue with [Installation](installation.md) for platform-specific setup,
or [Configuration](configuration.md) for the full settings reference.

## Documentation Map

| Page | Covers |
|------|--------|
| [Installation](installation.md) | Binaries, Docker, service install, filesystem paths |
| [Configuration](configuration.md) | `server.yml`, environment variables, modes, validation |
| [API Reference](api.md) | REST endpoints, authentication, response shapes, rate limits |
| [CLI Reference](cli.md) | Server binary flags and subcommands |
| [Security](security.md) | Threat model, headers, vulnerability reporting, privacy |
| [Integrations](integrations.md) | Metrics, GeoIP, SMTP, reverse proxies, well-known files |
| [Translating](translating.md) | Locale catalog format, placeholders, validation, and contribution workflow |
| [Development](development.md) | Building, testing, contributing |

## Project Status

Substantial parts of the system are implemented and exercised by the test
suite: configuration, path resolution, the link API and web interface, the
HTTP security layer, the scheduler, GeoIP, metrics, email notifications,
backup and restore, the self-updater, service installation, and CI/CD.

The server includes server-rendered Swagger and GraphQL documentation pages,
an OpenAPI JSON endpoint, a read-only GraphQL endpoint, API mirrors for
server pages, and an MCP endpoint with tools derived from the OpenAPI
contract. The MCP guide is available at `/server/help/mcp`. API route and
schema coverage continues to be tracked against the full API contract.
Notification delivery and rate-limit cache metrics are implemented. The server also
publishes CLI update metadata and serves colocated client binaries. The PWA
includes install and update prompts, offline status and fallback, a
versioned cache, and its required icon assets.
Browser Background Sync and browser geolocation need product-specific behavior
defined in the parent API template before implementation; these are distinct
from preference sync at `/server/preferences/sync`. The optional
native GUI uses the pure-Go `gogpu/ui` toolkit on Linux, macOS, and Windows;
BSD uses the TUI. See the reference pages and project backlog for current
behavior.

## License

MIT.
