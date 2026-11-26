# Development Guide

This project is spec-driven. `AI.md` in the repository root is the
authoritative specification and is **read-only** — never edit it. Project
specifics (name, org, business logic) live in `IDEA.md`. A `TODO.AI.md` is
used when the project has a multi-item implementation backlog. `CLAUDE.md`
and `.claude/rules/*.md` are condensed quick references to `AI.md`.

## Prerequisites

- **Docker** — every build, test, and lint run happens inside a container.
  Never build on the host.
- **Make** — local development only. CI/CD never invokes `make`; the
  workflows use explicit `go` commands.
- **Go** — supplied by the `casjaysdev/go:latest` toolchain image. The
  module targets the latest stable Go (`go 1.26` in `go.mod`); no Go
  installation is required on the host.

## Repository Layout

| Path | Contents |
|------|----------|
| `src/` | All Go source. `src/main.go` is the server entry point. |
| `src/client/` | The `shortner-cli` client binary: `main.go`, `cmd/`, `config/`, `paths/`, `api/`, `tui/`, `setup/`. |
| `src/server/template/`, `src/server/static/` | Embedded HTML templates, CSS, JS, and email templates. |
| `tests/` | Shell test entry points and the chromedp browser suite. |
| `docker/` | `Dockerfile`, `Dockerfile.dev`, compose files, and the runtime rootfs overlay. |
| `docs/` | This ReadTheDocs documentation set — nothing else belongs here. |
| `scripts/` | Helper scripts. |
| `binaries/`, `releases/` | Build output (git-ignored). |

Key packages under `src/`:

| Package | Responsibility |
|---------|----------------|
| `config` | `server.yml` schema, defaults, load/save, validation, env overrides |
| `paths` | Per-platform path resolution, privileged/unprivileged/container detection |
| `mode` | `production`/`development` and debug resolution |
| `db` | SQLite schema and CRUD (links, clicks, API tokens, scheduler, security reports) |
| `httpserver` | Router, middleware, handlers, security headers, well-known files |
| `security` | Token generation/hashing, Argon2id, IP anonymization, slug/short-code generation, bot detection, AES-GCM sealing |
| `apperr` | Canonical error envelope and HTTP status mapping |
| `applog` | Log formatters, file logger, audit logger, in-memory ring buffer |
| `notify` | Email templates, SMTP detection, RFC 5322 message sending |
| `scheduler` | Internal cron-equivalent task scheduler |
| `geoip` | Offline GeoIP lookup and database updates |
| `metrics` | Prometheus registry and instrumented collectors |
| `backup` | Encrypted archive create/verify/restore and retention |
| `updater` | Self-update over GitHub Releases |
| `service` | Privilege escalation, system account, init-system integration |
| `certmgr`, `fqdn` | Certificate lookup/ACME and FQDN resolution |

## Build

```bash
git clone https://github.com/apimgr/shortner
cd shortner
make local
```

All build and test `make` targets run inside `casjaysdev/go:latest` with
`CGO_ENABLED=0` and `GOFLAGS=-buildvcs=false`:

| Target | Description |
|--------|-------------|
| `make dev` | Quick build to a temporary directory for iteration |
| `make local` | Build a binary for the local OS/arch into `binaries/` |
| `make build` | Build all 8 target platforms into `binaries/` |
| `make test` | Check version precedence, then run the Go test suite with the 60% coverage gate |
| `make release` | Build and package stable release artifacts into `releases/` |
| `make beta` | Push a timestamped `YYYYMMDDHHMMSS-beta` tag from a clean `main` branch |
| `make docker` | Build the container image from `docker/Dockerfile` |
| `make clean` | Remove `binaries/` and `releases/` |

Target platforms are `linux`, `darwin`, `windows`, and `freebsd`, each on
`amd64` and `arm64` — eight binaries, all statically linked with no CGO.

Build metadata is embedded via `-ldflags` into
`src/common/version`: local Makefile builds use `$VERSION`, then a non-empty
`release.txt`, then the seven-character commit ID (or `devel` outside git).
CI never reads `release.txt` or `$VERSION`: devel builds use the seven-character
commit ID, beta builds use a UTC timestamp plus `-beta`, and stable releases
use the pushed tag with the leading `v` removed. `CommitID`, `BuildEpoch`, and
`OfficialSite` (from `site.txt`) are also embedded.

### Building without Make

The Makefile is a local convenience only. The equivalent explicit command,
which is what CI uses:

```bash
PROJECT_NAME=shortner
GO_CACHE="${GO_CACHE:-$HOME/go/pkg/mod}"
GO_BUILD="${GO_BUILD:-$HOME/.cache/go-build/$PROJECT_NAME}"
mkdir -p "$GO_CACHE" "$GO_BUILD"
docker run --rm \
  --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
  -v "$PWD:/app" \
  -v "$GO_CACHE:/usr/local/share/go/pkg/mod" \
  -v "$GO_BUILD:/usr/local/share/go/cache" \
  -w /app \
  -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
  casjaysdev/go:latest \
  go build -buildvcs=false -trimpath -o binaries/shortner ./src
```

### Docker image

```bash
docker buildx build -f docker/Dockerfile -t shortner:local .
```

The image is a multi-stage build: `casjaysdev/go:latest` compiles the
binary, `alpine:latest` runs it under `tini` as PID 1 with
`STOPSIGNAL SIGRTMIN+3` and a `HEALTHCHECK` that calls
`shortner --status`.

## Run Locally

```bash
./binaries/shortner --debug --mode development --port 8080
```

On first run the binary creates its configuration and data directories,
writes `server.yml` and private `secrets.yml`, generates the operator token,
and prints the startup banner. See [Configuration](configuration.md) for
where those files land on each platform.

To run against a throwaway directory instead of the system paths:

```bash
mkdir -p "${TMPDIR:-/tmp}/apimgr"
TEMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/apimgr/shortner-XXXXXX")
mkdir -p "$TEMP_DIR/config" "$TEMP_DIR/data"
CONFIG_DIR="$TEMP_DIR/config" DATA_DIR="$TEMP_DIR/data" \
  ./binaries/shortner --debug --port 8080
```

Remove only the printed `TEMP_DIR` after stopping the server.

## Testing

```bash
make test
```

This runs `go test -v -cover ./...` inside the toolchain container and
**fails the build if total coverage drops below 60%**. Coverage profiles
are written to a temporary directory, never into the project tree.

The shell entry points in `tests/` layer integration testing on top:

| Script | Purpose |
|--------|---------|
| `tests/run_tests.sh` | Docker-only basic suite; runs `tests/docker.sh` and never falls back to Incus |
| `tests/docker.sh` | Builds in `casjaysdev/go:latest`, checks the project runtime image's required tools, Tor and I2P status, then runs the suite inside a disposable `alpine:latest` container |
| `tests/incus.sh` | Same suite inside `images:debian/trixie`, plus a real systemd service install/uninstall lifecycle |
| `tests/suite.sh` | The shared in-container suite (binary info, first run, health, content negotiation, well-known, frontend pages, link CRUD, redirect/410/404, stats, token authorization, the `shortner-cli` client, rate limiting) |
| `tests/test_content_negotiation.sh` | Standalone negotiation matrix against any running instance |
| `tests/e2e.sh` + `tests/e2e/` | chromedp browser suite behind the `e2e` build tag; standalone, never invoked by `run_tests.sh` |

`tests/assert.sh` and `tests/common.sh` hold shared helpers. All runtime
and test data goes to `${TMPDIR:-/tmp}/apimgr/shortner-XXXXXX/` — never the
project directory.

`tests/incus.sh` is the explicit Incus full-app suite. The e2e suite is tagged so `go test ./...` still builds without a browser:

```bash
bash tests/e2e.sh
```

## Linting

```bash
PROJECT_NAME=shortner
GO_CACHE="${GO_CACHE:-$HOME/go/pkg/mod}"
GO_BUILD="${GO_BUILD:-$HOME/.cache/go-build/$PROJECT_NAME}"
mkdir -p "$GO_CACHE" "$GO_BUILD"
docker run --rm \
  --name "${PROJECT_NAME}-$(tr -dc 'a-z0-9' </dev/urandom | head -c8)" \
  -v "$PWD:/app" \
  -v "$GO_CACHE:/usr/local/share/go/pkg/mod" \
  -v "$GO_BUILD:/usr/local/share/go/cache" \
  -w /app \
  -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
  casjaysdev/go:latest sh -c 'gofmt -l ./src && go vet ./... && staticcheck ./...'
```

`gofmt -l` must print nothing, and `go vet` and `staticcheck` must both be
clean. The same checks run as the `lint` job in `.github/workflows/ci.yml`.

## Continuous Integration

Workflows live in `.github/workflows/`:

| Workflow | Trigger | Jobs |
|----------|---------|------|
| `ci.yml` | push / PR, plus a weekly Monday cron | `lint`, `secret-scan`, `workflow-policy`, `test` (60% gate), `build`, `vuln-scan`, `image-scan` |
| `release.yml` | tag push (`v*`) | 8-platform build matrix, SBOM, sha256/sha512 checksums, build provenance attestation, GitHub Release |
| `beta.yml` | timestamped `*-beta` tag push | Prerelease build |
| `devel.yml` | 3am UTC cron and push to the default branch | Rolling `devel` tag, deleted and recreated each run |
| `docker.yml` | main push / version or beta tag / schedule / dispatch | Multi-arch (`linux/amd64`, `linux/arm64`) images pushed to `ghcr.io` |

Equivalent CI/CD pipelines are maintained for GitHub Actions, GitLab CI,
Gitea Actions, Forgejo Actions, and Jenkins. Each provider runs explicit
commands rather than Make targets and includes build, test, lint, coverage,
secret and vulnerability scans, stable and beta releases, devel builds, and
container image publishing. Third-party CI images and GitHub Actions are
pinned to immutable digests or full commit SHAs; the maintained
`casjaysdev/go:latest` toolchain image is the documented exception.

The beta workflow is started by a timestamped `YYYYMMDDHHMMSS-beta` tag. The
`make beta` target creates and pushes that tag from a clean `main` branch.

## Code Style

- Standard Go formatting (`gofmt`); tabs come from `gofmt`, everything else
  in the repo uses spaces.
- Go directory names are singular (`handler/`, `model/`), matching package
  names; tooling directories (`scripts/`, `tests/`) stay plural.
- Comments go **above** the line they describe, never inline, and never in
  pure data formats such as JSON or `KEY=VALUE` env files. YAML comments
  are the same — above the setting.
- Every text file ends with exactly one trailing newline.
- No `TODO`/`FIXME`/`HACK` markers and no commented-out code in committed
  files. Multi-item implementation backlogs belong in `TODO.AI.md`.
- No CGO, ever. `CGO_ENABLED=0` in every build path, which is why the
  database driver is pure-Go `modernc.org/sqlite`.
- Server-side rendering only. No React, Vue, or any client-side framework;
  JavaScript may enhance a page but must never be required for a feature.
- Validate on the server for everything. Client-side validation is a
  convenience layer, never the enforcement point.

## Contributing

1. Fork the repository.
2. Create a feature branch.
3. Read the relevant `AI.md` PART before implementing — the spec is the
   source of truth, and guessing at behavior it already defines is a bug.
4. Make your changes, including tests that fail before and pass after.
5. Run `make test` and the lint commands above; both must be clean.
6. Update `docs/` if your change affects operators, integrators, or end
   users. If a feature exists in code and is user-visible, it belongs here.
7. Submit a pull request.

## License

MIT — see
[LICENSE.md](https://github.com/apimgr/shortner/blob/main/LICENSE.md).
