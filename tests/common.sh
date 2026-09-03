#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Shared helpers for the shortner test scripts (AI.md PART 28)
#
# Sourced by run_tests.sh, docker.sh, incus.sh, e2e.sh and
# test_content_negotiation.sh. Never executed directly.
#
# Everything here obeys the two hard rules from AI.md PART 28:
#   - the project directory holds SOURCE ONLY; all runtime/test data lives
#     under ${TMPDIR:-/tmp}/{project_org}/{internal_name}-XXXXXX/
#   - nothing is ever built or run on the host; the toolchain is always the
#     casjaysdev/go:latest container, mirroring the Makefile's GO_DOCKER

# Absolute project root, resolved from this file's own location so the
# scripts work no matter what the caller's working directory is.
project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Project identity, derived exactly the way the Makefile derives it: the
# git remote wins, the directory layout is the fallback.
project_name="$(git -C "$project_dir" remote get-url origin 2>/dev/null | sed -E 's|\.git/?$||' | sed -E 's|.*/||')"
[ -n "$project_name" ] || project_name="$(basename "$project_dir")"
project_org="$(git -C "$project_dir" remote get-url origin 2>/dev/null | sed -E 's|\.git/?$||' | sed -E 's|.*/([^/]+)/[^/]+$|\1|')"
[ -n "$project_org" ] || project_org="$(basename "$(dirname "$project_dir")")"

# internal_name is the frozen on-disk identifier (AI.md PART 2/4). It
# matches project_name for this project.
internal_name="$project_name"

# Toolchain and runtime images (AI.md PART 28 / global execution hierarchy).
go_image="${SHORTNER_GO_IMAGE:-casjaysdev/go:latest}"
docker_test_image="${SHORTNER_DOCKER_TEST_IMAGE:-alpine:latest}"
incus_test_image="${SHORTNER_INCUS_TEST_IMAGE:-images:debian/trixie}"

# Persistent Go caches on the host, same paths the Makefile uses so a test
# run reuses the module cache instead of re-downloading the world.
go_cache="${SHORTNER_GO_CACHE:-$HOME/go/pkg/mod}"
go_build_cache="${SHORTNER_GO_BUILD:-$HOME/.cache/go-build/$project_name}"

# Build output directory (Makefile BINDIR).
bin_dir="$project_dir/binaries"

# Counters, color, __pass/__fail/__skip, __assert_* and __print_summary live in a
# dependency-free file so the in-container suite can source the same code.
# shellcheck source=tests/assert.sh
. "$project_dir/tests/assert.sh"

# Emits 8 random lowercase alphanumerics, used for unique container names.
__random_suffix() {
  set +o pipefail
  tr -dc 'a-z0-9' </dev/urandom | head -c8
  set -o pipefail
}

# Creates the mandatory temp-dir structure and echoes its path:
#   ${TMPDIR:-/tmp}/{project_org}/{internal_name}-XXXXXX/volumes/{config,data,logs}
__make_temp_dir() {
  local base="${TMPDIR:-/tmp}/$project_org"
  mkdir -p "$base"
  local dir
  dir="$(mktemp -d "$base/${internal_name}-XXXXXX")"
  mkdir -p "$dir/volumes/config" "$dir/volumes/data" "$dir/volumes/logs"
  printf '%s\n' "$dir"
}

# Version string, resolved the same way the Makefile resolves it.
__build_version() {
  if [ -s "$project_dir/release.txt" ]; then
    tr -d '[:space:]' <"$project_dir/release.txt"
  else
    printf '%s' "${VERSION:-devel}"
  fi
}

# Reports whether src/client is a real buildable `package main`. It is, as
# of PART 32, but the probe stays so a tree mid-refactor skips the CLI
# checks loudly instead of failing the whole run on a build error.
__has_client_main() {
  [ -d "$project_dir/src/client" ] || return 1
  grep -rqs --include='*.go' -- '^package main' "$project_dir/src/client" || return 1
  return 0
}

# Builds the linux binaries used by the container suites into binaries/.
# Runs inside casjaysdev/go:latest with CGO disabled, mirroring the
# Makefile's GO_DOCKER recipe. Never builds on the host.
__build_binaries() {
  local version commit epoch site ldflags
  version="$(__build_version)"
  commit="$(git -C "$project_dir" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  epoch="$(date -u +%s)"
  site="https://github.com/$project_org/$project_name"
  ldflags="-s -w"
  ldflags="$ldflags -X 'github.com/$project_org/$project_name/src/common/version.Version=$version'"
  ldflags="$ldflags -X 'github.com/$project_org/$project_name/src/common/version.CommitID=$commit'"
  ldflags="$ldflags -X 'github.com/$project_org/$project_name/src/common/version.BuildEpoch=$epoch'"
  ldflags="$ldflags -X 'github.com/$project_org/$project_name/src/common/version.OfficialSite=$site'"

  mkdir -p "$bin_dir" "$go_cache" "$go_build_cache"

  local build_cmd
  build_cmd="GOOS=linux GOARCH=${TEST_GOARCH:-amd64} go build -buildvcs=false -trimpath -ldflags \"$ldflags\" -o $bin_dir/$project_name ./src"
  if __has_client_main; then
    build_cmd="$build_cmd && GOOS=linux GOARCH=${TEST_GOARCH:-amd64} go build -buildvcs=false -trimpath -ldflags \"$ldflags\" -o $bin_dir/$project_name-cli ./src/client"
  fi

  printf 'Building %s %s in %s...\n' "$project_name" "$version" "$go_image"
  docker run --rm \
    --name "$project_name-build-$(__random_suffix)" \
    --memory="${DOCKER_MEM:-4g}" --cpus="${DOCKER_CPUS:-2}" \
    -v "$project_dir:/app" \
    -v "$go_cache:/usr/local/share/go/pkg/mod" \
    -v "$go_build_cache:/usr/local/share/go/cache" \
    -w /app \
    -e CGO_ENABLED=0 \
    -e GOFLAGS=-buildvcs=false \
    "$go_image" \
    sh -c "$build_cmd"
}

# Reports whether a command exists on the host.
__have_cmd() {
  command -v -- "$1" >/dev/null 2>&1
}
