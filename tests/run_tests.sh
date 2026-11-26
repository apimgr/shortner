#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Docker-only basic test entry point for shortner (AI.md PART 28)
#
# Runs tests/docker.sh. The Incus-based full-app suite, clean-OS VM suite,
# and browser E2E suite are separate entry points and are never auto-selected.
#
# Usage:
#   ./tests/run_tests.sh
#
# Docker is required. The script exits 69 when Docker is unavailable.

set -eo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ! command -v docker >/dev/null 2>&1; then
  printf 'ERROR: docker not found: the basic test suite requires Docker\n' >&2
  printf 'Install Docker: https://docker.com/\n' >&2
  exit 69
fi

exec "$script_dir/docker.sh"
