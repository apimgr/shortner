# CI/CD Workflow Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use `make` inside CI/CD workflows — explicit commands only
- Pin a third-party GitHub Action to a tag — pin to a full commit SHA
- Add a Go toolchain image or `build-toolchain.yml` when the maintained
  `casjaysdev/go:latest` image is sufficient

## CRITICAL - ALWAYS DO
- `act --list -W {file}` must pass before a workflow file is considered done
- Provide equivalent build/test/security, release, and Docker workflows for
  GitHub, GitLab, Gitea, Forgejo, and Jenkins as required by AI.md PART 27
- Keep Go lint, test, and vulnerability jobs on `casjaysdev/go:latest`; no
  separate toolchain-image workflow is required for this project
- Verify pinned Action SHAs with the 3-point verification process

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Security gates | `ci.yml` contains scheduled secret, vulnerability, workflow-policy, and image scans | AI.md PART 27 |
| Action pinning | full commit SHA, never a tag | global CLAUDE.md, PART 27 |

## QUICK REFERENCE
- PART 27 spans all supported providers: GitHub Actions in `.github/workflows/`,
  GitLab in `.gitlab-ci.yml`, Gitea Actions in `.gitea/workflows/`, Forgejo
  Actions in `.forgejo/workflows/`, and Jenkins in `Jenkinsfile`.
- CI commands are explicit and run the same quality, security, build, and
  release gates; workflows never call Makefile targets.

---
For complete details, see AI.md PART 27
