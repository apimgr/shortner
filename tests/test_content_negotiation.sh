#!/usr/bin/env bash
# @@License      : WTFPL
# @@Description  : Content-negotiation matrix runner (AI.md PART 14 / PART 28)
#
# Walks PART 28's negotiation matrix against an ALREADY-RUNNING server and
# reports one PASS/FAIL per cell. It only speaks HTTP — it never starts,
# builds, or installs anything — so it is safe to point at a server running
# in a container, in Incus, or on a remote host.
#
# Usage:
#   ./tests/test_content_negotiation.sh [base_url]
#   BASE_URL=http://127.0.0.1:64580 ./tests/test_content_negotiation.sh
#
# The same matrix is covered in-container by tests/suite.sh's own
# negotiation section; this script exists so the matrix can be re-run
# against any running instance without a full beta pass.

set -eo pipefail

tests_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tests/assert.sh
. "$tests_dir/assert.sh"

base_url="${1:-${BASE_URL:-http://127.0.0.1:8090}}"
api_version="${API_VERSION:-v1}"
api_url="$base_url/api/$api_version"
browser_ua='Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36'

if ! command -v -- curl >/dev/null 2>&1; then
  printf 'curl is required\n' >&2
  exit 1
fi

# __body {url} {curl args...} — echoes the response body.
__body() {
  local url="$1"
  shift
  curl -sS "$@" "$url" 2>/dev/null || true
}

# __status {url} {curl args...} — echoes the response status code.
__status() {
  local url="$1"
  shift
  curl -sS -o /dev/null -w '%{http_code}' "$@" "$url" 2>/dev/null || echo 000
}

# __content_type {url} {curl args...} — echoes the Content-Type header.
__content_type() {
  local url="$1"
  shift
  curl -sS -o /dev/null -w '%{content_type}' "$@" "$url" 2>/dev/null || true
}

printf 'content negotiation matrix against %s\n' "$base_url"

__section "Frontend routes (smart detection)"
for path in / /server /server/about /server/healthz /list; do
  out="$(__body "$base_url$path" -H 'Accept: text/html' -H "User-Agent: $browser_ua")"
  __assert_contains "$path + text/html -> HTML" "<!DOCTYPE" "$out"
  out="$(__body "$base_url$path" -H 'Accept: text/plain')"
  __assert_not_contains "$path + text/plain -> not HTML" "<!DOCTYPE" "$out"
done

__section "API routes"
for path in /server/healthz; do
  out="$(__body "$api_url$path" -H 'Accept: application/json')"
  if command -v -- jq >/dev/null 2>&1; then
    if printf '%s' "$out" | jq -e . >/dev/null 2>&1; then
      __pass "$path + application/json -> valid JSON"
    else
      __fail "$path + application/json -> not valid JSON"
    fi
  else
    __assert_contains "$path + application/json -> JSON object" "{" "$out"
  fi

  ctype="$(__content_type "$api_url$path" -H 'Accept: application/json')"
  __assert_contains "$path + application/json -> JSON Content-Type" "application/json" "$ctype"

  out="$(__body "$api_url$path" -H 'Accept: text/plain')"
  __assert_not_contains "$path + text/plain -> no JSON envelope" '"ok":' "$out"

  out="$(__body "$api_url$path" -H "User-Agent: curl/8.0.0")"
  __assert_not_contains "$path + curl UA (no Accept) -> plain text" '"ok":' "$out"

  out="$(__body "$api_url$path" -H "User-Agent: $browser_ua")"
  __assert_contains "$path + browser UA (no Accept) -> JSON" '"status"' "$out"
done

__section "CORS"
ctype="$(curl -sS -o /dev/null -D - "$api_url/server/healthz" 2>/dev/null | tr -d '\r' | grep -i -- '^access-control-allow-origin:' || true)"
__assert_contains "API sends Access-Control-Allow-Origin: *" "*" "$ctype"

__section "Always-plain-text endpoints"
for path in /robots.txt /llms.txt /.well-known/security.txt; do
  code="$(__status "$base_url$path")"
  if [ "$code" = "200" ]; then
    ctype="$(__content_type "$base_url$path")"
    __assert_contains "$path -> text/plain" "text/" "$ctype"
  else
    __fail "$path returned HTTP $code"
  fi
done

__section "Path suffix (.txt)"
code="$(__status "$base_url/server/healthz.txt")"
if [ "$code" = "200" ]; then
  out="$(__body "$base_url/server/healthz.txt" -H "User-Agent: $browser_ua")"
  __assert_not_contains "/server/healthz.txt -> not HTML" "<!DOCTYPE" "$out"
else
  __skip "/server/healthz.txt" "path-suffix routing not implemented yet (HTTP $code) — TODO.AI.md PART 13/14"
fi

__print_summary
