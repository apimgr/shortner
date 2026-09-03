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
- PART 29 is implemented: root `mkdocs.yml` (Material theme, slate/deep-purple
  dark default, search + minify plugins, the PART 29 nav) and
  `.readthedocs.yaml` (v2, ubuntu-24.04, python 3.12), plus
  `docs/requirements.txt`, `docs/stylesheets/dark.css` + `light.css`
  (verbatim from the spec), and all eight pages — `index.md`,
  `installation.md`, `configuration.md`, `api.md`, `cli.md`, `security.md`,
  `integrations.md`, `development.md`. Every page documents what the code
  actually does: deferred items (no `shortner-cli`, no Swagger/OpenAPI/
  GraphQL, no Tor/I2P, no DNS-01, no PGP CLI, inert cache, unpopulated
  business metrics) are stated as not implemented rather than described as
  working. RTD site URL is `https://apimgr-shortner.readthedocs.io` per
  PART 29's organization-account format. `mkdocs build --strict` verified
  clean inside `python:alpine` (zero warnings, so every nav entry and
  internal link resolves)

- PART 30 (I18N & A11Y) is implemented for the server binary and Web UI:
  `src/common/i18n/` is the single shared catalog (`go:embed
  locales/*.json`, 7 languages, `Translate`/`TranslateFormat`/
  `TranslatePlural`/`LangFromRequest`/`Direction`/`RawLocale`/
  `CLILanguage`). Interpolation is LITERAL `{token}` replacement — never
  `fmt.Sprintf`, so a stray `%` in a translation can never corrupt
  output, and an unsupplied token stays visible. Plurals use CLDR
  categories via `golang.org/x/text/feature/plural`, with an explicit
  `zero` form preferred at count 0 when the catalog defines one.
  `src/cmd/i18n-validate` + `make i18n-validate` enforce key-set, `{token}`,
  and plural-category parity across all 7 files. Web language priority:
  `?lang=` (sets the `lang` cookie) > cookie > `Accept-Language` >
  server default. CLI priority: `--lang` > config `lang` (unless
  `auto`) > `LC_ALL` > `LANG` > `en`. Email templates keep PART 17's own
  `{variable}` mechanism and are NOT in this catalog — see `TODO.AI.md`
  for that and the other deferred sub-items (client binary, subcommand
  help text, Go `error` strings, PWA/Swagger/GraphQL/toast strings)

---
For complete details, see AI.md PART 28, 29, 30
