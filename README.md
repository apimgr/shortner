# Shortner

[![License](https://img.shields.io/github/license/apimgr/shortner)](LICENSE.md)
[![Release](https://img.shields.io/github/v/release/apimgr/shortner)](https://github.com/apimgr/shortner/releases)

## About

Shortner is a self-hosted URL shortener with an API and web UI. Anyone can
create a short link with no account required; each link gets a one-time
owner token for managing it later, and every click is recorded with the
originating IP anonymized for privacy-respecting analytics.

## Official Site

https://shortner.example.com

## Features

- Anonymous link creation — no account required
- Owner tokens for managing links after creation
- Custom slugs and link expiration (expired links return `410 Gone`)
- Click analytics with IP anonymization
- Bot-exclusion for click analytics
- Server-side rendered web UI — works fully without JavaScript
- REST API mirrors every web UI feature
- Single static binary, no runtime dependencies

Planned (not yet shipped): GeoIP enrichment for click analytics, Tor hidden
service support (auto-enabled when Tor is found), and an optional I2P
eepsite (off by default).

## Production

### Binary

Download the latest release from [GitHub Releases](https://github.com/apimgr/shortner/releases/latest).

#### Linux

| Arch | Binary |
|------|--------|
| amd64 | `shortner-linux-amd64` |
| arm64 | `shortner-linux-arm64` |

```bash
curl -LSsf https://github.com/apimgr/shortner/releases/latest/download/shortner-linux-amd64 \
  -o /usr/local/bin/shortner && chmod +x /usr/local/bin/shortner
```

#### macOS

| Arch | Binary |
|------|--------|
| Intel (x86_64) | `shortner-darwin-amd64` |
| Apple Silicon (arm64) | `shortner-darwin-arm64` |

```bash
# Detect arch automatically
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -LSsf "https://github.com/apimgr/shortner/releases/latest/download/shortner-darwin-${ARCH}" \
  -o /usr/local/bin/shortner && chmod +x /usr/local/bin/shortner
# Remove macOS quarantine flag
xattr -d com.apple.quarantine /usr/local/bin/shortner 2>/dev/null || true
```

#### Windows

| Arch | Binary |
|------|--------|
| amd64 | `shortner-windows-amd64.exe` |
| arm64 | `shortner-windows-arm64.exe` |

Download and add to `%PATH%`.

#### FreeBSD

| Arch | Binary |
|------|--------|
| amd64 | `shortner-freebsd-amd64` |
| arm64 | `shortner-freebsd-arm64` |

### Docker

```bash
cd docker
docker compose up -d
```

This starts `shortner` (port `172.17.0.1:64580`) plus a `shortner-cache`
(Valkey) sidecar. Config and data persist in `./volumes/config` and
`./volumes/data`. Override `PORT`, `TZ`, or `CACHE_URL` via environment
variables in `docker-compose.yml`.

## Client

`shortner-cli` is the companion CLI/TUI client for managing links and
server settings from the command line.

```bash
curl -LSsf https://github.com/apimgr/shortner/releases/latest/download/shortner-cli-linux-amd64 \
  -o /usr/local/bin/shortner-cli && chmod +x /usr/local/bin/shortner-cli
```

| Flag | Description |
|------|-------------|
| `--server` | server base URL |
| `--token` | owner/API token |
| `--output` | output format (`table`, `json`, etc.) |

## Configuration

Shortner auto-creates `server.yml` with sane defaults on first run. Key
flags (see `shortner --help`):

| Flag | Description |
|------|-------------|
| `--config` | path to `server.yml` |
| `--address` | bind address |
| `--port` | bind port |
| `--baseurl` | public base URL used for generated short links |
| `--mode` | `production` (default) or `development` |
| `--debug` | verbose debug output |

### Environment variables

Every variable below is read by the server binary. CLI flags always win
over environment variables, which win over the built-in defaults.

| Variable | Description |
|----------|-------------|
| `CONFIG_DIR` | config directory (holds `server.yml`) |
| `DATA_DIR` | data directory |
| `LOG_DIR` | log directory |
| `DATABASE_DIR` | database directory (defaults inside the data directory) |
| `BACKUP_DIR` | backup directory |
| `LISTEN` | bind address (same as `--address`) |
| `PORT` | bind port (same as `--port`) |
| `DOMAIN` | public FQDN used for URL and certificate resolution |
| `HOSTNAME` | fallback hostname when `DOMAIN` is unset |
| `MODE` | `production` (default), `development`, or `debug` (defaults debug output on) |
| `DEBUG` | truthy value enables debug output (wins over `MODE=debug`) |
| `CONTAINER` | truthy value forces container path layout (`/config`, `/data`) |
| `TZ` | timezone used for user-facing timestamps |
| `NO_COLOR` | any non-empty value disables ANSI color output |
| `TERM` | `dumb` disables ANSI output and animated spinners |

## API

The REST API is the source of truth; the web UI consumes it. Successful
JSON responses use the `{"ok": true, "data": {…}}` envelope, errors use
`{"ok": false, "error": …, "message": …}`, and the health endpoints return
their health object directly.

| Endpoint | Description |
|----------|-------------|
| `POST https://shortner.example.com/api/v1/links` | create a short link |
| `GET https://shortner.example.com/{slug}` | redirect to the target URL |
| `GET https://shortner.example.com/{slug}/stats` | click analytics for a link |
| `GET https://shortner.example.com/server/healthz` | health check |

## Other

Issues and feature requests: [GitHub Issues](https://github.com/apimgr/shortner/issues).

## Development

Prerequisites: Docker (all builds run in `casjaysdev/go:latest`, never on
the host).

| Target | Description |
|--------|-------------|
| `make dev` | run in development mode with hot config |
| `make local` | build a binary for the local OS/arch |
| `make build` | build binaries for all 8 target platforms |
| `make test` | run the test suite with coverage gate |
| `make release` | build and package a release |
| `make docker` | build the Docker image |

```bash
make test
```

### Docker build

```bash
docker buildx build -f docker/Dockerfile -t shortner:local .
```

## Disclaimer

This software is provided "as is" without warranty of any kind. Use at your own risk.

- **No Warranty**: The authors are not responsible for any damages, data loss, or issues arising from use of this software
- **Not Professional Advice**: This software does not constitute legal, financial, medical, or other professional advice
- **Third-Party Services**: If this software connects to external APIs or services, their terms of service apply separately
- **Security**: While we strive to follow security best practices, no software is guaranteed to be free of vulnerabilities
- **Production Use**: Evaluate thoroughly before deploying in production environments

By using this software, you acknowledge that you have read and understood this disclaimer.

## License

MIT — see [LICENSE.md](LICENSE.md)
