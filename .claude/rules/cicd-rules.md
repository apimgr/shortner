# CI/CD Workflow Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use `make` inside CI/CD workflows — explicit commands only
- Pin a third-party GitHub Action to a tag — pin to a full commit SHA
- Create `ci.yml`/`release.yml` before security-only workflows exist

## CRITICAL - ALWAYS DO
- `act --list -W {file}` must pass before a workflow file is considered done
- Mirror workflows in `.github/workflows/` and `.gitea/workflows/` where
  applicable
- Verify pinned Action SHAs with the 3-point verification process

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Workflow creation order | security-only first, ci/release last | global CLAUDE.md, PART 27 |
| Action pinning | full commit SHA, never a tag | global CLAUDE.md, PART 27 |

## QUICK REFERENCE
- No workflow files exist yet — PART 27 is explicitly out of PART 0-6
  scope; tracked in `TODO.AI.md`

---
For complete details, see AI.md PART 27
