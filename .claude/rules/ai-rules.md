# AI Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never modify AI.md; it is the read-only source of implementation requirements.
- Never guess product behavior or skip a relevant PART. Search the spec, follow references, and ask only when it is genuinely silent or contradictory.
- Never add unspecified features, stubs, TODO/FIXME/HACK comments, or commented-out code.
- Never treat existing code, docs, TODO files, or audit files as authoritative over AI.md.
- Never make project workarounds in host configuration, another repository, or global tool configuration.

## CRITICAL - ALWAYS DO
- Read the relevant complete AI.md PART and its FINAL checklist before changing code or docs.
- Treat IDEA.md as product intent and variables; AI.md controls implementation. Do not edit project variables without user confirmation.
- Search before adding; preserve existing user work and make targeted changes.
- Keep implementation, server-rendered pages, API schemas, docs, tests, and deployment files synchronized.
- Verify actual behavior with the project-required checks and report exact commands/results.
- Follow the project commit workflow and never bypass hooks.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Which specification wins? | AI.md; it is read-only. | PART 0 |
| Where do project features and variables come from? | IDEA.md; variables are user-confirmed. | PART 0 |
| What if AI.md contradicts itself? | Record the AI.md-only defect in the named API-template issue log after searching it. | PART 0 |

## TERMINOLOGY
| Term | Meaning |
|---|---|
| Server | Main `shortner` binary and web/API service. |
| Client | Required companion `shortner-cli` binary. |

## QUICK REFERENCE
Before each edit: read the current file, reread its relevant AI.md clauses, search for existing behavior, then update paired docs/pages and verify.

---
For complete details, see AI.md PART 0, 1
