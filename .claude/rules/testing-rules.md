# Testing, Docs & I18N Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Commit with failing tests or below the 60% coverage gate
- Skip accessibility (WCAG 2.1 AA) when adding frontend features

## CRITICAL - ALWAYS DO
- `tests/run_tests.sh`, `tests/docker.sh`, `tests/incus.sh` are the required
  test entry points
- `make test` (or the CI equivalent explicit commands) must pass before
  every commit
- Documentation lives in `docs/` (ReadTheDocs layout: index, installation,
  configuration, api, cli, security, integrations, development)

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Coverage minimum | 60% | global CLAUDE.md, PART 28 |
| Test runner locations | `tests/run_tests.sh` + `docker.sh` + `incus.sh` | PART 3, 28 |
| Docs system | ReadTheDocs-style `docs/*.md` | PART 29 |

## QUICK REFERENCE
- `tests/`, `docs/` directories exist but are empty — test runner scripts
  and doc stubs are tracked in `TODO.AI.md`

---
For complete details, see AI.md PART 28, 29, 30
