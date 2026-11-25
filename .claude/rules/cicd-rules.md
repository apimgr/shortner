# CI/CD Workflow Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use `make` inside CI/CD workflows — explicit commands only
- Pin a third-party GitHub Action to a tag — pin to a full commit SHA
- Create `ci.yml`/`release.yml` before security-only workflows exist

## CRITICAL - ALWAYS DO
- `act --list -W {file}` must pass before a workflow file is considered done
- Provide equivalent build/test/security, release, and toolchain workflows
  for GitHub, GitLab, Gitea, Forgejo, and Jenkins as required by AI.md PART 27
- Verify pinned Action SHAs with the 3-point verification process

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Workflow creation order | security-only first, ci/release last | global CLAUDE.md, PART 27 |
| Action pinning | full commit SHA, never a tag | global CLAUDE.md, PART 27 |

## QUICK REFERENCE
- PART 27 spans all supported providers: GitHub Actions in `.github/workflows/`,
  GitLab in `.gitlab-ci.yml`, Gitea Actions in `.gitea/workflows/`, Forgejo
  Actions in `.forgejo/workflows/`, and Jenkins in `Jenkinsfile`.
- CI commands are explicit and run the same quality, security, build, and
  release gates; workflows never call Makefile targets.

---
For complete details, see AI.md PART 27
