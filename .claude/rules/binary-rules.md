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
  meaning `yes`); `--service`, most `--maintenance` actions, `--daemon`,
  and the client binary itself are NOT yet implemented (see `TODO.AI.md`)

---
For complete details, see AI.md PART 7, 8, 32
