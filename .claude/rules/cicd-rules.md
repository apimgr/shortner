# CI/CD Rules (PART 27)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never use Makefile targets in CI.
- Never use unpinned third-party actions, excessive workflow permissions, unsafe fork-triggered publishing, or exposed secrets.
- Never omit a required provider workflow or any of the eight platform builds.
- Never report completion after push without checking the resulting CI run.

## CRITICAL - ALWAYS DO
- Keep GitHub, GitLab, Gitea, Forgejo, and Jenkins workflows aligned with the same explicit build/test/security behavior.
- Build with `CGO_ENABLED=0` for Linux, macOS, Windows, and FreeBSD on amd64 and arm64.
- Run vet, staticcheck, coverage tests, vulnerability scans, secret scans, workflow policy, and image security checks as specified.
- Pin actions to verified full commit SHAs and use least-privilege permissions.
- Publish only verified artifacts with checksums, SBOM, release notes, and provenance/attestation where required.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| CI uses Makefile? | No; commands are explicit. | PART 27 |
| Required platform matrix? | Eight OS/architecture pairs. | PART 7, 27 |

## QUICK REFERENCE
After pushing, inspect the provider run, resolve failures, and verify the final run is green.

---
For complete details, see AI.md PART 27
