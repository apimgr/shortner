# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never call Makefile targets from CI/CD; workflows use explicit commands.
- Never run Go builds/tests directly on the host.
- Never write runtime or test data into the project source tree.

## CRITICAL - ALWAYS DO
- Use Makefile targets for local development/build/test entry points as specified.
- Run Go toolchain work through the approved `casjaysdev/go:latest` Docker image.
- Keep build outputs in the configured ignored binary/release directories and runtime data in project-scoped OS temp paths.
- Keep target names, help output, and documented commands accurate.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Are Make targets used by CI? | No; CI spells out explicit commands. | PART 25, 27 |

## QUICK REFERENCE
Run the documented Make target locally; ensure each target uses explicit container names, cleanup, and correct environment.

---
For complete details, see AI.md PART 25
