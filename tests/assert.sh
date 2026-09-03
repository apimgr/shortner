#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Assertion and reporting helpers shared by every test script
#
# Sourced by tests/common.sh on the host and by tests/suite.sh inside the
# test container. Deliberately dependency-free (no docker, no git, no
# project paths) so it can be copied into a bare alpine/debian container.

# Test counters, consumed by pass/fail/skip and reported by print_summary.
tests_passed=0
tests_failed=0
tests_skipped=0
failed_names=()

# Color is opt-out via NO_COLOR and auto-disabled when stdout is not a tty.
if [ -n "${NO_COLOR:-}" ] || [ ! -t 1 ]; then
  c_reset='' c_red='' c_green='' c_yellow='' c_blue=''
else
  c_reset=$'\033[0m' c_red=$'\033[31m' c_green=$'\033[32m' c_yellow=$'\033[33m' c_blue=$'\033[34m'
fi

# Prints a section banner.
__section() {
  printf '\n%s=== %s ===%s\n' "$c_blue" "$1" "$c_reset"
}

# Records a passing check.
__pass() {
  tests_passed=$((tests_passed + 1))
  printf '%s  PASS%s  %s\n' "$c_green" "$c_reset" "$1"
  return 0
}

# Records a failing check. Never returns non-zero so that a `set -e` caller
# runs the whole suite instead of aborting on the first failure; the exit
# code is decided once, by __print_summary.
__fail() {
  tests_failed=$((tests_failed + 1))
  failed_names+=("$1")
  printf '%s  FAIL%s  %s\n' "$c_red" "$c_reset" "$1"
  return 0
}

# Records a check that could not run in this environment. A skip is never
# silent and never counts as a pass.
__skip() {
  tests_skipped=$((tests_skipped + 1))
  printf '%s  SKIP%s  %s (%s)\n' "$c_yellow" "$c_reset" "$1" "${2:-not available here}"
  return 0
}

# __assert_eq {label} {expected} {actual}
__assert_eq() {
  if [ "$2" = "$3" ]; then
    __pass "$1"
  else
    __fail "$1 (expected '$2', got '$3')"
  fi
}

# __assert_contains {label} {needle} {haystack}
__assert_contains() {
  case "$3" in
    *"$2"*) __pass "$1" ;;
    *) __fail "$1 (missing '$2')" ;;
  esac
}

# __assert_not_contains {label} {needle} {haystack}
__assert_not_contains() {
  case "$3" in
    *"$2"*) __fail "$1 (unexpectedly contains '$2')" ;;
    *) __pass "$1" ;;
  esac
}

# __assert_nonempty {label} {value}
__assert_nonempty() {
  if [ -n "$2" ]; then
    __pass "$1"
  else
    __fail "$1 (empty value)"
  fi
}

# Prints the run summary and returns 1 when anything failed, so callers can
# `__print_summary || exit 1` (AI.md PART 28 "Test Script Rules": exit codes).
__print_summary() {
  printf '\n%s=== Summary ===%s\n' "$c_blue" "$c_reset"
  printf '  passed:  %s\n' "$tests_passed"
  printf '  failed:  %s\n' "$tests_failed"
  printf '  skipped: %s\n' "$tests_skipped"
  if [ "$tests_failed" -gt 0 ]; then
    printf '\n%sFailed checks:%s\n' "$c_red" "$c_reset"
    local name
    for name in "${failed_names[@]}"; do
      printf '  - %s\n' "$name"
    done
    return 1
  fi
  return 0
}
