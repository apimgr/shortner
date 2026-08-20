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
- PART 28's `tests/` deliverables are implemented:
  - `tests/run_tests.sh` — two-phase entry point: phase 1 `make test`
    (Go suite + 60% gate), phase 2 auto-detects the runtime (Incus when
    `incus info` succeeds, else Docker) and dispatches
  - `tests/docker.sh` — builds in `casjaysdev/go:latest`, runs the full
    suite inside a disposable `alpine:latest` container
  - `tests/incus.sh` — same suite inside `images:debian/trixie`, plus the
    real PART 23/24 systemd service lifecycle (install, enable, active,
    system user, reload/restart/stop/start, uninstall + its prompt)
  - `tests/suite.sh` — the shared in-container suite (binary info, rename
    test, first run, health, content negotiation, well-known, frontend
    pages, link CRUD, redirect/410/404, stats, token authorization, CLI,
    rate limiting); `tests/assert.sh` + `tests/common.sh` are its helpers
  - `tests/test_content_negotiation.sh` — standalone negotiation matrix
    against any running instance
  - `tests/e2e.sh` + `tests/e2e/` — chromedp browser suite behind the
    `e2e` build tag (Tier 1 SSR, Tier 2 JS-disabled, Tier 3 full browser
    with zero console errors), driven against a `chromedp/headless-shell`
    container; standalone, never invoked by `run_tests.sh`
- All runtime/test data goes to `${TMPDIR:-/tmp}/apimgr/shortner-XXXXXX/`
  — never the project directory
- No full beta/e2e run has been executed yet; see `TODO.AI.md`
- `docs/` still exists but is empty — doc stubs tracked in `TODO.AI.md`

---
For complete details, see AI.md PART 28, 29, 30
