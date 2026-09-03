# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Put `Dockerfile` in the project root — `docker/Dockerfile` only
- Put non-minimal logic in `entrypoint.sh` — the binary handles setup, dirs,
  permissions, user/group, Tor; entrypoint only sets env, starts the
  binary, and handles signals
- Run a container without `--rm --name {project}-XXXX`

## CRITICAL - ALWAYS DO
- Multi-stage build: `casjaysdev/go:latest` builder → `alpine` runtime
- `tini` as PID 1, `STOPSIGNAL SIGRTMIN+3`
- `HEALTHCHECK` calls the binary's own `--status` flag
- Separate `docker-compose.yml` (prod), `docker-compose.dev.yml` (dev),
  `docker-compose.test.yml` (ephemeral test) files in `docker/`

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Base runtime image | `alpine:latest` | PART 26 |
| Signal handling | tini + SIGRTMIN+3 | PART 26 |
| Healthcheck command | `{binary} --status` | PART 13, 26 |

## QUICK REFERENCE
- `docker/Dockerfile`, `docker/Dockerfile.dev`,
  `docker/rootfs/usr/local/bin/entrypoint.sh`, and all three compose files
  already exist and follow this pattern; build verified inside Docker (see
  `TODO.AI.md`/bootstrap report for the exact commands run)

---
For complete details, see AI.md PART 26
