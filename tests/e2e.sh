#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Browser end-to-end beta testing (AI.md PART 28)
#
# Standalone entry point — deliberately NOT invoked by tests/run_tests.sh,
# because it needs a browser image and takes far longer than the binary
# suite.
#
# It wires up three disposable pieces on a private Docker network:
#   1. chromedp/headless-shell — the browser, exposing a CDP endpoint
#   2. casjaysdev/go            — runs `go test -tags e2e ./tests/e2e/...`,
#                                 which itself starts the server binary
#   3. a temp dir on the host   — receives screenshots and HTML dumps for
#                                 failing tests (never the project dir)
#
# The three PART 28 tiers all run inside that go test invocation:
#   Tier 1  server-side rendering, plain net/http, no browser at all
#   Tier 2  JavaScript execution disabled at the browser level
#   Tier 3  full browser journey, zero console errors allowed
#
#   ./tests/e2e.sh

set -eo pipefail

# shellcheck source=tests/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

browser_image="${E2E_BROWSER_IMAGE:-chromedp/headless-shell:latest}"
run_id="$(__random_suffix)"
network_name="$project_name-e2e-$run_id"
browser_name="$project_name-e2e-browser-$run_id"
runner_name="$project_name-e2e-runner-$run_id"
e2e_port="${E2E_PORT:-64123}"
temp_dir=""
keep_artifacts=0

__cleanup() {
  docker rm -f "$runner_name" >/dev/null 2>&1 || true
  docker rm -f "$browser_name" >/dev/null 2>&1 || true
  docker network rm "$network_name" >/dev/null 2>&1 || true
  if [ -n "$temp_dir" ] && [ -d "$temp_dir" ]; then
    if [ "$keep_artifacts" = "1" ]; then
      printf 'artifacts kept at: %s\n' "$temp_dir"
    else
      rm -rf -- "$temp_dir"
    fi
  fi
}
trap __cleanup EXIT

if ! __have_cmd docker; then
  printf 'docker is required for the browser e2e suite\n' >&2
  exit 1
fi

__section "Build"
__build_binaries
if [ ! -x "$bin_dir/$project_name" ]; then
  printf 'build produced no %s binary\n' "$bin_dir/$project_name" >&2
  exit 1
fi
__pass "server binary built: $bin_dir/$project_name"

__section "Browser: $browser_image"
temp_dir="$(__make_temp_dir)"
docker network create "$network_name" >/dev/null
docker run -d --rm \
  --name "$browser_name" \
  --network "$network_name" \
  --shm-size=1g \
  "$browser_image" >/dev/null

# Bounded wait for the DevTools endpoint; never an open-ended sleep.
browser_ready=0
for _ in $(seq 1 60); do
  if docker run --rm --network "$network_name" "$docker_test_image" \
    sh -c "wget -q -O- http://$browser_name:9222/json/version >/dev/null 2>&1"; then
    browser_ready=1
    break
  fi
  sleep 1
done
if [ "$browser_ready" = "1" ]; then
  __pass "browser DevTools endpoint is up"
else
  __fail "browser never exposed a DevTools endpoint"
  docker logs "$browser_name" 2>&1 | tail -n 20 >&2 || true
  exit 1
fi

__section "End-to-end suite"
keep_artifacts=1
e2e_status=0
docker run --rm \
  --name "$runner_name" \
  --network "$network_name" \
  --memory="${DOCKER_MEM:-4g}" --cpus="${DOCKER_CPUS:-2}" \
  -v "$project_dir:/app" \
  -v "$go_cache:/usr/local/share/go/pkg/mod" \
  -v "$go_build_cache:/usr/local/share/go/cache" \
  -v "$temp_dir:/artifacts" \
  -w /app \
  -e CGO_ENABLED=0 \
  -e GOFLAGS=-buildvcs=false \
  -e TMPDIR=/artifacts \
  -e E2E_BIN="/app/binaries/$project_name" \
  -e E2E_CDP_URL="ws://$browser_name:9222/" \
  -e E2E_SELF_HOST="$runner_name" \
  -e E2E_PORT="$e2e_port" \
  -e E2E_ORG="$project_org" \
  -e E2E_NAME="$project_name" \
  "$go_image" \
  go test -tags e2e -count=1 -v -timeout 15m ./tests/e2e/... || e2e_status=$?

if [ "$e2e_status" = "0" ]; then
  keep_artifacts=0
  __pass "browser end-to-end suite passed"
else
  __fail "browser end-to-end suite failed (exit $e2e_status)"
fi

__print_summary || exit 1
exit "$e2e_status"
