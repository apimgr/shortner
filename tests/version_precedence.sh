#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_base="${TMPDIR:-/tmp}/apimgr/shortner"
mkdir -p "$temp_base"
temp_dir="$(mktemp -d "$temp_base/version-XXXXXX")"
trap 'rm -rf -- "$temp_dir"' EXIT

cp "$project_dir/Makefile" "$temp_dir/Makefile"
mkdir -p "$temp_dir/tests"
cp "$project_dir/tests/common.sh" "$temp_dir/tests/common.sh"
cp "$project_dir/tests/assert.sh" "$temp_dir/tests/assert.sh"
cp "$project_dir/site.txt" "$temp_dir/site.txt"

assert_equal() {
  local name="$1" expected="$2" actual="$3"
  if [ "$actual" != "$expected" ]; then
    printf '%s: expected %q, got %q\n' "$name" "$expected" "$actual" >&2
    exit 1
  fi
}

# Make expands VERSION when it parses the injected target.
# shellcheck disable=SC2016
print_version_target='version-check:;@printf "%s" "$(VERSION)"'

printf 'fixture-version\n' >"$temp_dir/release.txt"
# Child Bash expands $1 after the source path is supplied.
# shellcheck disable=SC2016
actual="$(env -u VERSION bash -c 'source "$1"; __build_version' _ "$temp_dir/tests/common.sh")"
assert_equal "test helper release fallback" fixture-version "$actual"
actual="$(env -u VERSION make -C "$temp_dir" --no-print-directory -s --eval="$print_version_target" version-check)"
assert_equal "Makefile release fallback" fixture-version "$actual"

actual="$(env VERSION=manual-override make -C "$temp_dir" --no-print-directory -s --eval="$print_version_target" version-check)"
assert_equal "Makefile user override" manual-override "$actual"

: >"$temp_dir/release.txt"
actual="$(env -u VERSION make -C "$temp_dir" --no-print-directory -s --eval="$print_version_target" version-check)"
assert_equal "Makefile empty release fallback" devel "$actual"
rm "$temp_dir/release.txt"
# Child Bash expands $1 after the source path is supplied.
# shellcheck disable=SC2016
actual="$(env -u VERSION bash -c 'source "$1"; __build_version' _ "$temp_dir/tests/common.sh")"
assert_equal "test helper missing release fallback" devel "$actual"

# Child Bash expands $1 after the source path is supplied.
# shellcheck disable=SC2016
actual="$(env OFFICIAL_SITE=https://github.com/apimgr/shortner bash -c 'source "$1"; __build_official_site' _ "$temp_dir/tests/common.sh")"
assert_equal "site file overrides the environment" https://csj.lol "$actual"
rm "$temp_dir/site.txt"
# Child Bash expands $1 after the source path is supplied.
# shellcheck disable=SC2016
actual="$(env OFFICIAL_SITE=https://github.com/apimgr/shortner bash -c 'source "$1"; __build_official_site' _ "$temp_dir/tests/common.sh")"
assert_equal "environment provides site when file is absent" https://github.com/apimgr/shortner "$actual"

printf 'Version precedence checks passed.\n'
