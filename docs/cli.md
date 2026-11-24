# CLI Reference

This page documents both binaries: the `shortner` server and the
`shortner-cli` client. The server sections come first; the client is
documented under [Client](#client-shortner-cli).

## Invocation

```
shortner [flags] [subcommand]
```

Run with no arguments to initialize if needed and start the server. The
binary reports its actual filename in `--help` and `--version`, so it
behaves correctly if you rename it.

## General Flags

| Flag | Description |
|------|-------------|
| `--help`, `-h` | Show help and exit |
| `--version`, `-v` | Show version, commit, and build date |
| `--debug` | Enable debug diagnostics |
| `--mode {value}` | `production`, `development`, `dev`, or `debug` |
| `--color {value}` | `auto` (default), `yes`, or `no` |
| `--lang {code}` | Output language; defaults to the `LANG` environment |
| `--status` | Print server status and health; exits `0` if running, `1` if not |
| `--daemon` | Run in the background (ignored inside containers) |
| `--shell {value}` | Shell integration: `completions`, `init`, `help` |

## Tor maintenance

Tor operations use the authenticated maintenance namespace. Run
`shortner --maintenance tor --help` for the command list. Mutating actions
require root/Administrator or the configured operator token; identity-changing
actions require the documented typed confirmation.

The CLI sends Tor operations to loopback-only internal `/server/tor/*`
endpoints so only the server process controls its embedded Tor instance.
`tor status` and `tor validate` can inspect local state when the server is
stopped; restart, regenerate, vanity start/stop/apply, and key import require a
running server. Vanity searches run in the server as background jobs and can
be cancelled with `shortner --maintenance tor vanity stop`.
`tor import-keys <path>` takes a directory containing Tor's
`hs_ed25519_secret_key` file.
Destructive Tor operations prompt before changing identity; add `--yes` to
the maintenance command to confirm non-interactively.

`--color no` and the conventional `NO_COLOR` environment variable both
disable color.

## Path and Network Flags

| Flag | Description |
|------|-------------|
| `--config {dir}` | Configuration directory |
| `--data {dir}` | Data directory |
| `--cache {dir}` | Cache directory |
| `--log {dir}` | Log directory |
| `--backup {dir}` | Backup directory |
| `--pid {file}` | PID file path |
| `--address {ip}` | Listen address |
| `--port {port}` | Listen port |
| `--baseurl {path}` | URL path prefix |

Each has a matching environment variable; the flag always wins. See
[Configuration](configuration.md#directory-overrides).

## Service

```
shortner --service {verb}
```

| Verb | Effect |
|------|--------|
| `start` | Start the service |
| `stop` | Stop the service |
| `restart` | Restart the service |
| `reload` | Reload configuration; falls back to `SIGHUP` where the init system has no reload verb |
| `status` | Show service state and the detected init system |
| `--install` | Install the service file, enable it, and start it |
| `--disable` | Stop and disable, keeping data, config, service file, and user |
| `--uninstall` | Remove everything, after an explicit confirmation |
| `--help` | Service help |

Verbs that require root escalate on their own rather than failing. The
uninstall prompt is unconditional and runs before anything else:

```
This will delete ALL data, configs, and the system user. Continue? [y/N]:
```

See [Installation](installation.md#installing-as-a-system-service) for the
supported init systems and the service account.

## Update

```
shortner --update [subcommand]
```

| Subcommand | Effect |
|------------|--------|
| `check` | Report whether an update is available; installs nothing |
| `yes` | Download, verify, and install |
| `branch {stable\|beta\|daily}` | Switch and persist the update channel |
| `--help` | Update help |

A bare `--update` means `yes`. `--maintenance update` is an alias.

| Channel | Source |
|---------|--------|
| `stable` | Tagged GitHub releases |
| `beta` | Prerelease tags |
| `daily` | The rolling `daily` tag, rebuilt each night |

The downloaded artifact's SHA-256 is verified against the release's
`sha256.txt` before anything is replaced; a mismatch aborts the update. On
Unix the running binary is replaced in place; on Windows it is renamed
aside and the old file is scheduled for deletion at the next reboot. If
the service is running it is restarted afterwards.

`server.update.defer_days` only constrains the scheduled `update_check`
task — an explicit `--update` ignores it.

!!! note
    Download progress is not printed, and the Windows reboot-cleanup
    notice is not shown. Both are known gaps.

## Scheduler

```
shortner --scheduler {verb} [task_id]
```

| Verb | Effect |
|------|--------|
| `list` | All tasks with schedule, enabled state, last and next run |
| `show {id}` | Task detail plus its five most recent runs |
| `run {id}` | Execute the task immediately and block until it finishes |
| `enable {id}` | Enable a task |
| `disable {id}` | Disable a task |
| `history {id}` | The 20 most recent runs |
| `--help` | Scheduler help |

The twelve built-in task IDs and their defaults are listed in
[Configuration](configuration.md#scheduler).

## Email

```
shortner email {verb} [argument]
```

| Verb | Effect |
|------|--------|
| `test [address]` | Send a test message; defaults to the configured reply-to address |
| `list` | List every template, marking custom overrides against embedded defaults |
| `preview {template}` | Render a template with sample data |
| `validate [template]` | Validate one template, or all of them |
| `reset {template}` | Delete the custom override and fall back to the embedded default |
| `--help` | Email help |

Validation reports errors and warnings, and suggests a correction for an
unknown variable ("Did you mean `{x}`?").

Custom templates live in `{config_dir}/template/email/{event}.tmpl` and
take precedence over the embedded defaults. They are picked up live —
deleting one is how you reset it, which is exactly what `reset` does.

The twelve events are `startup`, `shutdown`, `backup_complete`,
`backup_failed`, `ssl_expiring`, `ssl_renewed`, `ssl_renewal_failed`,
`security_alert`, `scheduler_error`, `update_available`,
`update_installed`, and `test`.

If no SMTP server is configured or auto-detected, email is off — nothing
is queued and nothing is retried. See
[Integrations](integrations.md#email-and-smtp).

## Backup and Restore

```
shortner --maintenance backup [file]
shortner --maintenance backup list
shortner --maintenance backup delete {filename}
shortner --maintenance restore {file}
shortner --maintenance security export TRACKING_ID [--output FILE]
```

Security report export writes the encrypted report message to
`{tracking_id}.asc` with mode `0600` by default. `--output -` writes the
ciphertext to standard output; the report body is never printed in clear.

| Flag | Effect |
|------|--------|
| `--include-ssl` | Include TLS certificates in the archive |
| `--include-data` | Include the data directory in the archive |

Archives are tar+gzip containing a `manifest.json`. When a password is
configured the whole archive is sealed with AES-256-GCM under a key
derived with Argon2id, and the filename gains a `.enc` suffix.

**The password is only ever read from an interactive prompt.** There is no
password flag, so it cannot end up in shell history or a process listing.
`server.backup.encryption.hint` stores a non-secret reminder.

Every backup is verified immediately after creation. The checks run in
order and stop at the first failure: the file exists, it is non-empty, it
decrypts, the archive is extractable with no path-traversal entries, the
manifest parses, the checksum matches, extraction succeeds, the extracted
`server.db` passes a SQLite integrity check, and the version is
compatible. **A backup that fails any check is deleted immediately** —
there is no such thing as a half-good archive on disk.

Restore authorization depends on who is running it:

| Situation | Behavior |
|-----------|----------|
| The database is empty | Restore proceeds |
| Running as root or Administrator | Confirmation prompt |
| Running as the service user | Operator token required, compared in constant time |
| Anyone else | Denied |

!!! note
    A running service-managed server is stopped before restore and started
    afterward. A standalone server without a detected service manager is
    not stopped automatically; stop it before restoring. The configured
    encryption hint is shown before the password prompt. Backup deletion
    requires operator authorization and typed confirmation.

## PGP Key Management

The `pgp` maintenance command manages the project security-report keypair:

```text
shortner --maintenance pgp generate
shortner --maintenance pgp rotate
shortner --maintenance pgp publish
shortner --maintenance pgp export public [PATH]
shortner --maintenance pgp export private PATH
shortner --maintenance pgp import FILE
shortner --maintenance pgp delete
```

The private key is encrypted at rest. Private-key export requires operator
authorization, writes files with mode `0600`, and is rate-limited.

## Mode, Setup, and Secret Rotation

Persist an operational mode with `--maintenance mode`. Root/Administrator
can run the command directly. The service account must enter the configured
operator token; unrelated users are denied. The server must be restarted
for a mode change to take effect.

```text
shortner --maintenance mode production
shortner --maintenance mode development
shortner --maintenance mode debug
shortner --maintenance setup
shortner --maintenance secret rotate installation_secret
shortner --maintenance secret rotate encryption_key
```

`setup` allows first-run initialization when the database has no links.
Once resources exist, it is root-only and requires confirmation before
resetting `server.yml` to defaults. Manual secret rotation requires operator
authorization and a typed `ROTATE <name>` confirmation. The installation
secret rotation re-seals the PGP private key and keeps the previous secret
for seven days to validate in-flight security links. Encryption-key rotation
re-seals stored token copies and security-report bodies and retains the old
key for the specified 30-day grace period. Neither secret value is printed.

## Data and compliance maintenance

Exports contain owner metadata, token metadata without token values or
hashes, and the owner's resources. Deletion removes the owner, tokens, and
resources transactionally after confirmation. Compliance reports contain
aggregate counts and do not include owner identifiers.

```text
shortner --maintenance data export --owner <prefix> [--output FILE]
shortner --maintenance data delete --owner <prefix> [--yes]
shortner --maintenance compliance report
```

Owner tokens can be listed without revealing their raw values, or revoked
by prefix:

```text
shortner --maintenance token list
shortner --maintenance token revoke <prefix>
```

## Signals

| Signal | Effect |
|--------|--------|
| `SIGTERM`, `SIGINT`, `SIGQUIT`, `SIGRTMIN+3` | Graceful shutdown |
| `SIGHUP` | Deliberately ignored |
| `SIGUSR1` | Log rotation hook (Unix only) |
| `SIGUSR2` | Status dump hook (Unix only) |

Graceful shutdown stops accepting connections, drains in-flight requests,
closes the HTTP server, stops the scheduler, sends the shutdown
notification if email is enabled, closes GeoIP and the database, flushes
logs, and removes the PID file.

`SIGHUP` is reserved for a future file-watcher-driven configuration
reload, so it currently does nothing. Configuration changes need a
restart.

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success, or `--status` found the server running |
| `1` | Error, or `--status` found the server not running |

## Client (`shortner-cli`)

`shortner-cli` is the companion client. It is a single static binary with
no runtime dependencies, built from `src/client/` for the same eight
platforms as the server.

```
shortner-cli [flags] [command] [args]
shortner-cli
```

Run with no command in a terminal and the client opens its interactive
interface. Run it with no command outside a terminal (in a pipe, in cron)
and it exits with a usage error rather than hanging.

### Client Flags

| Flag | Description |
|------|-------------|
| `-h`, `--help` | Show help |
| `-v`, `--version` | Show version |
| `--shell completions [SHELL]` | Print the completion script |
| `--shell init [SHELL]` | Print the eval-ready init line |
| `--debug` | Enable debug output |
| `--color {auto,yes,no}` | Color output |
| `--lang CODE` | Output language |
| `--server URL` | Server base URL |
| `--token TOKEN` | API token for ownership operations |
| `--token-file FILE` | Read the token from a file |
| `--config NAME` | Config profile name (default `cli`) |
| `--output {table,json,yaml,plain,csv}` | Output format |
| `--quiet` | Suppress non-essential output |
| `--verbose` | Show additional detail |
| `--update [check\|yes]` | Check for or install a newer client |

`SHELL` is optional for both `--shell` forms — it is auto-detected from
`$SHELL`. Supported shells: `bash`, `zsh`, `fish`, `sh`, `dash`, `ksh`,
`powershell`, `pwsh`.

### Client Commands

| Command | Description |
|---------|-------------|
| `shorten URL [--slug NAME] [--expire WHEN]` | Create a short link |
| `get SLUG` | Show one short link |
| `list [--page N] [--limit N]` | List short links |
| `update SLUG [--url URL] [--expire WHEN]` | Change destination or expiry |
| `delete SLUG [--force]` | Delete a short link |
| `stats SLUG` | Show click statistics |
| `health` | Show server health |
| `setup` | Run the configuration wizard |

Arguments are detected rather than flagged wherever possible: a bare
`http(s)` URL is shortened, a bare short code is looked up, and a URL
piped on stdin is shortened.

```bash
shortner-cli https://example.com/a/very/long/path
shortner-cli abc123
echo "https://example.com" | shortner-cli
```

### Client Configuration

Configuration lives at `{config_dir}/cli.yml` in the user's own
directories — never a system path. The directory is created `0700` and
the file `0600`, because it can hold an API token.

Values are resolved highest-priority first:

1. a command-line flag
2. an environment variable (`SHORTNER_SERVER_PRIMARY`, `SHORTNER_TOKEN`, …)
3. `cli.yml`
4. the compiled default

A flag is written back to `cli.yml` only when the stored value is empty
or invalid, so a one-off `--server` never overwrites a working
configuration.

### Interactive Interface

The client picks its interface automatically. There is no `--tui`,
`--cli`, or `--gui` flag; only `display.mode` in `cli.yml` overrides the
detection. An SSH or Mosh session always gets the terminal interface,
even with X11 forwarding available.

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | Move |
| `g` / `G` | Top / bottom |
| `n` / `p` | Next / previous page |
| `enter`, `l` | Open the selected link |
| `esc`, `h` | Back |
| `/` | Search |
| `c` | Create a link |
| `s` | Statistics |
| `H` | Server health |
| `d` | Delete (asks for confirmation) |
| `r` | Refresh |
| `?` | Help |
| `q`, `ctrl+c` | Quit |

The layout is responsive across seven terminal-size breakpoints and
redraws on resize, so a phone-sized SSH session stays usable. Terminals
without UTF-8 get an ASCII symbol set automatically.

!!! note "Optional native GUI"
    Native GUI support is built with `-tags gui` on Linux, macOS, and
    Windows using the pure-Go `gogpu/ui` toolkit. BSD builds use the TUI.
    The GUI supports setup, link creation, public link listing, lookup,
    update, deletion, click statistics, and server health. Other client
    configuration and update controls remain available through the CLI/TUI;
    full GUI parity and native screen-reader support are not currently
    available; use the CLI or TUI for those controls.

The server publishes `/api/autodiscover` and serves operator-provisioned
client binaries from `{data_dir}/cli/` by default. Set
`cli.binary_download.dir` in `yaml.d/maintenance.yml` to use another
directory, or `cli.binary_download.require_auth: true` to require the
operator bearer token for downloads. Place release binaries together with
`version.txt` and `sha256.txt` there; the server recomputes each checksum
and advertises only verified binaries. An empty list means there is no
downloadable client build for that platform.

### Client Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General error |
| `2` | Configuration error |
| `3` | Connection error |
| `4` | Authentication error |
| `5` | Not found |
| `64` | Usage error |
| `130` | Interrupted |
