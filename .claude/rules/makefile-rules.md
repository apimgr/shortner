# Makefile Rules (PART 25)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use the Makefile in CI/CD — CI uses explicit commands only, never `make`
- Build on the host — always Docker (`casjaysdev/go:latest`)

## CRITICAL - ALWAYS DO
- Six targets: `dev`, `local`, `build`, `test`, `release`, `docker` (+
  `clean`)
- Embed build info via `-ldflags`: `Version`, `CommitID`, `BuildDate`,
  `OfficialSite`
- `CGO_ENABLED=0` in every build target

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Toolchain image | `casjaysdev/go:latest` | global CLAUDE.md, PART 25 |
| Target platforms | linux/darwin/windows/freebsd × amd64/arm64 (8 total) | PART 1, 25 |

## QUICK REFERENCE
- `Makefile` at project root already implements all six targets plus
  `clean`, adapted from the AI.md PART 25 template

---
For complete details, see AI.md PART 25
