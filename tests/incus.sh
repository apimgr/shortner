#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Beta testing with Incus — debian:latest (AI.md PART 28)
#
# Builds the binaries in casjaysdev/go:latest, launches a disposable Incus
# system container running real systemd, pushes the binaries and
# tests/suite.sh into it, runs the full integration suite, and then
# exercises the PART 23/24 service lifecycle (install, enable, start,
# reload, restart, stop, uninstall) — the part Docker cannot cover because
# it has no init system.
#
# Host System Safety Rule (AI.md PART 0): every systemctl/useradd/install
# command below runs inside the container via `incus exec`, never on the
# host.
#
#   ./tests/incus.sh

set -eo pipefail

# shellcheck source=tests/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

container_name="test-$internal_name-$(__random_suffix)"
temp_dir=""

__cleanup() {
  incus delete --force "$container_name" >/dev/null 2>&1 || true
  if [ -n "$temp_dir" ] && [ -d "$temp_dir" ]; then
    rm -rf -- "$temp_dir"
  fi
}
trap __cleanup EXIT

if ! __have_cmd incus; then
  printf 'incus is not installed — run ./tests/docker.sh instead\n' >&2
  exit 1
fi

# __cexec {args...} — run a command inside the test container.
__cexec() {
  incus exec "$container_name" -- "$@"
}

# __csh {script} — run a shell snippet inside the test container.
__csh() {
  incus exec "$container_name" -- bash -lc "$1"
}

__section "Build"
__build_binaries
if [ ! -x "$bin_dir/$project_name" ]; then
  printf 'build produced no %s binary\n' "$bin_dir/$project_name" >&2
  exit 1
fi
__pass "server binary built: $bin_dir/$project_name"

__section "Container: $incus_test_image"
temp_dir="$(__make_temp_dir)"
printf 'test root: %s\n' "$temp_dir"

incus launch "$incus_test_image" "$container_name" >/dev/null

# Wait for systemd to finish booting and for DNS/network to come up before
# touching apt — a freshly launched container is not immediately usable.
booted=0
for _ in $(seq 1 60); do
  if __csh 'systemctl is-system-running --wait >/dev/null 2>&1 || systemctl is-system-running 2>/dev/null | grep -qE -- "running|degraded"' >/dev/null 2>&1; then
    booted=1
    break
  fi
  sleep 1
done
if [ "$booted" = "1" ]; then
  __pass "container booted with systemd"
else
  __fail "container never reached a running systemd state"
fi

__csh 'export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq curl jq file ca-certificates >/dev/null'
__pass "test tooling installed in the container"

__cexec mkdir -p /opt/tests "/opt/$internal_name-test"
incus file push "$project_dir/tests/suite.sh" "$container_name/opt/tests/suite.sh" >/dev/null
incus file push "$project_dir/tests/assert.sh" "$container_name/opt/tests/assert.sh" >/dev/null
incus file push "$bin_dir/$project_name" "$container_name/usr/local/bin/$project_name" >/dev/null
__cexec chmod +x "/usr/local/bin/$project_name" /opt/tests/suite.sh

suite_cli=""
if [ -x "$bin_dir/$project_name-cli" ]; then
  incus file push "$bin_dir/$project_name-cli" "$container_name/usr/local/bin/$project_name-cli" >/dev/null
  __cexec chmod +x "/usr/local/bin/$project_name-cli"
  suite_cli="/usr/local/bin/$project_name-cli"
else
  __skip "client binary build" "src/client has no package main yet"
fi
__pass "binaries and suite copied into the container"

__section "Integration suite"
suite_status=0
incus exec "$container_name" \
  --env SUITE_BIN="/usr/local/bin/$project_name" \
  --env SUITE_CLI="$suite_cli" \
  --env SUITE_ROOT="/opt/$internal_name-test" \
  --env SUITE_PORT="${SUITE_PORT:-64580}" \
  --env SUITE_NAME="$project_name" \
  --env NO_COLOR="${NO_COLOR:-1}" \
  -- bash /opt/tests/suite.sh || suite_status=$?

__section "Service lifecycle (PART 23/24)"
if __csh "/usr/local/bin/$project_name --service --install" >/dev/null 2>&1; then
  __pass "--service --install succeeded"
else
  __fail "--service --install failed"
fi

if __csh "systemctl is-enabled $project_name" >/dev/null 2>&1; then
  __pass "service is enabled"
else
  __fail "service is not enabled after --install"
fi

# The unit needs a moment to bind its listener before is-active is
# meaningful; a bounded wait, never an open-ended sleep.
active=0
for _ in $(seq 1 30); do
  if __csh "systemctl is-active --quiet $project_name"; then
    active=1
    break
  fi
  sleep 1
done
if [ "$active" = "1" ]; then
  __pass "service is active after --install"
else
  __fail "service never became active after --install"
  __csh "systemctl status $project_name --no-pager -l || true" || true
  __csh "journalctl -u $project_name --no-pager -n 40 || true" || true
fi

if __csh "id $internal_name" >/dev/null 2>&1; then
  __pass "dedicated system user $internal_name was created"
else
  __fail "dedicated system user $internal_name was not created"
fi

for verb in reload restart stop start; do
  if __csh "/usr/local/bin/$project_name --service $verb" >/dev/null 2>&1; then
    __pass "--service $verb succeeded"
  else
    __fail "--service $verb failed"
  fi
done

if __csh "/usr/local/bin/$project_name --service stop" >/dev/null 2>&1 && ! __csh "systemctl is-active --quiet $project_name"; then
  __pass "service stops cleanly"
else
  __fail "service did not stop cleanly"
fi

# --uninstall always prompts before deleting data, config, and the system
# user; the confirmation is fed on stdin.
if __csh "yes | /usr/local/bin/$project_name --service --uninstall" >/dev/null 2>&1; then
  __pass "--service --uninstall succeeded"
else
  __fail "--service --uninstall failed"
fi
if __csh "systemctl cat $project_name" >/dev/null 2>&1; then
  __fail "unit file still present after --uninstall"
else
  __pass "unit file removed by --uninstall"
fi

__print_summary || suite_status=1
exit "$suite_status"
