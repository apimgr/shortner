# Project Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never place Docker files outside `docker/` or runtime data in the source tree.
- Never invent project variables, official URLs, license details, or product behavior.
- Never add a forbidden or duplicate project file; inspect the allowed/forbidden file rules first.
- Never change frozen `internal_name` or `internal_org` values without explicit direction and a full migration plan.

## CRITICAL - ALWAYS DO
- Keep README and project structure synchronized with actual server, client, install, and Docker behavior.
- Follow the exact source, docs, runtime-data, and build-output paths in AI.md.
- Preserve project variables in IDEA.md; resolve template values from that file.
- Use the platform-specific paths and permissions defined by the OS path rules.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Where are container definitions? | Under `docker/`. | PART 3 |
| Where does runtime data go? | OS-specific config/data/log/cache paths, never source files. | PART 4 |

## QUICK REFERENCE
README is user-facing and current; docs are in `docs/` for ReadTheDocs; generated runtime configuration is not committed.

---
For complete details, see AI.md PART 2, 3, 4
