#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Beta testing with Docker — alpine:latest (AI.md PART 28)
#
# Builds the binaries in casjaysdev/go:latest, boots a disposable
# alpine:latest container, pushes the binaries and tests/suite.sh into it,
# and runs the full integration suite there. Nothing is ever built or
# executed on the host, and no runtime data touches the project directory:
# the container's working root is a bind mount of
# ${TMPDIR:-/tmp}/{project_org}/{internal_name}-XXXXXX/volumes.
#
# This is the Docker half of tests/run_tests.sh. Run it directly for a
# quick beta pass without Incus:
#
#   ./tests/docker.sh

set -eo pipefail

# shellcheck source=tests/common.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

container_name="$project_name-test-$(__random_suffix)"
image_container_name="$project_name-image-check-$(__random_suffix)"
image_tag="$project_name:test-$(__random_suffix)"
temp_dir=""

__cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker rm -f "$image_container_name" >/dev/null 2>&1 || true
  docker image rm "$image_tag" >/dev/null 2>&1 || true
  if [ -n "$temp_dir" ] && [ -d "$temp_dir" ]; then
    rm -rf -- "$temp_dir"
  fi
  if [ -n "$bin_dir" ] && [ -d "$bin_dir" ]; then
    rm -rf -- "$bin_dir"
  fi
}
trap __cleanup EXIT

if ! __have_cmd docker; then
  printf 'docker is not installed — cannot run the Docker beta suite\n' >&2
  exit 1
fi

__section "Build"
__build_binaries
if [ ! -x "$bin_dir/$project_name" ]; then
  printf 'build produced no %s binary\n' "$bin_dir/$project_name" >&2
  exit 1
fi
__pass "server binary built: $bin_dir/$project_name"
if __has_client_main; then
  __pass "client binary built: $bin_dir/$project_name-cli"
else
  __skip "client binary build" "src/client has no package main yet"
fi

__section "Runtime image contract"
image_epoch="$(date -u +%s)"
docker build \
  --build-arg VERSION="$(__build_version)" \
  --build-arg BUILD_DATE="$(date -u -d "@$image_epoch" +%Y-%m-%dT%H:%M:%SZ)" \
  --build-arg BUILD_EPOCH="$image_epoch" \
  --build-arg COMMIT_ID="$(git -C "$project_dir" rev-parse --short HEAD)" \
  -f "$project_dir/docker/Dockerfile" -t "$image_tag" "$project_dir"
docker run --rm --entrypoint sh "$image_tag" -c \
  'for c in bash curl tor i2pd tini; do command -v "$c" || exit 1; done; test -r /etc/ssl/cert.pem; test -d /usr/share/zoneinfo'
__pass "runtime image includes required binaries, timezone data, and CA certificates"

docker run -d --rm --name "$image_container_name" "$image_tag" >/dev/null
image_healthy=false
health_deadline=$((SECONDS + 60))
while [ "$SECONDS" -lt "$health_deadline" ]; do
  image_health="$(docker exec "$image_container_name" curl -fsS --max-time 2 -H 'Accept: application/json' http://127.0.0.1/server/healthz 2>/dev/null || true)"
  if printf '%s' "$image_health" | jq -e '(.status == "healthy" or .status == "degraded") and .checks.database == "ok" and .features.tor.enabled == true and (.features.tor.status == "starting" or .features.tor.status == "healthy" or .features.tor.status == "unavailable")' >/dev/null 2>&1; then
    image_healthy=true
    break
  fi
  sleep 2
done
if [ "$image_healthy" != true ]; then
  printf '%s\n' "$image_health" >&2
  docker logs "$image_container_name" 2>&1 | sed -E 's/(Operator token[^:]*: ).*/\1[REDACTED]/' >&2 || true
  __fail "runtime image did not serve clearnet health with Tor detected"
else
  __pass "runtime image serves clearnet health while detected Tor bootstraps or runs"
fi
docker rm -f "$image_container_name" >/dev/null

__section "Container: $docker_test_image"
temp_dir="$(__make_temp_dir)"
printf 'test root: %s\n' "$temp_dir"

docker run -d --rm \
  --name "$container_name" \
  --memory="${DOCKER_MEM:-2g}" --cpus="${DOCKER_CPUS:-2}" \
  -v "$temp_dir/volumes:/opt/$internal_name-test" \
  -w "/opt/$internal_name-test" \
  "$docker_test_image" \
  sleep 900 >/dev/null

# The suite needs bash/curl/file/jq, and server startup needs the account
# tools supplied by shadow; alpine ships none of them in the base image.
docker exec "$container_name" sh -c 'apk add --no-cache bash curl file jq ca-certificates shadow tor i2pd tzdata >/dev/null'
__pass "test tooling installed in the container"

docker exec "$container_name" mkdir -p /opt/tests
docker cp "$project_dir/tests/suite.sh" "$container_name:/opt/tests/suite.sh"
docker cp "$project_dir/tests/assert.sh" "$container_name:/opt/tests/assert.sh"
docker cp "$bin_dir/$project_name" "$container_name:/usr/local/bin/$project_name"
docker exec "$container_name" chmod +x "/usr/local/bin/$project_name" /opt/tests/suite.sh

suite_cli=""
if [ -x "$bin_dir/$project_name-cli" ]; then
  docker cp "$bin_dir/$project_name-cli" "$container_name:/usr/local/bin/$project_name-cli"
  docker exec "$container_name" chmod +x "/usr/local/bin/$project_name-cli"
  suite_cli="/usr/local/bin/$project_name-cli"
fi
__pass "binaries and suite copied into the container"

__section "Integration suite"
docker exec \
  -e SUITE_BIN="/usr/local/bin/$project_name" \
  -e SUITE_CLI="$suite_cli" \
  -e SUITE_ROOT="/opt/$internal_name-test" \
  -e SUITE_PORT="${SUITE_PORT:-64580}" \
  -e SUITE_NAME="$project_name" \
  -e NO_COLOR="${NO_COLOR:-}" \
  "$container_name" \
  bash /opt/tests/suite.sh
