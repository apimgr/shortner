# CLI Reference

This page documents the `shortner` server binary's command-line surface.

!!! warning "The client binary is not available yet"
    The project specification calls for a companion client binary,
    `shortner-cli`, with its own flags (`--server`, `--token`, `--output`)
    and a setup wizard. **It is not implemented.** The `src/client`
    directory is a placeholder. Until it ships, use `curl` or any HTTP
    client against the [REST API](api.md).

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
shortner --maintenance restore {file}
```

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
    Restore does not currently stop and restart a running server, and does
    not snapshot the existing state first. Stop the service before
    restoring. The `backup list` and `backup delete` subcommands are not
    implemented, and the encryption hint is not shown at the restore
    prompt.

## Not Yet Implemented

These `--maintenance` subcommands are specified but not built:

`mode`, `setup`, `pgp`, `secret`, `token`, `data`, `compliance`.

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
