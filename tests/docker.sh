#!/usr/bin/env bash
set -euo pipefail

# Builds and runs shortner via docker/docker-compose.test.yml. This is the
# only compose file AI may run directly — it uses tmpfs volumes, never a
# project-directory bind mount, so no temp-dir copy is required. See
# AI.md PART 28 "AI Docker Compose Rules" — docker-compose.yml and
# docker-compose.dev.yml are human-only and must never be run by AI.

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cleanup() {
  (cd "$project_dir/docker" && docker compose -f docker-compose.test.yml down --remove-orphans) || true
}
trap cleanup EXIT

cd "$project_dir/docker"
docker compose -f docker-compose.test.yml build \
  --build-arg VERSION="$(cat "$project_dir/release.txt")"
docker compose -f docker-compose.test.yml up -d
docker compose -f docker-compose.test.yml ps
docker compose -f docker-compose.test.yml logs --tail=50
