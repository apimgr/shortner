#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Verify a built shortner runtime image against AI.md PART 26

set -eo pipefail

image="${1:-}"
if [ -z "$image" ]; then
  printf 'usage: %s IMAGE\n' "${0##*/}" >&2
  exit 2
fi

project_name="${PROJECT_NAME:-shortner}"
timeout_seconds="${DOCKER_TIMEOUT:-3600}"
require_overlay="${REQUIRE_OVERLAY:-0}"
container_name="$project_name-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)"

__docker() {
  timeout -k 30 "$timeout_seconds" docker "$@"
}

__cleanup() {
  __docker rm -f "$container_name" >/dev/null 2>&1 || true
}
trap __cleanup EXIT INT TERM

printf 'Checking required runtime commands in %s...\n' "$image"
# The single-quoted program must expand variables inside the image's shell.
# shellcheck disable=SC2016
__docker run --rm --no-healthcheck --name "$project_name-$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8)" --entrypoint sh "$image" \
  -c 'for command in git bash curl tor i2pd tini; do command -v "$command" >/dev/null || exit 1; done; test -s /usr/share/zoneinfo/UTC && test -s /etc/ssl/certs/ca-certificates.crt'

__docker run --detach --rm --name "$container_name" -p 127.0.0.1::80 "$image" >/dev/null
host_port="$(__docker port "$container_name" 80/tcp | sed 's/.*://')"
if [ -z "$host_port" ]; then
  printf 'could not resolve the published application port for %s\n' "$container_name" >&2
  exit 1
fi

__health() {
  curl -q -sS --max-time 2 -H 'Accept: application/json' "http://127.0.0.1:$host_port/server/healthz" 2>/dev/null || true
}

response=''
health_ok=0
health_deadline=$(( $(date +%s) + 60 ))
while [ "$(date +%s)" -lt "$health_deadline" ]; do
  response="$(__health)"
  if printf '%s' "$response" | jq -e '(.status == "healthy" or .status == "degraded") and .checks.database == "ok" and .features.tor.enabled == true and (.features.tor.status == "starting" or .features.tor.status == "healthy" or .features.tor.status == "unavailable") and .features.tor.hostname == "" and .features.i2p.enabled == true and (.features.i2p.status == "starting" or .features.i2p.status == "healthy" or .features.i2p.status == "unavailable") and .features.i2p.hostname == ""' >/dev/null 2>&1; then
    health_ok=1
    break
  fi
  sleep 2
done
if [ "$health_ok" != 1 ]; then
  printf 'image health route failed: %s\n' "$response" >&2
  __docker logs "$container_name" 2>&1 | sed -E 's/(Operator token[^:]*: ).*/\1[REDACTED]/' >&2 || true
  exit 1
fi

__wait_overlay() {
  local feature="$1" wait_seconds="$2" deadline status hostname check
  deadline=$(( $(date +%s) + wait_seconds ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    response="$(__health)"
    status="$(printf '%s' "$response" | jq -r ".features.$feature.status // empty" 2>/dev/null || true)"
    case "$status" in
      healthy|unavailable|error) break ;;
    esac
    sleep 2
  done

  if [ "$status" = healthy ]; then
    hostname="$(printf '%s' "$response" | jq -r ".features.$feature.hostname // empty")"
    check="$(printf '%s' "$response" | jq -r ".checks.$feature // empty")"
    if [ "$check" != ok ]; then
      printf '%s health check is not ok\n' "$feature" >&2
      return 1
    fi
    if [ -n "$hostname" ]; then
      if [ "$feature" = tor ]; then
        [[ "$hostname" =~ ^[a-z2-7]{56}\.onion$ ]] || { printf 'invalid Tor hostname\n' >&2; return 1; }
      else
        [[ "$hostname" =~ ^[a-z2-7]{52}\.b32\.i2p$ ]] || { printf 'invalid I2P hostname\n' >&2; return 1; }
      fi
    else
      printf '%s healthy; hostname withheld by expose policy\n' "$feature"
      return 0
    fi
    printf '%s healthy: %s\n' "$feature" "$hostname"
    return 0
  fi

  if [ "$status" = error ]; then
    printf '%s reported an error: %s\n' "$feature" "$response" >&2
    return 1
  fi
  if [ "$require_overlay" = 1 ]; then
    printf '%s did not become healthy within %ss\n' "$feature" "$wait_seconds" >&2
    return 1
  fi
  printf 'SKIP: no outbound network; %s did not bootstrap\n' "$feature" >&2
}

__wait_overlay tor 300
__wait_overlay i2p 600
printf 'Image contract passed for %s\n' "$image"
