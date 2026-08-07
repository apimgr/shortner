#!/usr/bin/env bash
set -euo pipefail

# Runs system-level integration tests (systemd service install/start/stop,
# privilege escalation, firewall/network behavior) inside an ephemeral
# Incus container. Never test these behaviors on the host — see AI.md
# PART 0 "Host System Safety Rule" and PART 28.
#
# No systemd/service-install support exists yet (tracked in TODO.AI.md,
# PART 23/24), so this script currently only proves the container lifecycle
# works; it will grow real checks once --service is implemented.

internal_name="shortner"
container_name="test-${internal_name}-$(set +o pipefail; tr -dc 'a-z0-9' </dev/urandom | head -c8)"

cleanup() {
  incus delete --force "$container_name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

incus launch images:alpine/edge "$container_name"
incus exec "$container_name" -- sh -c 'apk add --no-cache curl'
incus exec "$container_name" -- sh -c 'echo incus lifecycle OK'
