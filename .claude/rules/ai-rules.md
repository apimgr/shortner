# AI Assistant Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Modify `AI.md` content — it is read-only spec; project changes go in `IDEA.md`
- Guess or assume a value a command can produce (`date`, `basename "$PWD"`,
  `git config user.email`, `uname -m`) — run the command instead
- Implement any feature without reading its PART in AI.md first
- Use bcrypt for config/backup passwords — use Argon2id (API tokens: SHA-256)
- Create premium tiers or paywalls — all features free
- Skip target platforms — build all 8 (linux/darwin/windows/freebsd × amd64/arm64)
- Use client-side rendering frameworks (React/Vue) — server-side Go templates only

## CRITICAL - ALWAYS DO
- Read the relevant AI.md PART before implementing anything
- Run the mandatory compliance schedule before/during/after work
- Keep CLAUDE.md and `.claude/rules/*.md` in sync with what AI.md actually says
- Commit often via `gitcommit <command>`, small focused commits, fresh
  `COMMIT_MESS` per commit — subagents never commit, only the parent does

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Where is the full spec? | `AI.md` (read-only, ~46k lines) | PART 0 |
| Where do project specifics go? | `IDEA.md` (description/variables/business logic) | PART 0, 33 |
| Who commits? | Parent session only, via `gitcommit` | PART 0 |
| What if AI.md and IDEA.md conflict? | AI.md wins; fix IDEA.md | PART 0 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| server | `shortner` main binary, runs as service |
| client | `shortner-cli` companion CLI/TUI/GUI |

## QUICK REFERENCE
- Self-check before any change: read spec → follow exactly → don't guess →
  would this pass the compliance checklist?

---
For complete details, see AI.md PART 0, 1
