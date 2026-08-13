#!/usr/bin/env bash
set -euo pipefail

# Runs the Go test suite inside the casjaysdev/go toolchain container.
# Never runs on the host — see AI.md PART 28 / global CLAUDE.md execution
# hierarchy.

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
container_name="shortner-test-$(set +o pipefail; tr -dc 'a-z0-9' </dev/urandom | head -c8)"

docker run --rm --name "$container_name" \
  -v "$project_dir:/app" -w /app \
  -e GOFLAGS=-buildvcs=false \
  -e CGO_ENABLED=0 \
  casjaysdev/go:latest \
  go test -cover ./...
