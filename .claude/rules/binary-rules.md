# Binary Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never build, test, or run Go binaries on the host; use `casjaysdev/go:latest` through approved project commands.
- Never enable CGO or omit `CGO_ENABLED=0` for builds.
- Never omit the required `shortner-cli` companion or its shared CLI behavior.
- Never print secrets, tokens, or passwords in CLI output or logs.

## CRITICAL - ALWAYS DO
- Build static server and CLI binaries for Linux, macOS, Windows, and FreeBSD on amd64 and arm64.
- Keep command-line parsing, validation, help, color, language, and NO_COLOR behavior aligned with AI.md.
- Implement the client as a complete API client with the specified token discovery, first-run, and context behavior.
- Update help, man pages, completions, docs, and tests when commands or flags change.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Is the companion client required? | Yes, `shortner-cli`. | PART 32 |
| Is CGO allowed? | No. | PART 7 |

## QUICK REFERENCE
CI builds all eight OS/architecture pairs; validate both binaries and their help/translation surfaces.

---
For complete details, see AI.md PART 7, 8, 32
