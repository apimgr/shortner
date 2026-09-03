# Configuration & Mode Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Put YAML comments inline — always ABOVE the setting (exception: GitHub
  Actions SHA-pin `# vX.Y.Z` annotations, which stay inline)
- Hardcode dev values — detect mode/debug at runtime
- Roll a second boolean parser — one `config.ParseBool()`/`IsTruthy()` entry
  point for every boolean source (env, config file, CLI)

## CRITICAL - ALWAYS DO
- Mode priority: `--mode` flag > `MODE` env > default `production`
- Debug priority: `--debug` flag > `DEBUG` env > `--mode debug` alias > `false`
- `--mode debug` / `MODE=debug` expands to development+debug; explicit
  `--debug`/`DEBUG` still wins over the alias
- All settings configurable via both API and config file

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Config format | YAML (`gopkg.in/yaml.v3`) | PART 5 |
| Config file name | `server.yml` | PART 4, 5 |
| Four operational states | prod, prod+debug, dev, dev+debug | PART 6 |
| Default mode | `production`, debug `false` | PART 6 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| Mode | `production` or `development` |
| Debug | boolean, orthogonal to mode |

## QUICK REFERENCE
- `src/mode/mode.go` implements `Resolve(modeFlag, debugFlag)` per this
  priority order
- `src/config/config.go` implements `Default`/`Load`/`Save`/`EnsureToken`

---
For complete details, see AI.md PART 5, 6, 12
