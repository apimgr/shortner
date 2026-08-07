# Privilege Escalation & Service Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Assume root — detect privileged vs unprivileged at runtime and pick the
  matching path set (PART 4)
- Write a systemd unit or service file outside what PART 23/24 specify

## CRITICAL - ALWAYS DO
- Binary handles its own directory creation, permissions, user/group drop —
  not the entrypoint script (see `docker/rootfs/usr/local/bin/entrypoint.sh`,
  which stays minimal by design)
- Support `--service` install/uninstall subcommands per PART 24

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|----------|--------|-----------------|
| Privilege detection | `paths.IsPrivileged()` / `IsAdministrator()` | PART 4, 23 |
| Entrypoint responsibility | minimal — env, signals, exec only | PART 23, 26 |

## QUICK REFERENCE
- `src/paths/paths.go` already implements `IsPrivileged()` and
  `IsAdministrator()` (Windows via build-tag files); the `--service`
  subcommand itself is not yet implemented — tracked in `TODO.AI.md`

---
For complete details, see AI.md PART 23, 24
