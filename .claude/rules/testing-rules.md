# Testing Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never run Go builds, tests, or binaries on the host; never place runtime/test output in the project tree.
- Never use production/development Compose for AI testing or silently fall back from Docker tests to Incus.
- Never accept less than 60% Go coverage or incomplete endpoint coverage.
- Never omit a localization key or ship untranslated visible UI strings.
- Never claim accessibility or documentation checks that were not performed.

## CRITICAL - ALWAYS DO
- Run unit checks through the approved Go container and test/lint wrapper; use the project scripts for Docker/Incus/E2E suites.
- Achieve at least 60% Go coverage and cover every endpoint/integration route.
- Keep MkDocs Material and ReadTheDocs configuration/pages current for installation, configuration, API, CLI, security, integrations, development, and translation.
- Maintain locale parity and WCAG 2.1 AA behavior, including keyboard and screen-reader support.
- Keep test runtime data in `${TMPDIR:-/tmp}/{project_org}/{internal_name}-XXXXXX/` and clean only artifacts created by this task.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Minimum Go coverage? | 60%. | PART 28 |
| Is browser E2E part of commit gate? | No; on-demand beta testing. | PART 28 |
| Where do docs live? | `docs/`, for MkDocs/ReadTheDocs only. | PART 29 |

## QUICK REFERENCE
Verify server-rendered and no-JavaScript flows, route/API errors, locale parity, and docs build when relevant.

---
For complete details, see AI.md PART 28, 29, 30
