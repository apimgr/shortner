# Development Guide

This project is spec-driven. `AI.md` in the repository root is the
authoritative specification and is **read-only** — never edit it. Project
specifics (name, org, business logic) live in `IDEA.md`, the outstanding
work list lives in `TODO.AI.md`, and `CLAUDE.md` plus `.claude/rules/*.md`
are condensed quick references generated from `AI.md`.

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

All six `make` targets run inside `casjaysdev/go:latest` with
`CGO_ENABLED=0` and `GOFLAGS=-buildvcs=false`:

| Target | Description |
|--------|-------------|
| `make dev` | Quick build to a temporary directory for iteration |
| `make local` | Build a binary for the local OS/arch into `binaries/` |
| `make build` | Build all 8 target platforms into `binaries/` |
| `make test` | Run the Go test suite with the 60% coverage gate |
| `make release` | Build and package release artifacts into `releases/` |
| `make docker` | Build the container image from `docker/Dockerfile` |
| `make clean` | Remove `binaries/` and `releases/` |

Target platforms are `linux`, `darwin`, `windows`, and `freebsd`, each on
`amd64` and `arm64` — eight binaries, all statically linked with no CGO.

Build metadata is embedded via `-ldflags` into
`src/common/version`: `Version` (from `release.txt`, else `$VERSION`, else
`devel`), `CommitID`, `BuildEpoch`, and `OfficialSite` (from `site.txt`).

### Building without Make

The Makefile is a local convenience only. The equivalent explicit command,
which is what CI uses:

```bash
docker run --rm --name shortner-build \
  -v "$PWD:/app" -w /app \
  -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
  casjaysdev/go:latest \
  go build -o binaries/shortner ./src
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
writes `server.yml` with defaults, generates the operator token, and prints
the startup banner. See [Configuration](configuration.md) for where those
files land on each platform.

To run against a throwaway directory instead of the system paths:

```bash
CONFIG_DIR=/tmp/shortner/config DATA_DIR=/tmp/shortner/data \
  ./binaries/shortner --debug --port 8080
```

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
| `tests/run_tests.sh` | Two-phase entry point: runs `make test`, then auto-detects Incus (falls back to Docker) and dispatches |
| `tests/docker.sh` | Builds in `casjaysdev/go:latest`, runs the suite inside a disposable `alpine:latest` container |
| `tests/incus.sh` | Same suite inside `images:debian/trixie`, plus a real systemd service install/uninstall lifecycle |
| `tests/suite.sh` | The shared in-container suite (binary info, first run, health, content negotiation, well-known, frontend pages, link CRUD, redirect/410/404, stats, token authorization, the `shortner-cli` client, rate limiting) |
| `tests/test_content_negotiation.sh` | Standalone negotiation matrix against any running instance |
| `tests/e2e.sh` + `tests/e2e/` | chromedp browser suite behind the `e2e` build tag; standalone, never invoked by `run_tests.sh` |

`tests/assert.sh` and `tests/common.sh` hold shared helpers. All runtime
and test data goes to `${TMPDIR:-/tmp}/apimgr/shortner-XXXXXX/` — never the
project directory.

The e2e suite is tagged so `go test ./...` still builds without a browser:

```bash
bash tests/e2e.sh
```

## Linting

```bash
docker run --rm -v "$PWD:/app" -w /app \
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
| `beta.yml` | push to `beta` | Prerelease build |
| `daily.yml` | 3am UTC cron and push to the default branch | Rolling `daily` tag, deleted and recreated each run |
| `docker.yml` | push / schedule / dispatch | Multi-arch (`linux/amd64`, `linux/arm64`) images pushed to `ghcr.io` |

Two rules are non-negotiable in CI:

- **Never call `make`** from a workflow — use explicit commands.
- **Pin every third-party Action to a full commit SHA**, never a tag. The
  `workflow-policy` job greps for tag-pinned actions and fails the build if
  it finds any.

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
  files. Open work belongs in `TODO.AI.md`.
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
