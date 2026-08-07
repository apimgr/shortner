# Shortner

Shortner is a self-hosted URL shortener with an API and web UI. Anyone can
create a short link with no account required; each link gets a one-time
owner token for managing it later, and every click is recorded with the
originating IP anonymized for privacy-respecting analytics.

🌐 **Site:** https://shortner.example.com

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE.md)
[![Release](https://img.shields.io/github/v/release/apimgr/shortner)](https://github.com/apimgr/shortner/releases)

---

## 📦 Install

Download the latest release from [GitHub Releases](https://github.com/apimgr/shortner/releases/latest).

### Linux
| Arch | Binary |
|------|--------|
| amd64 | `shortner-linux-amd64` |
| arm64 | `shortner-linux-arm64` |

```bash
curl -LSsf https://github.com/apimgr/shortner/releases/latest/download/shortner-linux-amd64 \
  -o /usr/local/bin/shortner && chmod +x /usr/local/bin/shortner
```

### macOS
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

### Windows
| Arch | Binary |
|------|--------|
| amd64 | `shortner-windows-amd64.exe` |
| arm64 | `shortner-windows-arm64.exe` |

Download and add to `%PATH%`.

### FreeBSD
| Arch | Binary |
|------|--------|
| amd64 | `shortner-freebsd-amd64` |
| arm64 | `shortner-freebsd-arm64` |

---

## 🐳 Docker

```bash
cd docker
docker compose up -d
```

This starts `shortner` (port `172.17.0.1:64580`) plus a `shortner-cache`
(Valkey) sidecar. Config and data persist in `./volumes/config` and
`./volumes/data`. Override `PORT`, `TZ`, or `CACHE_URL` via environment
variables in `docker-compose.yml`.

---

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

---

## 🛠️ Development

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

### 🐳 Docker build

```bash
docker buildx build -f docker/Dockerfile -t shortner:local .
```

---

## 📄 License

MIT — see [LICENSE.md](LICENSE.md)
