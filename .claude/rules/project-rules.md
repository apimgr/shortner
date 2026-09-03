# Project Structure Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Use any license other than MIT
- Put `Dockerfile` in the project root — it belongs in `docker/Dockerfile`
- Hardcode OS-specific paths — resolve them at runtime per PART 4
- Skip creating `LICENSE.md` in the project root

## CRITICAL - ALWAYS DO
- License: MIT, `LICENSE.md` in root, copyright holder = `{project_org}`
- Detect privileged vs unprivileged mode and pick the matching path set
- Use simplified paths (`/config`, `/data`) when running inside Docker
- Source lives in `src/`, tests in `tests/`, scripts in `scripts/`

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| License | MIT | PART 2 |
| Linux privileged config path | `/etc/{internal_org}/{internal_name}/server.yml` | PART 4 |
| Linux privileged data path | `/var/lib/{internal_org}/{internal_name}/` | PART 4 |
| Docker container paths | `/config`, `/data` (simplified) | PART 4 |
| Root layout | `src/`, `scripts/`, `tests/`, `docker/`, `docs/` | PART 3 |

## TERMINOLOGY
| Term | Meaning |
|------|---------|
| internal_name | frozen on-disk identifier, set once at first setup |
| internal_org | frozen on-disk org identifier, set once at first setup |

## QUICK REFERENCE
- `src/paths` resolves `Paths` by `runtime.GOOS` × privileged/unprivileged ×
  Docker container detection (see `src/paths/paths.go`)

---
For complete details, see AI.md PART 2, 3, 4
