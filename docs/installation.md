# Installation

`shortner` is distributed as a single static binary with no runtime
dependencies. There is nothing to install alongside it — no database
server, no interpreter, no shared libraries.

## Supported Platforms

Binaries are published for eight targets:

| OS | Architectures |
|----|---------------|
| Linux | `amd64`, `arm64` |
| macOS (Darwin) | `amd64`, `arm64` |
| Windows | `amd64`, `arm64` |
| FreeBSD | `amd64`, `arm64` |

All builds are `CGO_ENABLED=0` and statically linked, including the SQLite
driver (`modernc.org/sqlite`, pure Go).

## Download a Release

Releases are published on GitHub with SHA-256 and SHA-512 checksums, an
SBOM, and build provenance attestation.

```bash
curl -fsSLo shortner \
  https://github.com/apimgr/shortner/releases/latest/download/shortner-linux-amd64
curl -fsSLo sha256.txt \
  https://github.com/apimgr/shortner/releases/latest/download/sha256.txt
grep -- shortner-linux-amd64 sha256.txt | sha256sum -c -
chmod +x shortner
sudo mv shortner /usr/local/bin/shortner
```

Verify the checksum before running the binary. The built-in updater
(`shortner --update`) performs the same SHA-256 verification automatically
and refuses to install an artifact whose checksum does not match.

## First Run

```bash
shortner
```

On first run the binary:

1. Resolves its configuration, data, cache, log, and backup directories
   for the current OS and privilege level, and creates any that are
   missing.
2. Writes `server.yml` populated with defaults.
3. Generates the operator API token and the at-rest encryption key, then
   saves the config.
4. Opens (and creates, if needed) the SQLite database.
5. Starts the scheduler, registers its built-in tasks, and begins any
   enabled background work such as the GeoIP download.
6. Prints the startup banner and begins serving on `0.0.0.0:8090`.

The operator token is written into `server.yml`. Read it from there:

```bash
grep -- '^  token:' /etc/apimgr/shortner/server.yml
```

Treat that token as a root credential — it grants global authority over
every link and over the metrics endpoints.

## Filesystem Paths

Paths are resolved at runtime from the OS and whether the process is
privileged. Nothing is hardcoded at build time.

### Linux — running as root

| Purpose | Path |
|---------|------|
| Config | `/etc/apimgr/shortner/server.yml` |
| Data | `/var/lib/apimgr/shortner` |
| Database | `/var/lib/apimgr/shortner/db` |
| Cache | `/var/cache/apimgr/shortner` |
| Logs | `/var/log/apimgr/shortner` |
| Backups | `/mnt/Backups/apimgr/shortner` |
| TLS | `/etc/apimgr/shortner/ssl` |
| PID file | `/var/run/apimgr/shortner.pid` |

### Linux — running as an unprivileged user

| Purpose | Path |
|---------|------|
| Config | `~/.config/apimgr/shortner/server.yml` |
| Data | `~/.local/share/apimgr/shortner` |
| Cache | `~/.cache/apimgr/shortner` |
| Logs | `~/.local/log/apimgr/shortner` |
| Backups | `~/.local/share/Backups/apimgr/shortner` |

### macOS

Privileged installs live under `/Library/Application Support/apimgr/shortner`
with logs in `/Library/Logs/apimgr/shortner`. Unprivileged installs use the
same layout under `~/Library`.

### FreeBSD / OpenBSD / NetBSD

Privileged: config in `/usr/local/etc/apimgr/shortner`, data in
`/var/db/apimgr/shortner`, logs in `/var/log/apimgr/shortner`, backups in
`/var/backups/apimgr/shortner`. Unprivileged installs fall back to the
Linux unprivileged layout.

### Windows

Privileged (Administrator) installs use `%ProgramData%\apimgr\shortner`.
Unprivileged installs use `%AppData%\apimgr\shortner` for configuration
and `%LocalAppData%\apimgr\shortner` for data, cache, and logs.

### Containers

When the binary detects that it is running inside a container it uses a
flattened layout instead:

| Purpose | Path |
|---------|------|
| Config | `/config/shortner/server.yml` |
| Data | `/data/shortner` |
| Database | `/data/db/sqlite` |
| Logs | `/data/log/shortner` |
| Backups | `/data/backups/shortner` |

Every path can be overridden individually — see
[Configuration](configuration.md#directory-overrides).

## Docker

The published image runs `tini` as PID 1, uses `STOPSIGNAL SIGRTMIN+3`,
and has a `HEALTHCHECK` that calls `shortner --status`.

```bash
docker run -d --rm \
  --name shortner-app \
  -p 8080:80 \
  -e PORT=80 \
  -v "$PWD/config:/config:z" \
  -v "$PWD/data:/data:z" \
  ghcr.io/apimgr/shortner:latest
```

The binary — not the entrypoint script — creates directories, fixes
permissions, and drops privileges. The entrypoint sets environment
variables, execs the binary, and forwards signals; nothing more.

### Compose

Three compose files ship in `docker/`:

| File | Purpose |
|------|---------|
| `docker/docker-compose.yml` | Production |
| `docker/docker-compose.dev.yml` | Development |
| `docker/docker-compose.test.yml` | Ephemeral test runs |

```bash
docker compose -f docker/docker-compose.yml up -d
```

The production compose file mounts `./volumes/config` and `./volumes/data`
and publishes the container's port 80.

!!! note
    The production compose file also defines a Valkey sidecar and sets
    `CACHE_URL`. The Go code does not read `CACHE_URL` today — the cache
    package is present but not wired into the request path — so the sidecar
    is currently inert. Remove it if you do not want the extra container.

## Installing as a System Service

The binary installs and manages its own service. It detects the init
system, renders the correct unit or script, creates a dedicated system
user, enables the service, and starts it:

```bash
sudo shortner --service --install
```

Supported init systems:

| Init system | Installed to |
|-------------|--------------|
| systemd | `/etc/systemd/system/shortner.service` |
| OpenRC | `/etc/init.d/shortner` |
| SysVinit | `/etc/init.d/shortner` |
| runit | `/etc/sv/shortner/run` and `/etc/sv/shortner/log/run` |
| rc.d (BSD) | `/etc/rc.d/shortner` |
| launchd (macOS) | `/Library/LaunchDaemons/` or `~/Library/LaunchAgents/` |
| Windows SCM | Registered as a service under a Virtual Service Account |

SysVinit is selected only when neither `systemctl` nor `openrc-run` is
present.

### The service account

A dedicated `shortner` user and group are created with a matching UID and
GID, chosen by walking down from the top of the OS-safe range and skipping
reserved IDs. The account gets a no-login shell and no password, and its
home directory is the configuration directory. A `.service-account` marker
records that this binary created the account, so uninstall only ever
removes an account it created itself.

Directory and account creation happen during normal server startup, not
during `--service --install`.

### Managing the service

```bash
sudo shortner --service start
sudo shortner --service stop
sudo shortner --service restart
sudo shortner --service reload
sudo shortner --service status
```

`reload` uses the init system's own reload verb where one exists and falls
back to `SIGHUP` where it does not, so a reload never silently degrades
into a restart.

### Disabling and uninstalling

```bash
sudo shortner --service --disable
```

Stops and disables the service but leaves the service file, the
configuration, the data, and the system user in place. The command prints
how to re-enable it.

```bash
sudo shortner --service --uninstall
```

This is destructive. It prompts before touching anything:

```
This will delete ALL data, configs, and the system user. Continue? [y/N]:
```

Only `y` or `yes` proceeds. The prompt runs before even the init-system
probe.

## Privilege Escalation

Service subcommands that need root will escalate on their own rather than
failing with a permissions error. The chain is per-platform:

| Platform | Order tried |
|----------|-------------|
| Linux | already root → `sudo` → `su` → `pkexec` → `doas` |
| macOS | already root → `sudo` → `osascript` prompt |
| BSD | already root → `doas` → `sudo` → `su` |
| Windows | already Administrator → UAC prompt → `runas` |

## Reverse Proxy

Run `shortner` behind a reverse proxy for TLS termination if you are not
using its built-in ACME support. Private address ranges are always trusted
as proxies; add any others under `server.trusted_proxies.additional` so
that the client IP used for rate limiting, GeoIP, and click analytics is
resolved correctly.

See [Integrations](integrations.md#reverse-proxies) for a worked example.

## Upgrading

```bash
shortner --update check
shortner --update
```

The updater queries GitHub Releases for the configured channel, verifies
the downloaded artifact's SHA-256 against the release's `sha256.txt`,
replaces the binary in place, and restarts the service if one is running.
See [CLI Reference](cli.md#update) for channels and options.

## Uninstalling Manually

If you installed without `--service --install`, remove the binary and the
directories listed above. Take a backup first if the data matters:

```bash
shortner --maintenance backup
```
