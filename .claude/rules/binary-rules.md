# Binary Requirements Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use CGO — `CGO_ENABLED=0` always, pure Go dependencies only
- Ship dynamic assets separately — embed with Go's `embed` package
- Skip the client binary — the client CLI is REQUIRED for every project

## CRITICAL - ALWAYS DO
- Single static binary, no runtime dependencies
- No arguments → initialize (if needed) and start the server
- First run → auto-create `server.yml` with defaults, show banner
- First run → auto-create required directories
- Handle SIGTERM/SIGINT/SIGHUP properly; PID file enabled by default
- Shared flags on ALL binaries: `--help`, `--version`, `--shell`, `--debug`,
  `--color`, `--lang`
- Show the ACTUAL (possibly renamed) binary name in `--help`/`--version`

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Server binary name | `shortner` | PART 8 |
| Client binary name | `shortner-cli` | PART 8 |
| Server key flags | `--config`, `--data`, `--port`, `--mode` | PART 8 |
| Client key flags | `--server`, `--token`, `--output` | PART 8 |
| CLI setup wizard | client binary only, not server | PART 32 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| User-Agent | `{name}/{version}` per binary |

## QUICK REFERENCE
- `src/main.go` currently wires `--help`, `--version`, `--mode`, `--debug`,
  `--config`, `--address`, `--port`, `--baseurl`, `--status`, and
  `--update` (PART 22 — `check`/`yes`/`branch`, with a bare `--update`
  meaning `yes`), the positional `email` subcommand (PART 17 —
  `test`/`list`/`preview`/`validate`/`reset`), and `--service` (PART
  23/24 — `start`/`stop`/`restart`/`reload`/`--install`/`--disable`/
  `--uninstall`/`--help`), and the positional `tor` / `i2p` subcommands
  (PART 31 — `tor status|validate|restart|regenerate|vanity start|vanity
  apply|import-keys <path>`, `i2p status|validate|regenerate`);
  most `--maintenance` actions and `--daemon` are NOT yet implemented
  (see `TODO.AI.md`)
- PART 32 is implemented: `shortner-cli` lives in `src/client/`
  (`main.go` plus `cmd/`, `config/`, `paths/`, `api/`, `tui/`, `setup/`).
  It carries every universal flag (`--help`, `--version`, `--shell`,
  `--debug`, `--color`, `--lang`) plus `--server`, `--token`,
  `--token-file`, `--config`, `--output`, `--update`, `--quiet`,
  `--verbose`; the verbs `shorten`/`get`/`list`/`update`/`delete`/
  `stats`/`health`/`setup` with smart argument detection (a bare URL
  shortens, a bare slug is looked up, stdin is read in a pipe); shell
  completions for bash, zsh, fish, sh, dash, ksh, powershell, pwsh; and a
  bubbletea TUI that launches automatically when the client is run with
  no command in a terminal. There is NO `--tui`/`--cli`/`--gui` flag and
  no `tui` subcommand — PART 32 forbids them; only `display.mode` in
  `cli.yml` overrides auto-detection. The native GUI is deferred behind a
  `gui` build tag (cgo conflicts with `CGO_ENABLED=0`) and falls back to
  the feature-identical TUI

---
For complete details, see AI.md PART 7, 8, 32
