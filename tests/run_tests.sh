#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Two-phase test entry point for shortner (AI.md PART 28)
#
# Phase 1 — unit tests: the Go suite (`*_test.go`) with the 60% coverage
#           gate, run through the Makefile's `test` target so there is one
#           source of truth for how the Go tests are invoked.
# Phase 2 — beta tests: the full binary/integration suite, dispatched to
#           the best runtime available on this host:
#             Incus present  -> tests/incus.sh  (debian + real systemd)
#             otherwise      -> tests/docker.sh (alpine, no init system)
#
# Browser end-to-end testing is a separate, standalone entry point and is
# deliberately NOT run from here — see tests/e2e.sh.
#
# Usage:
#   ./tests/run_tests.sh              # unit tests, then beta tests
#   ./tests/run_tests.sh --unit-only  # phase 1 only
#   ./tests/run_tests.sh --beta-only  # phase 2 only
#   ./tests/run_tests.sh --docker     # force the Docker runtime
#   ./tests/run_tests.sh --incus      # force the Incus runtime
#
# Nothing runs on the host: phase 1 builds and tests inside
# casjaysdev/go:latest, phase 2 inside a disposable container.

set -eo pipefail

# shellcheck source=tests/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

run_unit=1
run_beta=1
forced_runtime=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --unit-only) run_beta=0 ;;
    --beta-only) run_unit=0 ;;
    --docker) forced_runtime="docker" ;;
    --incus) forced_runtime="incus" ;;
    -h | --help)
      sed -n '3,24p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      printf 'unknown option: %s (try --help)\n' "$1" >&2
      exit 2
      ;;
  esac
  shift
done

# Chooses the phase 2 runtime: an explicit flag wins, then Incus, then
# Docker. Echoes the runtime name, or nothing when neither is installed.
__detect_runtime() {
  if [ -n "$forced_runtime" ]; then
    printf '%s\n' "$forced_runtime"
    return 0
  fi
  if __have_cmd incus && incus info >/dev/null 2>&1; then
    printf 'incus\n'
    return 0
  fi
  if __have_cmd docker; then
    printf 'docker\n'
    return 0
  fi
  return 0
}

if [ "$run_unit" = "1" ]; then
  __section "Phase 1: unit tests (make test)"
  if ! __have_cmd docker; then
    printf 'docker is required for the Go toolchain container\n' >&2
    exit 1
  fi
  make -C "$project_dir" test
fi

if [ "$run_beta" = "0" ]; then
  exit 0
fi

runtime="$(__detect_runtime)"
case "$runtime" in
  incus)
    __section "Phase 2: beta tests (Incus)"
    exec "$project_dir/tests/incus.sh"
    ;;
  docker)
    __section "Phase 2: beta tests (Docker)"
    exec "$project_dir/tests/docker.sh"
    ;;
  *)
    printf 'neither incus nor docker is available — cannot run beta tests\n' >&2
    exit 1
    ;;
esac
