# Service Rules (PART 23, 24)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never perform service installation, privilege, or lifecycle operations on the host during tests.
- Never run the daemon with unnecessary privileges or weaken ownership/permission boundaries.
- Never ship service units that reference stale binary paths, flags, or config locations.

## CRITICAL - ALWAYS DO
- Follow the privilege transition and least-privilege startup sequence in PART 23.
- Provide and maintain the supported systemd, OpenRC/runit, SysV, and launchd service integrations from PART 24.
- Test service lifecycle behavior in the prescribed isolated environment.
- Keep generated unit files, CLI help, installation docs, and platform support aligned.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Where do service tests run? | Isolated Incus/container environments, never host. | PART 28 |

## QUICK REFERENCE
Any service change needs lifecycle, restart/failure, permissions, and install/uninstall coverage.

---
For complete details, see AI.md PART 23, 24
