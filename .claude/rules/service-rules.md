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
- `src/paths/paths.go` implements `IsPrivileged()` and
  `IsAdministrator()` (Windows via build-tag files); `src/service/`
  builds on top of them — it never duplicates that detection
- PART 23/24 are implemented in `src/service/`:
  - `escalate.go` / `escalate_windows.go` — the per-OS escalation chains
    (Linux root→sudo→su→pkexec→doas, macOS root→sudo→osascript,
    BSD root→doas→sudo→su, Windows Administrator→UAC→runas) and
    `ExecElevated`
  - `sysuser.go` — the dedicated `{internal_name}` user/group with
    matching UID==GID picked from 899→200 (399→200 on macOS) skipping the
    verbatim reserved-ID map, a no-login shell, no password; Linux
    `groupadd`/`useradd --system`, macOS `dscl` (IsHidden 1), FreeBSD
    `pw`. A `.service-account` marker records that this binary created
    the account, so uninstall only deletes a user it owns
  - `detect.go` — init-system detection; SysVinit is chosen only when
    both `openrc-run` and `systemctl` are absent
  - `template.go` — systemd, OpenRC, SysVinit, runit (`run` + `log/run`),
    rc.d, and launchd templates rendered to PART 24's exact install paths
  - `systemd.go`/`openrc.go`/`sysvinit.go`/`runit.go`/`rcd.go`/
    `launchd.go`/`windows.go` — the per-manager verbs
  - `reload_unix.go`/`reload_windows.go` — SIGHUP fallback for the init
    systems with no reload verb; reload never silently becomes a restart
- `src/service.go` wires `--service start|stop|restart|reload`,
  `--install`, `--disable`, `--uninstall`, and `--help` into `src/main.go`
- `--service --install` installs the service file, enables, and starts —
  user/group/directory creation happens during NORMAL server startup
- `--service --uninstall` always prompts
  `This will delete ALL data, configs, and the system user. Continue? [y/N]`
  before anything destructive, and the prompt runs before even the
  init-system probe
- Live init-system installs cannot be exercised in the build container —
  see `TODO.AI.md` for exactly what is and is not covered by tests

---
For complete details, see AI.md PART 23, 24
